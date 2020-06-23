#!/bin/bash

# Usage: Create two networks with same subnet and MACs.
# ./dhcp_multi_tenant.sh {ID}
# ./dhcp_multi_tenant.sh 1
# ./dhcp_multi_tenant.sh 2
# sudo ip netns exec test_net{ID}_p1 ping 10.0.0.12

tenant_net_id="${1}"
tenant_net_name="test-net${tenant_net_id}"
tenant_subnet_ipv4="10.0.0.0/24"
tenant_subnet_ipv6="fd47:8ac3:9083:35f6::/64"
tenant_reserved_ips="10.0.0.1..10.0.0.10"
tenant_router_ipv4="10.0.0.1"
tenant_router_ipv4_subnet="10.0.0.1/24"
tenant_router_ipv6_subnet="fd47:8ac3:9083:35f6::1/64"
tenant_router_mac="c0:ff:ee:00:00:00"
tenant_router_ext_ipv4="169.254.0.${tenant_net_id}"
tenant_router_ext_ipv6="fd47:8ac3:9083:35f7::1:${tenant_net_id}"
tenant_router_ext_mac="02:0a:7f:00:01:0${tenant_net_id}"
tenant_dns_v4="8.8.8.8"
tenant_dns_v6="2001:4860:4860::8888"
ext_net_name="lxd-ext"
ext_net_ipv4="169.254.0.254"
ext_net_ipv6="fd47:8ac3:9083:35f7::1"
ext_net_mac="02:0a:7f:00:02:30"

if [ "${tenant_net_id}" == "" ]; then
	echo "Please specify net ID"
	exit 1
fi

set -e
set -o xtrace

# Configure OVN database to accept connections from OVS chassis.
sudo ovn-sbctl set-connection ptcp:6642:127.0.0.1

echo "OVN database listener setup:"
sudo ovn-sbctl get-connection

# Create shared external switch for external network access.
sudo ovn-nbctl --may-exist ls-add "${ext_net_name}"

# Create logical switch port for external port on external switch.
sudo ovn-nbctl --if-exists lsp-del "${ext_net_name}"
sudo ovn-nbctl lsp-add "${ext_net_name}" "${ext_net_name}" -- \
	lsp-set-addresses "${ext_net_name}" "${ext_net_mac} ${ext_net_ipv4} ${ext_net_ipv6}"

sudo ovs-vsctl --may-exist add-port br-int "${ext_net_name}" -- \
	set interface "${ext_net_name}" \
	type=internal \
	mac='["'"${ext_net_mac=}"'"]' \
	external_ids:iface-id="${ext_net_name}"

# Bring up interface on LXD host.
sudo ip a flush dev "${ext_net_name}"
sudo ip a add "${ext_net_ipv4}"/24 dev "${ext_net_name}"
sudo ip a add "${ext_net_ipv6}"/64 dev "${ext_net_name}"
sudo ip link set "${ext_net_name}" up

# Create logical switch with subnet and reserved IPs.
sudo ovn-nbctl --if-exists ls-del "${tenant_net_name}"

sudo ovn-nbctl ls-add "${tenant_net_name}" -- \
	set logical_switch "${tenant_net_name}" \
		other_config:subnet="${tenant_subnet_ipv4}" \
		other_config:ipv6_prefix="${tenant_subnet_ipv6}" \
		other_config:exclude_ips="${tenant_reserved_ips}"

# Create DHCP settings for network.
sudo ovn-nbctl destroy dhcp_options \
	$(sudo ovn-nbctl --format=csv --no-headings --data=bare --columns=_uuid find dhcp_options \
		external_ids:lxd_network="${tenant_net_name}")

dhcp_opts_uuid=$(sudo ovn-nbctl create dhcp_option \
	external_ids:lxd_network="${tenant_net_name}" \
	cidr="${tenant_subnet_ipv4}" \
	options:server_id="${tenant_router_ipv4}" \
	options:lease_time="3600" \
	options:router="${tenant_router_ipv4}" \
	options:server_mac="${tenant_router_mac}" \
	options:dns_server="${tenant_dns_v4}" \
	)

