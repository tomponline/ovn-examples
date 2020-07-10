#!/bin/bash
# Setup external provider using OVS bridge.
ip link add dev ovs-lxdbr0-a type veth peer name ovs-lxdbr0-b
sysctl net.ipv6.conf.ovs-lxdbr0-a.disable_ipv6=1 \
        net.ipv6.conf.ovs-lxdbr0-a.forwarding=0 \
        net.ipv6.conf.ovs-lxdbr0-b.disable_ipv6=1 \
        net.ipv6.conf.ovs-lxdbr0-b.forwarding=0
ip link set master lxdbr0 up ovs-lxdbr0-a
ip link set ovs-lxdbr0-b up
ovs-vsctl --may-exist add-br ovs-lxdbr0
ovs-vsctl --may-exist add-port ovs-lxdbr0 ovs-lxdbr0-b
ovs-vsctl set open . external-ids:ovn-bridge-mappings=lxdbr0:ovs-lxdbr0
