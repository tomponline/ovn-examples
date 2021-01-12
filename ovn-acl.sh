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

# Baseline allow switch rules added when switch is created.
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

# Setup NSG port group to allow ping internally.
sudo ovn-nbctl --if-exists destroy port_group ping_internal
sudo ovn-nbctl pg-add ping_internal
sudo ovn-nbctl --type=port-group acl-add ping_internal to-lport 2 "inport == @ping_internal && outport == @ping_internal && icmp" allow-related
sudo ovn-nbctl --type=port-group --log --name ping_internal acl-add ping_internal to-lport 0 "inport == @ping_internal || outport == @ping_internal" drop # NSG default drop.

# Apply NSG to multiple NICs.
sudo ovn-nbctl pg-set-ports ping_internal \
        lxd-net9-instance-37b963ca-378c-4eca-bbf0-e4cdfcbd62e0-eth0 \
        lxd-net9-instance-9211022a-4df4-4122-9588-525401779289-eth0

# Setup NSG port group to allow ping to 8.8.8.8.
sudo ovn-nbctl --if-exists destroy port_group ping_google
sudo ovn-nbctl pg-add ping_google
sudo ovn-nbctl --type=port-group acl-add ping_google to-lport 2 "inport == @ping_google && icmp && ip4.dst == 8.8.8.8" allow-related
sudo ovn-nbctl --type=port-group --log --name ping_google acl-add ping_google to-lport 0 "inport == @ping_google || outport == @ping_google" drop # NSG default drop.

# Apply NSG to single NIC.
sudo ovn-nbctl pg-set-ports ping_google \
        lxd-net9-instance-37b963ca-378c-4eca-bbf0-e4cdfcbd62e0-eth0

# Setup NSG port group to allow HTTP externally.
sudo ovn-nbctl --if-exists destroy port_group http_outbound
sudo ovn-nbctl pg-add http_outbound
sudo ovn-nbctl --type=port-group acl-add http_outbound to-lport 2 "outport == @http_outbound && tcp.dst == {80,443}" allow # HTTP{S} outbound
sudo ovn-nbctl --type=port-group --log --name http_outbound acl-add http_outbound to-lport 0 "inport == @http_outbound || outport == @http_outbound" drop # NSG default drop.

# Apply NSG to network by adding router port to group.
sudo ovn-nbctl pg-set-ports http_outbound \
        "${routerPort}"

# Setup NSG port group to allow ping to our externally routed subnet.
sudo ovn-nbctl --if-exists destroy port_group ping_external_subnet
sudo ovn-nbctl pg-add ping_external_subnet
sudo ovn-nbctl --type=port-group acl-add ping_external_subnet to-lport 2 "icmp && ip4.dst == 10.0.0.0/24" allow-related
sudo ovn-nbctl --type=port-group --log --name ping_external_subnet acl-add ping_external_subnet to-lport 0 "inport == @ping_internal || outport == @ping_internal" drop # NSG default drop.

# Apply NSG to network by adding router port to group.
sudo ovn-nbctl pg-set-ports ping_external_subnet \
        "${routerPort}"