#	options:domain_name=linuxcontainers.org \

dhcpv6_opts_uuid=$(sudo ovn-nbctl create dhcp_option \
	external_ids:lxd_network="${tenant_net_name}" \
	cidr='"'"${tenant_subnet_ipv6}"'"' \
	options:server_id="${tenant_router_mac}" \
	options:dns_server="${tenant_dns_v6}" \
	)

# 	options:domain_search=linuxcontainers.org \

# Setup router.
routerName="${tenant_net_name}_router"
routerPort="${tenant_net_name}_gw"
sudo ovn-nbctl --if-exists lr-del "${routerName}"
sudo ovn-nbctl lr-add "${routerName}" -- \
	set logical_router "${routerName}" options:chassis="$(hostname)"

sudo ovn-nbctl lrp-add "${routerName}" "${routerPort}" "${tenant_router_mac}" "${tenant_router_ipv4_subnet}" "${tenant_router_ipv6_subnet}" -- \
	set logical_router_port "${routerPort}" \
		ipv6_ra_configs:send_periodic=true \
		ipv6_ra_configs:address_mode=dhcpv6_stateful \
		ipv6_ra_configs:min_interval=10 \
		ipv6_ra_configs:max_interval=15 \
		ipv6_ra_configs:rdnss="${tenant_dns_v6}"

# Add default route on router to shared external host interface.
sudo ovn-nbctl lr-route-add "${routerName}" 0.0.0.0/0 "${ext_net_ipv4}" -- \
	lr-route-add "${routerName}" ::/0 "${ext_net_ipv6}"

# Setup router port on switch.
routerSwitchPort="${routerPort}_sw" # This has to be different than $routerPort.
sudo ovn-nbctl --if-exists lsp-del "${routerSwitchPort}"
sudo ovn-nbctl lsp-add "${tenant_net_name}" "${routerSwitchPort}" -- \
	lsp-set-type "${routerSwitchPort}" router -- \
	lsp-set-addresses "${routerSwitchPort}" router -- \
	lsp-set-options "${routerSwitchPort}" router-port="${routerPort}"

# Setup SNAT on router.
sudo ovn-nbctl destroy nat \
        $(sudo ovn-nbctl --format=csv --no-headings --data=bare --columns=_uuid find nat \
                external_ids:lxd_network="${tenant_net_name}")

# IPv4 SNAT.
sudo ovn-nbctl -- --id=@nat create nat type="snat" \
	external_ids:lxd_network="${tenant_net_name}" \
	logical_ip="${tenant_subnet_ipv4}" \
	external_ip="${tenant_router_ext_ipv4}" -- \
	add logical_router "${routerName}" nat @nat

# IPv6 SNAT.
# TODO as current OVN doesn't support SNAT.

# Add local DNS entries for router to internal switch.
sudo ovn-nbctl destroy dns \
        $(sudo ovn-nbctl --format=csv --no-headings --data=bare --columns=_uuid find dns \
                external_ids:lxd_network="${tenant_net_name}")

sudo ovn-nbctl -- --id=@dns create dns records="gateway.localdomain=${tenant_router_ipv4}" \
	external_ids:lxd_network="${tenant_net_name}" -- \
	add logical_switch "${tenant_net_name}" dns_records @dns

# Create router port for external access.
externalRouterPort="${tenant_net_name}_ext_gw"
sudo ovn-nbctl --if-exists lrp-del "${externalRouterPort}"
sudo ovn-nbctl lrp-add "${routerName}" "${externalRouterPort}" "${tenant_router_ext_mac}" "${tenant_router_ext_ipv4}"/24 "${tenant_router_ext_ipv6}"/64

