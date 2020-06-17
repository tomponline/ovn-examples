#!/bin/bash

# Usage: Create two networks with same subnet and MACs.
# ./dhcp_multi_tenant.sh test_net1
# ./dhcp_multi_tenant.sh test_net2
# sudo ip netns exec test_net1_p1 ping 10.0.0.12

tenant_net_name="${1}"
tenant_subnet_ipv4="10.0.0.0/24"
tenant_subnet_ipv6="fd47:8ac3:9083:35f6::/64"
tenant_reserved_ips="10.0.0.1..10.0.0.10"
tenant_router_ipv4="10.0.0.1"
tenant_router_ipv4_subnet="10.0.0.1/24"
tenant_router_ipv6_subnet="fd47:8ac3:9083:35f6::1/64"
tenant_router_mac="c0:ff:ee:00:00:00"

if [ "${tenant_net_name}" == "" ]; then
	echo "Please specify net name"
	exit 1
fi

set -e
set -o xtrace

# Configure OVN database to accept connections from OVS chassis.
sudo ovn-sbctl set-connection ptcp:6642:127.0.0.1

echo "OVN database listener setup:"
sudo ovn-sbctl get-connection

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
	options:server_mac="${tenant_router_mac}")

# Setup router.
routerName="${tenant_net_name}_router"
routerPort="${tenant_net_name}_gw"
sudo ovn-nbctl --if-exists lr-del "${routerName}"
sudo ovn-nbctl lr-add "${routerName}"
sudo ovn-nbctl lrp-add "${routerName}" "${routerPort}" "${tenant_router_mac}" "${tenant_router_ipv4_subnet}" "${tenant_router_ipv6_subnet}" -- \
	set logical_router_port "${routerPort}" \
		ipv6_ra_configs:send_periodic=true \
		ipv6_ra_configs:address_mode=slaac \
		ipv6_ra_configs:min_interval=10 \
		ipv6_ra_configs:max_interval=15

# Setup router port on switch.
routerSwitchPort="${routerPort}_sw" # This has to be different than $routerPort.
sudo ovn-nbctl --if-exists lsp-del "${routerPort}"
sudo ovn-nbctl lsp-add "${tenant_net_name}" "${routerSwitchPort}" -- \
	lsp-set-type "${routerSwitchPort}" router -- \
	lsp-set-addresses "${routerSwitchPort}" router -- \
	lsp-set-options "${routerSwitchPort}" router-port="${routerPort}"

# Print summary of logical network and DHCP options.
echo -e "\nCreated logical network ${tenant_net_name}:"
sudo ovn-nbctl show "${tenant_net_name}"
sudo ovn-nbctl list dhcp_options "${dhcp_opts_uuid}"

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
	portName="${tenant_net_name}_${1}"
	mac="${2}"
	sudo ovn-nbctl --if-exists lsp-del "${portName}"
	sudo ovn-nbctl lsp-add "${tenant_net_name}" "${portName}" -- \
		lsp-set-addresses "${portName}" "${mac} dynamic" -- \
		lsp-set-dhcpv4-options "${portName}" "${dhcp_opts_uuid}"

	# Print summary of logical switch and ports.
	echo -e "\nLogical switch port added:"
	sudo ovn-nbctl show "${tenant_net_name}"

	# Add port to local OVS integration bridge.
	# Use "internal" type port so it can be moved into network namespace and used like a tap device.
	sudo ovs-vsctl --if-exists del-port "${portName}"
	sudo ovs-vsctl add-port br-int "${portName}" -- \
		set interface "${portName}" \
		type=internal \
		mac='["'"${mac}"'"]' \
		external_ids:iface-id="${portName}"

	echo -e "\nOVS switch port added to local integration bridge:"
	sudo ovs-vsctl show 
}

movePort () {
	# Move OVS switch port into network namespace to test.
	portName="${tenant_net_name}_${1}"
	netnsName="${portName}"
	set +e
	sudo ip netns del "${netnsName}"
	set -e
	sudo ip netns add "${netnsName}"
	sudo ip -n "${netnsName}" addr add 127.0.0.1/8 dev lo
	sudo ip -n "${netnsName}" link set lo up
	sudo ip link set netns "${netnsName}" "${portName}"
	sudo ip netns exec "${netnsName}" dhclient -v -i "${portName}" --no-pid
	sudo ip netns exec "${netnsName}" ip a show "${portName}"
	echo -e "Moved ${portName} to ${netnsName}"
}

addPort "p1" "c0:ff:ee:00:00:01"
movePort "p1"

addPort "p2" "c0:ff:ee:00:00:02"
movePort "p2"
