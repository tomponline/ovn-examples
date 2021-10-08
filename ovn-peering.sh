#!/bin/bash
ovn-nbctl --if-exists lrp-del lrp0
ovn-nbctl --if-exists lrp-del lrp1

# Setup peer relationship between two OVN routers.
# This uses two router-ports, one on each router, with the respective port as its peer.
# The router ports re-use the same MAC address as the internal LAN port as well as the
# same IP (with a single /32 or /128 subnet) as the OVN router's LAN port.
# This avoids the need for allocating additional IPs for the peering link.
ovn-nbctl lrp-add lxd-net8-lr lrp0 00:16:3e:d6:73:26 10.110.120.1/32 fd42:7832:3b4e:cffb::1/128 peer=lrp1
ovn-nbctl lrp-add lxd-net10-lr lrp1 00:16:3e:3f:e4:9f 10.105.164.1/32 fd42:5389:62b9:be7c::1/128 peer=lrp0

ovn-nbctl --if-exists lr-route-del lxd-net8-lr 10.105.164.1/24
ovn-nbctl --if-exists lr-route-del lxd-net10-lr 10.110.120.0/24
ovn-nbctl --if-exists lr-route-del lxd-net10-lr 10.0.0.1/32
ovn-nbctl --if-exists lr-route-del lxd-net8-lr fd42:5389:62b9:be7c::/64
ovn-nbctl --if-exists lr-route-del lxd-net10-lr fd42:7832:3b4e:cffb::/64

# Add static routes for the subnet(s) reachable on the repspective networks.
# This includes any NICs that have ipv{n}.routes.external entries (to avoid asymmetric routing).
# The routes specify the target router port to use to exit the logical router as the nexthop IP
# isn't directly reachable using the logical router routing table (due to the use of /32 or /128 addressing above).
ovn-nbctl lr-route-add lxd-net8-lr 10.105.164.0/24 10.105.164.1 lrp0
ovn-nbctl lr-route-add lxd-net10-lr 10.110.120.0/24 10.110.120.1 lrp1
ovn-nbctl lr-route-add lxd-net10-lr 10.0.0.1/32 10.110.120.1 lrp1
ovn-nbctl lr-route-add lxd-net8-lr fd42:5389:62b9:be7c::/64 fd42:5389:62b9:be7c::1 lrp0
ovn-nbctl lr-route-add lxd-net10-lr fd42:7832:3b4e:cffb::/64 fd42:7832:3b4e:cffb::1 lrp1