# Create logical switch port for router on external switch.
externalRouterSwitchPort="${routerPort}_ext_sw" # This has to be different than $externalRouterPort.
sudo ovn-nbctl --if-exists lsp-del "${externalRouterSwitchPort}"
sudo ovn-nbctl lsp-add "${ext_net_name}" "${externalRouterSwitchPort}" -- \
        lsp-set-type "${externalRouterSwitchPort}" router -- \
        lsp-set-addresses "${externalRouterSwitchPort}" router -- \
        lsp-set-options "${externalRouterSwitchPort}" router-port="${externalRouterPort}"

# Print summary of logical network and DHCP options.
echo -e "\nCreated logical network ${tenant_net_name}:"
sudo ovn-nbctl show "${tenant_net_name}"
sudo ovn-nbctl list dhcp_options "${dhcp_opts_uuid}"
sudo ovn-nbctl list dhcp_options "${dhcpv6_opts_uuid}"


# Connect local machine OVS to local OVN database.
# The "." record seems to be a way to specify the first record in this table,
# although can't find any docs on this, only numerous examples using this style.
sudo ovs-vsctl set open_vswitch . \
	external_ids:ovn-remote="tcp:127.0.0.1:6642" \
	external_ids:ovn-remote-probe-interval=10000 \
	external_ids:ovn-encap-ip=127.0.0.1 \
	external_ids:ovn-encap-type="geneve" \
	external_ids:system-id="$(hostname)"

echo -e "\nConnected local OVS chassis to OVN database:"
sudo ovn-sbctl show
sudo ovs-vsctl --columns external_ids list open_vswitch .

addPort () {
	# Add port to logical switch in OVN and configure port with DHCP options.
	# Port names are globally unique and used to link OVN to OVS, so include network name in port name.
	portName="${tenant_net_name}-${1}"
	mac="${2}"
	sudo ovn-nbctl --if-exists lsp-del "${portName}"
	sudo ovn-nbctl lsp-add "${tenant_net_name}" "${portName}" -- \
		lsp-set-addresses "${portName}" "${mac} dynamic" -- \
		lsp-set-dhcpv4-options "${portName}" "${dhcp_opts_uuid}" -- \
		lsp-set-dhcpv6-options "${portName}" "${dhcpv6_opts_uuid}"

	# Print summary of logical switch and ports.
	echo -e "\nLogical switch port added:"
	sudo ovn-nbctl show "${tenant_net_name}"

	# Create veth pair.
	set +e
	ip link del "${portName}"
	set -e
	sudo ip link add "${portName}" type veth peer name "${portName}-p"
	sudo ip link set "${portName}-p" address "${mac}"
	sudo ip link set "${portName}" up

	# Add veth port to integration bridge.
	sudo ovs-vsctl --if-exists del-port "${portName}"
	sudo ovs-vsctl add-port br-int "${portName}" -- \
		set interface "${portName}" external_ids:iface-id="${portName}"

	echo -e "\nOVS switch port added to local integration bridge:"
	sudo ovs-vsctl show
}

movePort () {
	# Move OVS switch port into network namespace to test.
	portName="${tenant_net_name}-${1}-p"
	#netnsName="${portName}"
	set +e
	lxc delete -f "${portName}"
	set -e
	lxc init images:ubuntu/focal "${portName}"
	lxc config device add "${portName}" eth0 nic nictype=physical name=eth0 parent="${portName}"
	lxc start "${portName}"
	#sudo ip netns del "${netnsName}"
	#sudo ip netns add "${netnsName}"
	#sudo ip -n "${netnsName}" addr add 127.0.0.1/8 dev lo
	#sudo ip -n "${netnsName}" link set lo up
	#sudo ip link set netns "${netnsName}" "${portName}"
	#sudo ip netns exec "${netnsName}" udhcpc -q -n -i "${portName}"
	#sudo ip netns exec "${netnsName}" ip a show "${portName}"
	#echo -e "Moved ${portName} to ${netnsName}"
}

addPort "p1" "c0:ff:ee:00:00:01"
movePort "p1"

#addPort "p2" "c0:ff:ee:00:00:02"
#movePort "p2"

