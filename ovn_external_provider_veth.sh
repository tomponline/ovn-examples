#!/bin/bash
# Setup external provider using veth pair.
ip link add dev veth-lxdbr0-a type veth peer name veth-lxdbr0-b
sysctl net.ipv6.conf.veth-lxdbr0-a.disable_ipv6=1 \
        net.ipv6.conf.veth-lxdbr0-a.forwarding=0 \
        net.ipv6.conf.veth-lxdbr0-b.disable_ipv6=1 \
        net.ipv6.conf.veth-lxdbr0-b.forwarding=0
ip link set master lxdbr0 up veth-lxdbr0-a
ip link set veth-lxdbr0-b up
ovs-vsctl --may-exist add-port br-int veth-lxdbr0-b
ovs-vsctl set interface veth-lxdbr0-b external_ids:iface-id=project1-net1-lsp-parent-ext
