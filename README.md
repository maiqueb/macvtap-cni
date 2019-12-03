# macvtap-cni

## Overview
TODO

## Example Configuration

```json
{
    "name": "mynet",
    "type": "macvtap",
    "master": "eth0",
    "mode": "bridge",
    "mtu": 1500,
    "mac": "02:03:04:05:06:07"
}
```

## Network Configuration Reference

* `name`   (string, required): the name of the network.
* `type`   (string, required): "ovs".
* `master` (string, required): name of the parent interface.
* `mode`   (string, optional): mode of the communication between endpoints. Can
  be either *vepa*, *bridge*, or *private*. Defauls to *bridge*.
* `mtu`    (integer, optional): mtu to set in the macvtap interface.

## Manual Testing

```shell
# Build the binary
go build

# Create a new namespace
ip netns add ns1

# Run ADD command connecting the namespace to the host iface 
cat <<EOF | CNI_COMMAND=ADD CNI_CONTAINERID=ns1 CNI_NETNS=/var/run/netns/ns1 CNI_IFNAME=eth2 CNI_PATH=`pwd` ./macvtap-cni
{
    "name": "mynet",
    "type": "macvtap",
    "master": "eth0",
    "mode": "bridge",
    "mtu": 1500,
    "mac": "02:03:04:05:06:07"
}
EOF

# Check that a veth pair was connected inside the namespace
ip netns exec ns1 ip link

# Run DEL command removing the veth pair and OVS port
cat <<EOF | CNI_COMMAND=DEL CNI_CONTAINERID=ns1 CNI_NETNS=/var/run/netns/ns1 CNI_IFNAME=eth2 CNI_PATH=`pwd` ./macvtap-cni
{
    "name": "mynet",
    "type": "macvtap",
    "master": "eth0",
    "mode": "bridge",
    "mtu": 1500,
    "mac": "02:03:04:05:06:07"
}
EOF

# Check that veth pair was removed from the namespace
ip netns exec ns1 ip link

# Delete the namespace
ip netns del ns1
```

