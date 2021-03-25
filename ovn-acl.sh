set -xe
# Vars.
intSwitchName="lxd-net9-ls-int"
routerIntPort="lxd-net9-ls-int-lsp-router"
routerIntIP4="10.164.241.1"
routerIntIP6="fd42:2f0b:37e5:4157::1"
dnsIP4="10.102.242.1"
dnsIP6="fd42:442d:225e:59df::1"

# Clear all rules directly applied to internal and external switches.
sudo ovn-nbctl clear logical_switch "${intSwitchName}" acls

# Baseline allow internal switch rules added when switch is created.
sudo ovn-nbctl --type=switch acl-add "${intSwitchName}" to-lport 1 "arp" allow # ARP
sudo ovn-nbctl --type=switch acl-add "${intSwitchName}" to-lport 1 "nd" allow # Neighbour discovery
sudo ovn-nbctl --type=switch acl-add "${intSwitchName}" to-lport 1 "icmp6.type == 143" allow # Multicast listener report
sudo ovn-nbctl --type=switch acl-add "${intSwitchName}" to-lport 1 "inport == \"${routerIntPort}\" && nd_ra" allow # Router adverts from router
sudo ovn-nbctl --type=switch acl-add "${intSwitchName}" to-lport 1 "outport == \"${routerIntPort}\" && nd_rs" allow # Router solicitation to router
sudo ovn-nbctl --type=switch acl-add "${intSwitchName}" to-lport 1 "outport == \"${routerIntPort}\" && udp.dst == 67" allow # DHCPv4 to router
sudo ovn-nbctl --type=switch acl-add "${intSwitchName}" to-lport 1 "outport == \"${routerIntPort}\" && udp.dst == 547" allow # DHCPv6 to router
sudo ovn-nbctl --type=switch acl-add "${intSwitchName}" to-lport 1 "outport == \"${routerIntPort}\" && icmp4.type == 8 && ip4.dst == ${routerIntIP4}" allow # Ping to router IP4
sudo ovn-nbctl --type=switch acl-add "${intSwitchName}" to-lport 1 "inport == \"${routerIntPort}\" && icmp4.type == 0 && ip4.src == ${routerIntIP4}" allow # Ping reply from router IP4
sudo ovn-nbctl --type=switch acl-add "${intSwitchName}" to-lport 1 "outport == \"${routerIntPort}\" && icmp6.type == 128 && ip6.dst == ${routerIntIP6}" allow # Ping to router IP6
sudo ovn-nbctl --type=switch acl-add "${intSwitchName}" to-lport 1 "inport == \"${routerIntPort}\" && icmp6.type == 129 && ip6.src == ${routerIntIP6}" allow # Ping reply from router IP6
sudo ovn-nbctl --type=switch acl-add "${intSwitchName}" to-lport 1 "outport == \"${routerIntPort}\" && (ip4.dst == ${dnsIP4} || ip6.dst == ${dnsIP6}) && (udp.dst == 53 || tcp.dst == 53)" allow # DNS

# Setup NSG port group to allow ping internally.
sudo ovn-nbctl --if-exists destroy port_group ping_internal
sudo ovn-nbctl pg-add ping_internal
sudo ovn-nbctl --type=port-group acl-add ping_internal to-lport 2 '(ip4.src == $ping_internal_ip4 || ip6.src == $ping_internal_ip6) && (ip4.dst == $ping_internal_ip4 || ip6.dst == $ping_internal_ip6) && icmp' allow-related
sudo ovn-nbctl --type=port-group --log --name ping_internal acl-add ping_internal to-lport 0 "inport == @ping_internal || outport == @ping_internal" drop # NSG default drop.

# Apply NSG to multiple NICs.
sudo ovn-nbctl pg-set-ports ping_internal \
        lxd-net9-instance-37b963ca-378c-4eca-bbf0-e4cdfcbd62e0-eth0 \
        lxd-net9-instance-e3c60b04-cdf1-44f4-8cee-fc1f157d717c-eth0

# Setup NSG port group to allow ping to 8.8.8.8.
sudo ovn-nbctl --if-exists destroy port_group ping_google
sudo ovn-nbctl pg-add ping_google
sudo ovn-nbctl --type=port-group acl-add ping_google to-lport 2 "inport == @ping_google && icmp && ip4.dst == 8.8.8.8" allow-related
sudo ovn-nbctl --type=port-group --log --name ping_google acl-add ping_google to-lport 0 "inport == @ping_google || outport == @ping_google" drop # NSG default drop.

# Apply NSG to single NIC.
sudo ovn-nbctl pg-set-ports ping_google \
        lxd-net9-instance-37b963ca-378c-4eca-bbf0-e4cdfcbd62e0-eth0

# Setup NSG port group to allow HTTP outbound.
sudo ovn-nbctl --if-exists destroy port_group http_outbound
sudo ovn-nbctl pg-add http_outbound
sudo ovn-nbctl --type=port-group acl-add http_outbound to-lport 2 "inport == @http_outbound && tcp.dst == {80,443}" allow-related # HTTP{S} outbound
sudo ovn-nbctl --type=port-group --log --name http_outbound acl-add http_outbound to-lport 0 "inport == @http_outbound || outport == @http_outbound" drop # NSG default drop.

# Apply NSG to multiple NICs.
sudo ovn-nbctl pg-set-ports http_outbound \
        lxd-net9-instance-37b963ca-378c-4eca-bbf0-e4cdfcbd62e0-eth0 \
        lxd-net9-instance-e3c60b04-cdf1-44f4-8cee-fc1f157d717c-eth0
