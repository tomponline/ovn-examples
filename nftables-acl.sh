table bridge lxd {
	set pg.net.lxdbr0 {
		type ifname
		elements = { "vethc31239d2" }
	}

	chain acl.extout.lxdbr0 {
		type filter hook input priority 0; policy accept;
		iifname != @pg.net.lxdbr0 accept
		ct state established,related accept
		ether type arp accept
		icmpv6 type { destination-unreachable, packet-too-big, time-exceeded, parameter-problem, nd-router-solicit, nd-neighbor-solicit, nd-neighbor-advert, mld2-listener-report } accept
		icmp type { destination-unreachable, time-exceeded, parameter-problem } accept
		ether type ip udp dport 67 accept
		ether type ip6 udp dport 547 accept
		icmp type echo-request ip daddr 10.105.189.1 accept
		icmpv6 type echo-request ip6 daddr fd42:4aa1:ce49:3818::1 accept
		ip daddr 10.105.189.1 udp dport 53 accept
		ip daddr 10.105.189.1 tcp dport 53 accept
		ip6 daddr fd42:4aa1:ce49:3818::1 udp dport 53 accept
		ip6 daddr fd42:4aa1:ce49:3818::1 tcp dport 53 accept
		jump acl.defaultextout.lxdbr0
	}

	chain acl.extin.lxdbr0 {
		type filter hook output priority 0; policy accept;
		oifname != @pg.net.lxdbr0 accept
		ct state established,related accept
		ether type arp accept
		icmpv6 type { destination-unreachable, packet-too-big, time-exceeded, parameter-problem, nd-router-advert, nd-neighbor-solicit, nd-neighbor-advert, mld2-listener-report } accept
		icmp type { destination-unreachable, time-exceeded, parameter-problem } accept
		ether type ip udp dport 68 accept
		ether type ip6 udp dport 546 accept
		jump acl.default.lxdbr0
	}

	chain acl.int.lxdbr0 {
		type filter hook forward priority 0; policy accept;
		iifname != @pg.net.lxdbr0 accept
		ct state established,related accept
		ether type arp accept
		icmpv6 type { destination-unreachable, packet-too-big, time-exceeded, parameter-problem, nd-neighbor-solicit, nd-neighbor-advert, mld2-listener-report } accept
		icmp type { destination-unreachable, time-exceeded, parameter-problem } accept
		jump acl.default.lxdbr0
	}

	chain acl.default.lxdbr0 {
		iifname "vethc31239d2" log prefix "lxd-700efa6a-d37d-4820-8f9a-27aba9dca3f6-eth0-egress: " drop comment "lxd-instance-700efa6a-d37d-4820-8f9a-27aba9dca3f6-eth0"
		oifname "vethc31239d2" log prefix "lxd-700efa6a-d37d-4820-8f9a-27aba9dca3f6-eth0-ingress: " drop comment "lxd-instance-700efa6a-d37d-4820-8f9a-27aba9dca3f6-eth0"
	}

	chain acl.defaultextout.lxdbr0 {
		iifname "vethc31239d2" log prefix "lxd-700efa6a-d37d-4820-8f9a-27aba9dca3f6-eth0-egress: " reject comment "lxd-instance-700efa6a-d37d-4820-8f9a-27aba9dca3f6-eth0"
	}
}
