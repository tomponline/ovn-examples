set -xe

# Clear all rules for a switch.
sudo ovn-nbctl clear logical_switch lxd-net9-ls-int acls

# Baseline rules
sudo ovn-nbctl --type=switch --log acl-add lxd-net9-ls-int to-lport 0 1 drop # Default log and drop
sudo ovn-nbctl --type=switch acl-add lxd-net9-ls-int to-lport 1 'arp' allow # ARP
sudo ovn-nbctl --type=switch acl-add lxd-net9-ls-int to-lport 1 'nd' allow # Neighbour discovery
sudo ovn-nbctl --type=switch acl-add lxd-net9-ls-int to-lport 1 'inport == "lxd-net9-ls-int-lsp-router" && nd_ra' allow # Router adverts
sudo ovn-nbctl --type=switch acl-add lxd-net9-ls-int to-lport 1 'nd_rs' allow # Router solicitation
sudo ovn-nbctl --type=switch acl-add lxd-net9-ls-int to-lport 1 'icmp6.type == 143' allow # Multicast listener report
sudo ovn-nbctl --type=switch acl-add lxd-net9-ls-int to-lport 1 'udp.dst == 67' allow # DHCPv4
sudo ovn-nbctl --type=switch acl-add lxd-net9-ls-int to-lport 1 'udp.dst == 547' allow # DHCPv6
sudo ovn-nbctl --type=switch acl-add lxd-net9-ls-int to-lport 2 'outport == "lxd-net9-ls-int-lsp-router" && icmp4.type == 8 && ip4.dst == 10.164.241.1' allow # Ping to router
sudo ovn-nbctl --type=switch acl-add lxd-net9-ls-int to-lport 1 'inport == "lxd-net9-ls-int-lsp-router" && icmp4.type == 0 && ip4.src == 10.164.241.1' allow # Ping reply from router
sudo ovn-nbctl --type=switch acl-add lxd-net9-ls-int to-lport 2 'inport == @testpg && outport == @testpg && icmp' allow-related # Ping between ports in group

# DNS rules

