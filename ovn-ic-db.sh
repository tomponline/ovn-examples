set -ex

apt install ovn-ic-db -y

cat <<EOF > /etc/default/ovn-ic
OVN_CTL_OPTS="
--db-ic-nb-create-insecure-remote=yes
--db-ic-sb-create-insecure-remote=yes
--db-ic-nb-addr=[::]
--db-ic-sb-addr=[::]
"
EOF

systemctl enable --now ovn-ic-db
systemctl restart ovn-ic-db

ovn-ic-nbctl --may-exist ts-add ts1

ovn-ic-nbctl show
ovn-ic-sbctl show
