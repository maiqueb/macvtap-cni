// Copyright 2019 CNI authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package main

import (
	"encoding/json"
	"fmt"
	"net"
	"runtime"

	"github.com/vishvananda/netlink"

	"github.com/containernetworking/cni/pkg/skel"
	"github.com/containernetworking/cni/pkg/types"
	"github.com/containernetworking/cni/pkg/types/current"
	"github.com/containernetworking/cni/pkg/version"

	"github.com/containernetworking/plugins/pkg/ip"
	"github.com/containernetworking/plugins/pkg/ns"
	bv "github.com/containernetworking/plugins/pkg/utils/buildversion"
	"github.com/containernetworking/plugins/pkg/utils/sysctl"
)

const (
	IPv4InterfaceArpProxySysctlTemplate = "net.ipv4.conf.%s.proxy_arp"
)

type NetConf struct {
	types.NetConf
	Master   string `json:"master"`
	Mode     string `json:"mode"`
	MTU      int    `json:"mtu,omitempty"`
	DeviceID string `json:"deviceID,omitempty"`
}

type EnvArgs struct {
	types.CommonArgs
	MAC types.UnmarshallableString `json:"mac,omitempty"`
}

func init() {
	// this ensures that main runs only on main thread (thread group leader).
	// since namespace ops (unshare, setns) are done for a single thread, we
	// must ensure that the goroutine does not jump from OS thread to thread
	runtime.LockOSThread()
}

func loadConf(bytes []byte) (*NetConf, string, error) {
	n := &NetConf{}
	if err := json.Unmarshal(bytes, n); err != nil {
		return nil, "", fmt.Errorf("failed to load netconf: %v", err)
	}

	if n.Master != "" && n.DeviceID != "" {
		return nil, "", fmt.Errorf(`""deviceID" attribute cannot be used with "master" attribute."`)
	} else if n.Master == "" && n.DeviceID == "" {
		return nil, "", fmt.Errorf(`"Either (exclusive) "deviceID" or "master" attributes are required."`)
	}

	return n, n.CNIVersion, nil
}

func validateConf(netConf NetConf) error {
	if netConf.Master != "" {
		masterMTU, err := getMTUByName(netConf.Master)
		// check existing and MTU of master interface
		if err != nil {
			return err
		}
		if netConf.MTU < 0 || netConf.MTU > masterMTU {
			return fmt.Errorf("invalid MTU %d, must be [0, master MTU(%d)]", netConf.MTU, masterMTU)
		}
	}
	return nil
}

func getEnvArgs(envArgsString string) (EnvArgs, error) {
	if envArgsString != "" {
		e := EnvArgs{}
		err := types.LoadArgs(envArgsString, &e)
		if err != nil {
			return EnvArgs{}, err
		}
		return e, nil
	}
	return EnvArgs{}, nil
}

func getMTUByName(ifName string) (int, error) {
	link, err := netlink.LinkByName(ifName)
	if err != nil {
		return 0, err
	}
	return link.Attrs().MTU, nil
}

func modeFromString(s string) (netlink.MacvlanMode, error) {
	switch s {
	case "", "bridge":
		return netlink.MACVLAN_MODE_BRIDGE, nil
	case "private":
		return netlink.MACVLAN_MODE_PRIVATE, nil
	case "vepa":
		return netlink.MACVLAN_MODE_VEPA, nil
	default:
		return 0, fmt.Errorf("unknown macvtap mode: %q", s)
	}
}

func modeToString(mode netlink.MacvlanMode) (string, error) {
	switch mode {
	case netlink.MACVLAN_MODE_BRIDGE:
		return "bridge", nil
	case netlink.MACVLAN_MODE_PRIVATE:
		return "private", nil
	case netlink.MACVLAN_MODE_VEPA:
		return "vepa", nil
	default:
		return "", fmt.Errorf("unknown macvtap mode: %q", mode)
	}
}

func createMacvtap(conf *NetConf, ifName string, netns ns.NetNS) (*current.Interface, error) {
	macvlan := &current.Interface{Name: ifName}

	mode, err := modeFromString(conf.Mode)
	if err != nil {
		return nil, err
	}

	m, err := netlink.LinkByName(conf.Master)
	if err != nil {
		return nil, fmt.Errorf("failed to lookup master %q: %v", conf.Master, err)
	}

	// due to kernel bug we have to create with tmpName or it might
	// collide with the name on the host and error out
	tmpName, err := ip.RandomVethName()
	if err != nil {
		return nil, err
	}

	mv := &netlink.Macvtap{
		Macvlan: netlink.Macvlan{
			LinkAttrs: netlink.LinkAttrs{
				MTU:         conf.MTU,
				Name:        tmpName,
				ParentIndex: m.Attrs().Index,
				Namespace:   netlink.NsFd(int(netns.Fd())),
				TxQLen:      m.Attrs().TxQLen,
			},
			Mode: mode,
		},
	}
	if err := netlink.LinkAdd(mv); err != nil {
		return nil, fmt.Errorf("failed to create macvtap: %v", err)
	}

	err = configureArp(mv, netns)
	if err != nil {
		return nil, err
	}
	err = updateMacvtapIface(mv, macvlan, ifName, netns)
	if err != nil {
		return nil, err
	}
	return macvlan, nil
}

