set -e
ovn-nbctl --if-exist lb-del test
ovn-nbctl --may-exist lb-add test 10.128.213.11:80 10.176.202.3:80 tcp
ovn-nbctl --may-exist lb-add test 10.128.213.11 10.176.202.2
ovn-nbctl --may-exist lb-add test 10.128.213.11:81 10.176.202.3:80 tcp

#ovn-nbctl --may-exist lb-add test '[fd42:b545:2e58:ec06::12]' '[fd42:3242:1613:9c39:216:3eff:fe80:6179]'
#ovn-nbctl --may-exist lb-add test '[fd42:b545:2e58:ec06::11]:80' '[fd42:3242:1613:9c39:216:3eff:fe80:6179]:80' tcp
#ovn-nbctl --may-exist lb-add test '[fd42:b545:2e58:ec06::11]:81' '[fd42:3242:1613:9c39:216:3eff:fe80:6179]:80' tcp

ovn-nbctl --may-exist lr-lb-add lxd-net2-lr test
ovn-nbctl --may-exist ls-lb-add lxd-net2-ls-int test
ovn-nbctl lr-lb-list lxd-net2-lr

#ovn-nbctl lr-nat-del lxd-net2-lr dnat_and_snat
#ovn-nbctl lr-nat-del lxd-net2-lr dnat

#ovn-nbctl --may-exist --stateless lr-nat-add lxd-net2-lr dnat_and_snat 10.128.213.13 10.176.202.2
#ovn-nbctl --may-exist lr-nat-add lxd-net2-lr dnat 10.128.213.13 10.176.202.2
