#!/bin/bash

modprobe br_netfilter
echo 1 > /proc/sys/net/bridge/bridge-nf-call-iptables

iptables -F
iptables -A FORWARD -m physdev --physdev-in lxdbr0 -j LOG