func configureArp(macvtapConfig netlink.Link, netns ns.NetNS) error {
	err := netns.Do(func(_ ns.NetNS) error {
		// TODO: duplicate following lines for ipv6 support, when it will be added in other places
		ipv4SysctlValueName := fmt.Sprintf(IPv4InterfaceArpProxySysctlTemplate, macvtapConfig.Attrs().Name)
		if _, err := sysctl.Sysctl(ipv4SysctlValueName, "1"); err != nil {
			// remove the newly added link and ignore errors, because we already are in a failed state
			_ = netlink.LinkDel(macvtapConfig)
			return fmt.Errorf("failed to set proxy_arp on newly added interface %q: %v", macvtapConfig.Attrs().Name, err)
		}
		return nil
	})
	return err
}

func updateMacvtapIface(macvtapLink netlink.Link, macvtapIface *current.Interface, ifaceName string, netns ns.NetNS) error {
	err := netns.Do(func(_ ns.NetNS) error {
		err := ip.RenameLink(macvtapLink.Attrs().Name, ifaceName)
		if err != nil {
			_ = netlink.LinkDel(macvtapLink)
			return fmt.Errorf("failed to rename macvlan to %q: %v", ifaceName, err)
		}

		updatedLink := macvtapLink
		updatedLink.Attrs().Name = ifaceName
		if err := netlink.LinkSetUp(updatedLink); err != nil {
			return fmt.Errorf("failed to set macvtap iface up: %v", err)
		}
		// Re-fetch macvlan to get all properties/attributes
		contMacvlan, err := netlink.LinkByName(ifaceName)
		if err != nil {
			return fmt.Errorf("failed to refetch macvlan %q: %v", ifaceName, err)
		}
		macvtapIface.Mac = contMacvlan.Attrs().HardwareAddr.String()
		macvtapIface.Sandbox = netns.Path()

		return nil
	})
	return err
}

func configureMacvtap(conf *NetConf, ifName string, netns ns.NetNS) (*current.Interface, error) {
	iface, err := netlink.LinkByName(conf.DeviceID)
	if err != nil {
		return nil, fmt.Errorf("failed to lookup device %q: %v", conf.DeviceID, err)
	}
	if err := netlink.LinkSetNsFd(iface, int(netns.Fd())); err != nil {
		return nil, fmt.Errorf("failed to move iface %s to the netns %d because: %v", iface, netns.Fd(), err)
	}
	err = netns.Do(func(_ ns.NetNS) error {
		if err := netlink.LinkSetMTU(iface, conf.MTU); err != nil {
			return fmt.Errorf("failed to set the macvtap MTU for %s: %v", conf.DeviceID, err)
		}
		return nil
	})
	macvtap := &current.Interface{Name: ifName}
	err = configureArp(iface, netns)
	if err != nil {
		return nil, err
	}
	err = updateMacvtapIface(iface, macvtap, ifName, netns)
	if err != nil {
		return nil, err
	}
	return macvtap, err
}

func cmdAdd(args *skel.CmdArgs) error {
	n, cniVersion, err := loadConf(args.StdinData)
	if err != nil {
		return err
	}
	if err = validateConf(*n); err != nil {
		return err
	}

	netns, err := ns.GetNS(args.Netns)
	if err != nil {
		return fmt.Errorf("failed to open netns %q: %v", netns, err)
	}
	defer netns.Close()

	var macvtapInterface *current.Interface
	if n.DeviceID != "" {
		macvtapInterface, err = configureMacvtap(n, args.IfName, netns)
	} else {
		macvtapInterface, err = createMacvtap(n, args.IfName, netns)
	}
	if err != nil {
		return err
	}

	// Delete link if err to avoid link leak in this ns
	defer func() {
		if err != nil {
			netns.Do(func(_ ns.NetNS) error {
				return ip.DelLinkByName(args.IfName)
			})
		}
	}()

	envArgs, err := getEnvArgs(args.Args)
	if err != nil {
		return err
	}

	var mac net.HardwareAddr
	if envArgs.MAC != "" {
		mac, err = net.ParseMAC(string(envArgs.MAC))
		if err != nil {
			return err
		}
	}

	if mac.String() != "" {
		err = netns.Do(func(_ ns.NetNS) error {
			macIf, err := netlink.LinkByName(args.IfName)
			if err != nil {
				return fmt.Errorf("failed to lookup new macvtapdevice %q: %v", args.IfName, err)
			}

			if err = netlink.LinkSetHardwareAddr(macIf, mac); err != nil {
				return fmt.Errorf("failed to add hardware addr to %q: %v", args.IfName, err)
			}
			return nil
		})

		if err != nil {
			return err
		}
	}

	result := &current.Result{
		CNIVersion: cniVersion,
		Interfaces: []*current.Interface{macvtapInterface},
	}

	return types.PrintResult(result, cniVersion)
}

func cmdDel(args *skel.CmdArgs) error {
	if args.Netns == "" {
		return nil
	}

	// There is a netns so try to clean up. Delete can be called multiple times
	// so don't return an error if the device is already removed.
	err := ns.WithNetNSPath(args.Netns, func(_ ns.NetNS) error {

		if err := ip.DelLinkByName(args.IfName); err != nil {
			if err != ip.ErrLinkNotFound {
				return err
			}
		}
		return nil
	})

	return err
}

func cmdCheck(args *skel.CmdArgs) error {
	return nil
}

func main() {
	skel.PluginMain(cmdAdd, cmdCheck, cmdDel, version.All, bv.BuildString("macvtap"))
}
