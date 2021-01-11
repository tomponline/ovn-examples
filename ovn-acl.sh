set -xe
# Vars.
switchName="lxd-net9-ls-int"
routerPort=""${switchName}"-lsp-router"
routerIP4="10.164.241.1"
routerIP6="fd42:2f0b:37e5:4157::1"
dnsIP4="10.102.242.1"
dnsIP6="fd42:442d:225e:59df::1"

# Clear all rules for a switch.
sudo ovn-nbctl clear logical_switch "${switchName}" acls

# Baseline switch rules.
sudo ovn-nbctl --type=switch --log acl-add "${switchName}" to-lport 0 1 drop # Default log and drop
sudo ovn-nbctl --type=switch acl-add "${switchName}" to-lport 1 "arp" allow # ARP
sudo ovn-nbctl --type=switch acl-add "${switchName}" to-lport 1 "nd" allow # Neighbour discovery
sudo ovn-nbctl --type=switch acl-add "${switchName}" to-lport 1 "icmp6.type == 143" allow # Multicast listener report
sudo ovn-nbctl --type=switch acl-add "${switchName}" to-lport 1 "inport == \"${routerPort}\" && nd_ra" allow # Router adverts from router
sudo ovn-nbctl --type=switch acl-add "${switchName}" to-lport 1 "outport == \"${routerPort}\" && nd_rs" allow # Router solicitation to router
sudo ovn-nbctl --type=switch acl-add "${switchName}" to-lport 1 "outport == \"${routerPort}\" && udp.dst == 67" allow # DHCPv4 to router
sudo ovn-nbctl --type=switch acl-add "${switchName}" to-lport 1 "outport == \"${routerPort}\" && udp.dst == 547" allow # DHCPv6 to router
sudo ovn-nbctl --type=switch acl-add "${switchName}" to-lport 1 "outport == \"${routerPort}\" && icmp4.type == 8 && ip4.dst == ${routerIP4}" allow # Ping to router IP4
sudo ovn-nbctl --type=switch acl-add "${switchName}" to-lport 1 "inport == \"${routerPort}\" && icmp4.type == 0 && ip4.src == ${routerIP4}" allow # Ping reply from router IP4
sudo ovn-nbctl --type=switch acl-add "${switchName}" to-lport 1 "outport == \"${routerPort}\" && icmp6.type == 128 && ip6.dst == ${routerIP6}" allow # Ping to router IP6
sudo ovn-nbctl --type=switch acl-add "${switchName}" to-lport 1 "inport == \"${routerPort}\" && icmp6.type == 129 && ip6.src == ${routerIP6}" allow # Ping reply from router IP6
sudo ovn-nbctl --type=switch acl-add "${switchName}" to-lport 1 "outport == \"${routerPort}\" && ip4.dst == ${dnsIP4} && udp.dst == 53" allow # DNS IPv4 UDP
sudo ovn-nbctl --type=switch acl-add "${switchName}" to-lport 1 "outport == \"${routerPort}\" && ip4.dst == ${dnsIP4} && tcp.dst == 53" allow # DNS IPv4 TCP
sudo ovn-nbctl --type=switch acl-add "${switchName}" to-lport 1 "outport == \"${routerPort}\" && ip6.dst == ${dnsIP6} && udp.dst == 53" allow # DNS IPv6 UDP
sudo ovn-nbctl --type=switch acl-add "${switchName}" to-lport 1 "outport == \"${routerPort}\" && ip6.dst == ${dnsIP6} && tcp.dst == 53" allow # DNS IPv6 TCP

# Port groups.
sudo ovn-nbctl pg-set-ports testpg lxd-net9-instance-37b963ca-378c-4eca-bbf0-e4cdfcbd62e0-eth0 lxd-net9-instance-9211022a-4df4-4122-9588-525401779289-eth0
sudo ovn-nbctl --type=switch acl-add "${switchName}" to-lport 2 "inport == @testpg && outport == @testpg && icmp" allow-related # Ping between ports in group

# Network assigned rules.
sudo ovn-nbctl --type=switch acl-add "${switchName}" to-lport 2 "outport == \"${routerPort}\" && tcp.dst == {80,443}" allow # HTTP{S} outbound
sudo ovn-nbctl --type=switch acl-add "${switchName}" to-lport 2 "inport == \"${routerPort}\" && icmp && ip4.dst == 10.0.0.1" allow # Ping to external route NIC IP
