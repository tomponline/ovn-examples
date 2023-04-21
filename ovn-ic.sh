set -ex

apt install ovn-ic -y

ovn-nbctl set NB_Global . \
	name=${HOSTNAME} \
        options:ic-route-adv=false \
        options:ic-route-adv-default=false \
        options:ic-route-learn=false \
        options:ic-route-learn-default=false

sudo ovs-vsctl set open_vswitch . \
	external_ids:ovn-encap-type=geneve \
	external_ids:ovn-remote="unix:/var/run/ovn/ovnsb_db.sock" \
	external_ids:ovn-encap-ip=$(ip r get 8.8.8.8 | grep -v cache | awk '{print $7}') \
	external_ids:ovn-is-interconn=true

cat <<EOF > /etc/ovn/ovn-ic-db-params.conf
--ic-nb-db=tcp:10.21.203.10:6645
--ic-sb-db=tcp:10.21.203.10:6646
EOF

systemctl enable ovn-ic --now
systemctl restart ovn-ic

# Check the transit switch has been created in the NB database.
ovn-nbctl find logical_switch other_config:interconn-ts=ts1 | grep ts1

lxdOVNNetworkID="$(lxd sql global 'select id from networks where name="ovn1"' | tr -cd '[[:digit:]]')"
routerName="lxd-net${lxdOVNNetworkID}-lr"
chassisName="$(ovn-sbctl --column=name --format=csv --no-headings --data=bare find chassis hostname="${HOSTNAME}")"
tsSwitchPortName="lsp-ts-${HOSTNAME}"
tsRouterPortName="lrp-ts-${HOSTNAME}"
hostnameDigit="$(echo "${HOSTNAME}" | tr -cd '[[:digit:]]')"
tsRouterPortMAC="aa:aa:aa:aa:aa:0${hostnameDigit}"

ovn-nbctl \
	-- --if-exists lsp-del "${tsSwitchPortName}" \
	-- --if-exists lrp-del "${tsRouterPortName}" \
	-- lrp-add "${routerName}" "${tsRouterPortName}" "${tsRouterPortMAC}" 10.0.0."${hostnameDigit}"/24 fda4:47dc:2fd4::"${hostnameDigit}"/64 \
	-- lrp-set-gateway-chassis "${tsRouterPortName}" "${chassisName}" 99 \
	-- lsp-add ts1 "${tsSwitchPortName}" \
	-- lsp-set-addresses "${tsSwitchPortName}" router \
	-- lsp-set-type "${tsSwitchPortName}" router \
	-- lsp-set-options "${tsSwitchPortName}" router-port="${tsRouterPortName}"

