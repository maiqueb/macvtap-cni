// Copyright 2020 CNI authors
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
	"fmt"

	"github.com/containernetworking/cni/pkg/skel"
	"github.com/containernetworking/cni/pkg/types"
	"github.com/containernetworking/plugins/pkg/ns"
	"github.com/containernetworking/plugins/pkg/testutils"

	"github.com/vishvananda/netlink"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

const MASTER_NAME = "eth0"
const macAddress = "0a:59:00:dc:6a:e0"

var _ = Describe("allowed configurations", func() {
	It("accepts a configuration w/ the 'master' attribute.", func() {
		conf := fmt.Sprintf(`{
    		"cniVersion": "0.3.1",
    		"name": "mynet",
    		"type": "macvtap",
    		"master": "%s"
		}`, MASTER_NAME)
		netConf, _, err := loadConf([]byte(conf))
		Expect(err).NotTo(HaveOccurred())
		Expect(netConf.Master).To(Equal(MASTER_NAME))
	})
	It("accepts a configuration w/ the 'deviceID' attribute.", func() {
		macvtapIfaceName := "vtap0"
		conf := fmt.Sprintf(`{
    		"cniVersion": "0.3.1",
    		"name": "mynet",
    		"type": "macvtap",
    		"deviceID": "%s"
		}`, macvtapIfaceName)
		netConf, _, err := loadConf([]byte(conf))
		Expect(err).NotTo(HaveOccurred())
		Expect(netConf.DeviceID).To(Equal(macvtapIfaceName))
	})
	It("does not accept 'master' *and* 'deviceID' attributes.", func() {
		macvtapIfaceName := "vtap0"
		conf := fmt.Sprintf(`{
    		"cniVersion": "0.3.1",
    		"name": "mynet",
			"type": "macvtap",
			"master": "eth1",
    		"deviceID": "%s"
		}`, macvtapIfaceName)
		_, _, err := loadConf([]byte(conf))
		Expect(err).To(HaveOccurred())
	})
	It("requires either 'master' *or* 'deviceID' attributes.", func() {
		macvtapIfaceName := "vtap0"
		conf := fmt.Sprintf(`{
    		"cniVersion": "0.3.1",
    		"name": "mynet",
			"type": "macvtap",
			"master": "eth1",
    		"deviceID": "%s"
		}`, macvtapIfaceName)
		_, _, err := loadConf([]byte(conf))
		Expect(err).To(HaveOccurred())
	})
})

var _ = Describe("macvtap Operations", func() {
	var originalNS ns.NetNS

	BeforeEach(func() {
		// Create a new NetNS so we don't modify the host
		var err error
		originalNS, err = testutils.NewNS()
		Expect(err).NotTo(HaveOccurred())

		err = originalNS.Do(func(ns.NetNS) error {
			defer GinkgoRecover()

			// Add master
			err = netlink.LinkAdd(&netlink.Dummy{
				LinkAttrs: netlink.LinkAttrs{
					Name: MASTER_NAME,
				},
			})
			Expect(err).NotTo(HaveOccurred())
			_, err = netlink.LinkByName(MASTER_NAME)
			Expect(err).NotTo(HaveOccurred())
			return nil
		})
		Expect(err).NotTo(HaveOccurred())
	})

	AfterEach(func() {
		Expect(originalNS.Close()).To(Succeed())
		Expect(testutils.UnmountNS(originalNS)).To(Succeed())
	})

	It("creates an macvtap link in a non-default namespace", func() {
		conf := &NetConf{
			NetConf: types.NetConf{
				CNIVersion: "0.3.1",
				Name:       "testConfig",
				Type:       "macvtap",
			},
			Master: MASTER_NAME,
			Mode:   "bridge",
			MTU:    1500,
		}

		targetNs, err := testutils.NewNS()
		Expect(err).NotTo(HaveOccurred())
		defer targetNs.Close()

		err = originalNS.Do(func(ns.NetNS) error {
			defer GinkgoRecover()

			_, err := createMacvtap(conf, "foobar0", targetNs)
			Expect(err).NotTo(HaveOccurred())
			return nil
		})
		Expect(err).NotTo(HaveOccurred())

		// Make sure macvtap link exists in the target namespace
		err = targetNs.Do(func(ns.NetNS) error {
			defer GinkgoRecover()

			link, err := netlink.LinkByName("foobar0")
			Expect(err).NotTo(HaveOccurred())
			Expect(link.Attrs().Name).To(Equal("foobar0"))
			return nil
		})
		Expect(err).NotTo(HaveOccurred())
	})
	It("imports an existing macvtap link in a non-default namespace", func() {
		macvtapIfaceName := "mymacvtap0"

		// create the initial macvtap
		conf := &NetConf{
			NetConf: types.NetConf{
				CNIVersion: "0.3.1",
				Name:       "testConfig",
				Type:       "macvtap",
			},
			Master: MASTER_NAME,
			Mode:   "bridge",
		}
		targetNs, err := testutils.NewNS()
		Expect(err).NotTo(HaveOccurred())
		defer targetNs.Close()

		err = originalNS.Do(func(ns.NetNS) error {
			defer GinkgoRecover()

			_, err := createMacvtap(conf, macvtapIfaceName, originalNS)
			Expect(err).NotTo(HaveOccurred())
			return nil
		})
		Expect(err).NotTo(HaveOccurred())

		updatedMtu := 1000
		conf = &NetConf{
			NetConf: types.NetConf{
				CNIVersion: "0.3.1",
				Name:       "testConfig",
				Type:       "macvtap",
			},
			Master:   MASTER_NAME,
			Mode:     "bridge",
			MTU:      updatedMtu,
			DeviceID: macvtapIfaceName,
		}

		Expect(err).NotTo(HaveOccurred())

		err = originalNS.Do(func(ns.NetNS) error {
			defer GinkgoRecover()

			_, err := configureMacvtap(conf, macvtapIfaceName, targetNs)
			Expect(err).NotTo(HaveOccurred())
			return nil
		})
		Expect(err).NotTo(HaveOccurred())

		// Make sure macvtap link exists with the updated configurations
		err = targetNs.Do(func(ns.NetNS) error {
			defer GinkgoRecover()

			link, err := netlink.LinkByName(macvtapIfaceName)
			Expect(err).NotTo(HaveOccurred())
			Expect(link.Attrs().Name).To(Equal(macvtapIfaceName))
			Expect(link.Attrs().MTU).To(Equal(updatedMtu))
			return nil
		})
		Expect(err).NotTo(HaveOccurred())
	})

	It("configures and deconfigures a macvtap link having a user specified mac address with ADD/DEL", func() {
		const IFNAME = "macvt0"

		conf := fmt.Sprintf(`{
    		"cniVersion": "0.3.1",
    		"name": "mynet",
    		"type": "macvtap",
    		"master": "%s"
		}`, MASTER_NAME)

		targetNs, err := testutils.NewNS()
		Expect(err).NotTo(HaveOccurred())
		defer targetNs.Close()

		args := &skel.CmdArgs{
			ContainerID: "dummy",
			Netns:       targetNs.Path(),
			IfName:      IFNAME,
			StdinData:   []byte(conf),
			Args:        fmt.Sprintf("MAC=%s", macAddress),
		}

		// Make sure macvtap link exists in the target namespace
		err = originalNS.Do(func(ns.NetNS) error {
			defer GinkgoRecover()

			_, _, err := testutils.CmdAdd(args.Netns, args.ContainerID, args.IfName, args.StdinData, func() error { return cmdAdd(args) })
			Expect(err).NotTo(HaveOccurred())
			return nil
		})
		Expect(err).NotTo(HaveOccurred())

		// Make sure macvtap link exists in the target namespace
		err = targetNs.Do(func(ns.NetNS) error {
			defer GinkgoRecover()

			link, err := netlink.LinkByName(IFNAME)
			Expect(err).NotTo(HaveOccurred())
			Expect(link.Attrs().Name).To(Equal(IFNAME))
			Expect(link.Attrs().HardwareAddr.String()).To(Equal(macAddress))

			return nil
		})
		Expect(err).NotTo(HaveOccurred())

		err = originalNS.Do(func(ns.NetNS) error {
			defer GinkgoRecover()

			args := &skel.CmdArgs{
				ContainerID: "dummy",
				Netns:       targetNs.Path(),
				IfName:      IFNAME,
				StdinData:   []byte(conf),
			}

			err := testutils.CmdDel(args.Netns, args.ContainerID, args.IfName, func() error {
				return cmdDel(args)
			})
			Expect(err).NotTo(HaveOccurred())
			return nil
		})
		Expect(err).NotTo(HaveOccurred())

		// Make sure macvtap link has been deleted
		err = targetNs.Do(func(ns.NetNS) error {
			defer GinkgoRecover()

			link, err := netlink.LinkByName(IFNAME)
			Expect(err).To(HaveOccurred())
			Expect(link).To(BeNil())
			return nil
		})
		Expect(err).NotTo(HaveOccurred())
	})
	It("configures and deconfigures a macvtap link having an auto-generated mac address with ADD/DEL", func() {
		const IFNAME = "macvt0"

		conf := fmt.Sprintf(`{
    		"cniVersion": "0.3.1",
    		"name": "mynet",
    		"type": "macvtap",
    		"master": "%s"
		}`, MASTER_NAME)

		targetNs, err := testutils.NewNS()
		Expect(err).NotTo(HaveOccurred())
		defer targetNs.Close()

		args := &skel.CmdArgs{
			ContainerID: "dummy",
			Netns:       targetNs.Path(),
			IfName:      IFNAME,
			StdinData:   []byte(conf),
		}

		// Make sure macvtap link exists in the target namespace
		err = originalNS.Do(func(ns.NetNS) error {
			defer GinkgoRecover()

			_, _, err := testutils.CmdAdd(args.Netns, args.ContainerID, args.IfName, args.StdinData, func() error { return cmdAdd(args) })
			Expect(err).NotTo(HaveOccurred())
			return nil
		})
		Expect(err).NotTo(HaveOccurred())

		// Make sure macvtap link exists in the target namespace
		err = targetNs.Do(func(ns.NetNS) error {
			defer GinkgoRecover()

			link, err := netlink.LinkByName(IFNAME)
			Expect(err).NotTo(HaveOccurred())
			Expect(link.Attrs().Name).To(Equal(IFNAME))
			hwAddr := fmt.Sprintf("%s", link.Attrs().HardwareAddr)
			Expect(hwAddr).NotTo(BeNil())

			return nil
		})
		Expect(err).NotTo(HaveOccurred())

		err = originalNS.Do(func(ns.NetNS) error {
			defer GinkgoRecover()

			args := &skel.CmdArgs{
				ContainerID: "dummy",
				Netns:       targetNs.Path(),
				IfName:      IFNAME,
				StdinData:   []byte(conf),
			}

			err := testutils.CmdDel(args.Netns, args.ContainerID, args.IfName, func() error {
				return cmdDel(args)
			})
			Expect(err).NotTo(HaveOccurred())
			return nil
		})
		Expect(err).NotTo(HaveOccurred())

		// Make sure macvtap link has been deleted
		err = targetNs.Do(func(ns.NetNS) error {
			defer GinkgoRecover()

			link, err := netlink.LinkByName(IFNAME)
			Expect(err).To(HaveOccurred())
			Expect(link).To(BeNil())
			return nil
		})
		Expect(err).NotTo(HaveOccurred())
	})
	It("fails to configure a macvtap device with invalid env args", func() {
		const IFNAME = "macvt0"

		conf := fmt.Sprintf(`{
    		"cniVersion": "0.3.1",
    		"name": "mynet",
    		"type": "macvtap",
    		"master": "%s"
		}`, MASTER_NAME)

		targetNs, err := testutils.NewNS()
		Expect(err).NotTo(HaveOccurred())
		defer targetNs.Close()

		args := &skel.CmdArgs{
			ContainerID: "dummy",
			Netns:       targetNs.Path(),
			IfName:      IFNAME,
			StdinData:   []byte(conf),
			Args:        "WHY=petr_made_me_do_it",
		}

		// expect the macvtap creation to have failed, because of the invalid argument
		err = originalNS.Do(func(ns.NetNS) error {
			defer GinkgoRecover()

			_, _, err := testutils.CmdAdd(args.Netns, args.ContainerID, args.IfName, args.StdinData, func() error { return cmdAdd(args) })
			Expect(err).To(HaveOccurred())
			return nil
		})
		Expect(err).NotTo(HaveOccurred())

		// Make sure macvtap link does not exist in the target namespace
		err = targetNs.Do(func(ns.NetNS) error {
			defer GinkgoRecover()

			_, err := netlink.LinkByName(IFNAME)
			Expect(err).To(HaveOccurred())
			return nil
		})
		Expect(err).NotTo(HaveOccurred())
	})
	It("configures and deconfigures an already existent macvtap link via ADD/DEL", func() {
		macvtapIfaceName := "mymacvtap0"

		// create the initial macvtap
		conf := &NetConf{
			NetConf: types.NetConf{
				CNIVersion: "0.3.1",
				Name:       "testConfig",
				Type:       "macvtap",
			},
			Master: MASTER_NAME,
			Mode:   "bridge",
		}
		targetNs, err := testutils.NewNS()
		Expect(err).NotTo(HaveOccurred())
		defer targetNs.Close()

		err = originalNS.Do(func(ns.NetNS) error {
			defer GinkgoRecover()

			_, err := createMacvtap(conf, macvtapIfaceName, originalNS)
			Expect(err).NotTo(HaveOccurred())
			return nil
		})
		Expect(err).NotTo(HaveOccurred())

		cniConf := fmt.Sprintf(`{
    		"cniVersion": "0.3.1",
    		"name": "mynet",
    		"type": "macvtap",
			"deviceID": "%s"
		}`, macvtapIfaceName)

		args := &skel.CmdArgs{
			ContainerID: "dummy",
			Netns:       targetNs.Path(),
			IfName:      macvtapIfaceName,
			StdinData:   []byte(cniConf),
		}

		// Make sure macvtap link exists in the target namespace
		err = originalNS.Do(func(ns.NetNS) error {
			defer GinkgoRecover()

			_, _, err := testutils.CmdAdd(args.Netns, args.ContainerID, args.IfName, args.StdinData, func() error { return cmdAdd(args) })
			Expect(err).NotTo(HaveOccurred())
			return nil
		})
		Expect(err).NotTo(HaveOccurred())

		// Make sure macvtap link exists in the target namespace
		err = targetNs.Do(func(ns.NetNS) error {
			defer GinkgoRecover()

			link, err := netlink.LinkByName(macvtapIfaceName)
			Expect(err).NotTo(HaveOccurred())
			Expect(link.Attrs().Name).To(Equal(macvtapIfaceName))

			return nil
		})
		Expect(err).NotTo(HaveOccurred())

		err = originalNS.Do(func(ns.NetNS) error {
			defer GinkgoRecover()

			args := &skel.CmdArgs{
				ContainerID: "dummy",
				Netns:       targetNs.Path(),
				IfName:      macvtapIfaceName,
				StdinData:   []byte(cniConf),
			}

			err := testutils.CmdDel(args.Netns, args.ContainerID, args.IfName, func() error {
				return cmdDel(args)
			})
			Expect(err).NotTo(HaveOccurred())
			return nil
		})
		Expect(err).NotTo(HaveOccurred())

		// Make sure macvtap link has been deleted
		err = targetNs.Do(func(ns.NetNS) error {
			defer GinkgoRecover()

			link, err := netlink.LinkByName(macvtapIfaceName)
			Expect(err).To(HaveOccurred())
			Expect(link).To(BeNil())
			return nil
		})
		Expect(err).NotTo(HaveOccurred())
	})
	It("assures the macvtap interface cannot be created with an MTU larger than the lower device", func() {
		const IFNAME = "macvt0"

		conf := fmt.Sprintf(`{
    		"cniVersion": "0.3.1",
    		"name": "mynet",
    		"type": "macvtap",
			"master": "%s",
			"mtu": 9000
		}`, MASTER_NAME)

		targetNs, err := testutils.NewNS()
		Expect(err).NotTo(HaveOccurred())
		defer targetNs.Close()

		args := &skel.CmdArgs{
			ContainerID: "dummy",
			Netns:       targetNs.Path(),
			IfName:      IFNAME,
			StdinData:   []byte(conf),
		}

		err = originalNS.Do(func(ns.NetNS) error {
			defer GinkgoRecover()

			_, _, err := testutils.CmdAdd(args.Netns, args.ContainerID, args.IfName, args.StdinData, func() error { return cmdAdd(args) })
			Expect(err).To(HaveOccurred())
			return err
		})
		Expect(err).To(HaveOccurred())
	})
})
