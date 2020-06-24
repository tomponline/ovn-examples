package main

import (
	"bytes"
	cryptoRand "crypto/rand"
	"encoding/hex"
	"fmt"
	"log"
	"math/big"
	"math/rand"
	"net"
	"os"
	"strings"

	"github.com/mdlayher/netx/eui64"

	"github.com/lxc/lxd/shared"
)

type network struct {
	name string
	gwV4 string
	gwV6 string
}

// Define IPs to use on host-side of project namespace veth-pair.
const extHostIPv4 = "169.254.0.1"
const extHostIPv6 = "fe80::1"

// Define IPs to use in the project namespace on the OVN-veth side.
const extNSOVNIPv4 = "169.254.1.1"
const extNSOVNIPv6 = "fe80::1:1"

// Define IPs to use on the external port of the OVN router.
const extOVNIPv4 = "169.254.1.2"
const extOVNIPv6 = "fe80::1:2"

// Define IPv6 ULA prefix used for generating point-to-point link addresses.
const extIPv6ULAPrefix = "fd42::"

// Define DNS settings.
const dnsV4 = "8.8.8.8"
const dnsV6 = "2001:4860:4860::8888"
const dnsDomainName = "lxd"
const dnsV6SearchDomains = "lxd"

func main() {
	chassisName, err := os.Hostname()
	if err != nil {
		log.Fatal(err)
	}

	err = connectOVStoOVN(chassisName)
	if err != nil {
		log.Fatal(err)
	}

	projects := []string{"project1", "project2"}
	for _, projectName := range projects {
		extHostName, err := createProjectExternalNamespace(projectName)
		if err != nil {
			log.Fatal(err)
		}
		log.Printf("Created external host veth interface %q linked to netns %q", extHostName, projectName)

		extRouterPortName, err := createProjectRouter(chassisName, projectName)
		if err != nil {
			log.Fatal(err)
		}
		log.Printf("Created project router %q and external port %q for chassis %q", projectName, extRouterPortName, chassisName)

		externalSwitchName, err := createProjectExternalSwitch(projectName, extRouterPortName)
		if err != nil {
			log.Fatal(err)
		}
		log.Printf("Created project external switch %q and linked external port %q to it", externalSwitchName, extRouterPortName)

		// Define the networks we want each project to have.
		networks := []network{
			network{
				name: "net1",
				gwV4: "10.0.0.1/24",
				gwV6: "fd47:8ac3:9083:35f6::1/64",
			},
			network{
				name: "net2",
				gwV4: "192.168.3.1/24",
				gwV6: "fd47:8ac3:9083:36f6::1/64",
			},
		}

		for _, network := range networks {
			internalSwitchName, DHCPv4Opt, DHCPv6Opt, err := createProjectInternalSwitch(projectName, network)
			if err != nil {
				log.Fatal(err)
			}
			log.Printf("Created project internal switch %q (DHCP %q) and connected router to it", internalSwitchName, DHCPv4Opt)

			// Create the instances we want to connect to this network.
			instances := []string{"c1", "c2"}
			for _, instance := range instances {
				instanceName := fmt.Sprintf("%s-%s", network.name, instance)
				instPortName, instPortMac, err := addInstancePort(projectName, internalSwitchName, instanceName, DHCPv4Opt, DHCPv6Opt)
				if err != nil {
					log.Fatal(err)
				}
				log.Printf("Created instance port %q (%q)", instPortName, instPortMac)

				err = createInstance(projectName, instanceName, instPortName)
				if err != nil {
					log.Fatal(err)
				}
				log.Printf("Created instance %q using port %q", instanceName, instPortName)
			}
		}
	}
}

// networkRandomDevName returns a random device name with prefix.
// If the random string combined with the prefix exceeds 13 characters then empty string is returned.
// This is to ensure we support buggy dhclient applications: https://bugs.debian.org/cgi-bin/bugreport.cgi?bug=858580
func networkRandomDevName(prefix string) string {
	// Return a new random veth device name
	randBytes := make([]byte, 4)
	rand.Read(randBytes)
	iface := prefix + hex.EncodeToString(randBytes)
	if len(iface) > 13 {
		return ""
	}

	return iface
}

// networkRandomMAC generates a random MAC address.
func networkRandomMAC() (string, error) {
	// Generate a new random MAC address using the usual prefix.
	ret := bytes.Buffer{}
	for _, c := range "00:16:3e:xx:xx:xx" {
		if c == 'x' {
			c, err := cryptoRand.Int(cryptoRand.Reader, big.NewInt(16))
			if err != nil {
				return "", err
			}
			ret.WriteString(fmt.Sprintf("%x", c.Int64()))
		} else {
			ret.WriteString(string(c))
		}
	}

	return ret.String(), nil
}

// networkRandomIPv6P2P generates random MAC and generates EUI64 IPv6 address from extIPv6ULAPrefix.
// Returns IP address and MAC address.
func networkRandomIPv6P2P() (string, string, error) {
	// Generate peer-side IPv6 address.
	macStr, err := networkRandomMAC()
	if err != nil {
		return "", "", err
	}

	mac, err := net.ParseMAC(macStr)
	if err != nil {
		return "", "", err
	}

	// Use ULA prefix for EUI64 IP generation.
	ipv6, err := eui64.ParseMAC(net.ParseIP(extIPv6ULAPrefix), mac)
	if err != nil {
		return "", "", err
	}

	return ipv6.String(), macStr, nil
}

func connectOVStoOVN(chassisName string) error {
	// Connect local machine OVS to local OVN database.
	// The "." record seems to be a way to specify the first record in this table,
	// although can't find any docs on this, only numerous examples using this style.
	_, err := shared.RunCommand("ovs-vsctl", "set", "open_vswitch", ".",
		"external_ids:ovn-remote=tcp:127.0.0.1:6642",
		"external_ids:ovn-remote-probe-interval=10000",
		"external_ids:ovn-encap-ip=127.0.0.1",
		"external_ids:ovn-encap-type=geneve",
		fmt.Sprintf("external_ids:system-id=%s", chassisName),
	)
	if err != nil {
		return err
	}

	return nil
}

// createProjectExternalNamespace creates network namespace for project and a veth pair with one end in the netns.
// Returns the name of the veth interface in the host netns.
func createProjectExternalNamespace(projectName string) (string, error) {
	// Create project network namespace.
	shared.RunCommand("ip", "netns", "del", projectName)
	_, err := shared.RunCommand("ip", "netns", "add", projectName)
	if err != nil {
		return "", err
	}

	_, err = shared.RunCommand("ip", "-n", projectName, "link", "set", "dev", "lo", "up")
	if err != nil {
		return "", err
	}

	// Figure out free IPV4 for LXD host-side interface.
	isUsed := func(routes []string, matchRoute string) bool {
		for _, route := range routes {
			parts := strings.Fields(route)
			if len(parts) < 1 {
				continue
			}

			if parts[0] == matchRoute {
				return true
			}
		}

		return false
	}

	var peerAddrV4 string
	routeStr, err := shared.RunCommand("ip", "-4", "route", "show")
	if err != nil {
		return "", err
	}

	routeStr = strings.TrimSpace(routeStr)
	routes := strings.Split(routeStr, "\n")

	// This places a limit of 253 projects with OVN networks per host.
	// Could be expanded if needed by using more addresses in 169.254.0.0/16.
	for i := 2; i < 255; i++ {
		checkAddr := net.IPv4(169, 254, 0, byte(i))
		if !isUsed(routes, checkAddr.String()) {
			peerAddrV4 = checkAddr.String()
			break
		}
	}

	if peerAddrV4 == "" {
		return "", fmt.Errorf("Unable to find free host-side IPv4 to use")
	}

	// Create veth pair from project network namespace to host network namespace.
	hostName := networkRandomDevName("exth")
	peerName := networkRandomDevName("extp")

	_, err = shared.RunCommand("ip", "link", "add", "dev", hostName, "type", "veth", "peer", "name", peerName)
	if err != nil {
		return "", err
	}

	// No need for auto-generated link-local IPv6 addresses.
	_, err = shared.RunCommand("sysctl",
		fmt.Sprintf("net.ipv6.conf.%s.addr_gen_mode=1", hostName),
		fmt.Sprintf("net.ipv6.conf.%s.autoconf=0", hostName),
		fmt.Sprintf("net.ipv6.conf.%s.accept_ra=0", hostName),
		fmt.Sprintf("net.ipv6.conf.%s.forwarding=1", hostName),
		fmt.Sprintf("net.ipv4.conf.%s.forwarding=1", hostName),
	)
	if err != nil {
		return "", err
	}

	_, err = shared.RunCommand("ip", "address", "add", fmt.Sprintf("%s/32", extHostIPv4), "dev", hostName)
	if err != nil {
		return "", err
	}

	_, err = shared.RunCommand("ip", "address", "add", fmt.Sprintf("%s/128", extHostIPv6), "dev", hostName)
	if err != nil {
		return "", err
	}

	_, err = shared.RunCommand("ip", "link", "set", "dev", hostName, "up")
	if err != nil {
		return "", err
	}

	_, err = shared.RunCommand("ip", "route", "add", fmt.Sprintf("%s/32", peerAddrV4), "dev", hostName)
	if err != nil {
		return "", err
	}

	// Generate peer-side IPv6 address.
	peerAddrV6, peerPortMAC, err := networkRandomIPv6P2P()
	if err != nil {
		return "", err
	}

	_, err = shared.RunCommand("ip", "-6", "route", "add", fmt.Sprintf("%s/128", peerAddrV6), "dev", hostName)
	if err != nil {
		return "", err
	}

	_, err = shared.RunCommand("ip", "link", "set", "netns", projectName, peerName)
	if err != nil {
		return "", err
	}

	_, err = shared.RunCommand("ip", "-n", projectName, "link", "set", "dev", peerName, "name", "eth0", "address", peerPortMAC)
	if err != nil {
		return "", err
	}

	// No need for auto-generated link-local IPv6 addresses.
	_, err = shared.RunCommand("ip", "netns", "exec", projectName, "sysctl",
		fmt.Sprintf("net.ipv6.conf.%s.addr_gen_mode=1", "eth0"),
		fmt.Sprintf("net.ipv6.conf.%s.autoconf=0", "eth0"),
		"net.ipv6.conf.all.forwarding=1",
		fmt.Sprintf("net.ipv6.conf.%s.accept_ra=0", "eth0"),
		fmt.Sprintf("net.ipv6.conf.%s.forwarding=1", "eth0"),
		fmt.Sprintf("net.ipv4.conf.%s.forwarding=1", "eth0"),
	)
	if err != nil {
		return "", err
	}

	// Add peer address.
	_, err = shared.RunCommand("ip", "-n", projectName, "-4", "address", "add", fmt.Sprintf("%s/32", peerAddrV4), "dev", "eth0")
	if err != nil {
		return "", err
	}

	_, err = shared.RunCommand("ip", "-n", projectName, "-6", "address", "add", fmt.Sprintf("%s/128", peerAddrV6), "dev", "eth0")
	if err != nil {
		return "", err
	}

	// Bring interface up.
	_, err = shared.RunCommand("ip", "-n", projectName, "link", "set", "dev", "eth0", "up")
	if err != nil {
		return "", err
	}

	// Add route back to host.
	_, err = shared.RunCommand("ip", "-n", projectName, "-4", "route", "add", fmt.Sprintf("%s/32", extHostIPv4), "dev", "eth0")
	if err != nil {
		return "", err
	}

	// Add default routes.
	_, err = shared.RunCommand("ip", "-n", projectName, "-4", "route", "add", "default", "via", extHostIPv4, "dev", "eth0")
	if err != nil {
		return "", err
	}

	_, err = shared.RunCommand("ip", "-n", projectName, "-6", "route", "add", "default", "via", extHostIPv6, "dev", "eth0")
	if err != nil {
		return "", err
	}

	// Enable NAT.
	_, err = shared.RunCommand("ip", "netns", "exec", projectName, "iptables", "-t", "nat", "-A", "POSTROUTING", "-o", "eth0", "-j", "MASQUERADE")
	if err != nil {
		return "", err
	}

	_, err = shared.RunCommand("ip", "netns", "exec", projectName, "ip6tables", "-t", "nat", "-A", "POSTROUTING", "-o", "eth0", "-j", "MASQUERADE")
	if err != nil {
		return "", err
	}

	return hostName, nil
}

// createProjectRouter creates a project logical router and internal/external logical router ports, returns the
// name of the external interface.
func createProjectRouter(chassisName string, projectName string) (string, error) {
	shared.RunCommand("ovn-nbctl", "--if-exists", "lr-del", projectName)
	_, err := shared.RunCommand("ovn-nbctl", "lr-add", projectName)
	if err != nil {
		return "", err
	}

	_, err = shared.RunCommand("ovn-nbctl", "set", "logical_router", projectName, fmt.Sprintf("options:chassis=%s", chassisName))
	if err != nil {
		return "", err
	}

	// Create external router port.
	externalPortName := fmt.Sprintf("%s-lrp-ext", projectName)
	externalPortMAC, err := networkRandomMAC()
	if err != nil {
		return "", err
	}

	shared.RunCommand("ovn-nbctl", "--if-exists", "lrp-del", externalPortName)
	_, err = shared.RunCommand("ovn-nbctl", "lrp-add", projectName, externalPortName, externalPortMAC, fmt.Sprintf("%s/32", extOVNIPv4), fmt.Sprintf("%s/128", extOVNIPv6))
	if err != nil {
		return "", err
	}

	// Add default IPv4 route.
	_, err = shared.RunCommand("ovn-nbctl", "lr-route-add", projectName, "0.0.0.0/0", extNSOVNIPv4, externalPortName)
	if err != nil {
		return "", err
	}

	// Add default IPv6 route.
	_, err = shared.RunCommand("ovn-nbctl", "lr-route-add", projectName, "::/0", extNSOVNIPv6, externalPortName)
	if err != nil {
		return "", err
	}

	return externalPortName, nil
}

// createProjectExternalSwitch creates external logical switch, connects external router port to it and returns
// external switch name.
func createProjectExternalSwitch(projectName string, externalRouterPort string) (string, error) {
	// Create external project switch.
	externalSwitchName := fmt.Sprintf("%s-ls-ext", projectName)
	shared.RunCommand("ovn-nbctl", "--if-exists", "ls-del", externalSwitchName)
	_, err := shared.RunCommand("ovn-nbctl", "ls-add", externalSwitchName)
	if err != nil {
		return "", err
	}

	// Create logical switch router port.
	externalSwitchRouterPortName := fmt.Sprintf("%s-lsrp-ext", projectName)
	shared.RunCommand("ovn-nbctl", "--if-exists", "lsp-del", externalSwitchRouterPortName)
	_, err = shared.RunCommand("ovn-nbctl", "lsp-add", externalSwitchName, externalSwitchRouterPortName)
	if err != nil {
		return "", err
	}

	// Connect logical router port to switch.
	_, err = shared.RunCommand("ovn-nbctl", "lsp-set-type", externalSwitchRouterPortName, "router")
	if err != nil {
		return "", err
	}

	_, err = shared.RunCommand("ovn-nbctl", "lsp-set-addresses", externalSwitchRouterPortName, "router")
	if err != nil {
		return "", err
	}

	_, err = shared.RunCommand("ovn-nbctl", "lsp-set-options", externalSwitchRouterPortName, fmt.Sprintf("router-port=%s", externalRouterPort))
	if err != nil {
		return "", err
	}

	// Create switch port on external switch and move into project network namespace.
	externalSwitchNSPortName := fmt.Sprintf("%s-lsnsp-ext", projectName)
	externalSwitchNSPortMAC, err := networkRandomMAC()
	if err != nil {
		return "", err
	}

	shared.RunCommand("ovn-nbctl", "--if-exists", "lsp-del", externalSwitchNSPortName)
	_, err = shared.RunCommand("ovn-nbctl", "lsp-add", externalSwitchName, externalSwitchNSPortName)
	if err != nil {
		return "", err
	}

	_, err = shared.RunCommand("ovn-nbctl", "lsp-set-addresses", externalSwitchNSPortName, fmt.Sprintf("%s %s %s", externalSwitchNSPortMAC, extNSOVNIPv4, extNSOVNIPv6))
	if err != nil {
		return "", err
	}

	// Clear existing ports.
	existingPorts, err := shared.RunCommand("ovs-vsctl", "--format=csv", "--no-headings", "--data=bare", "--colum=name", "find", "interface", fmt.Sprintf("external-ids:iface-id=%s", externalSwitchNSPortName))
	if err != nil {
		return "", err
	}

	existingPorts = strings.TrimSpace(existingPorts)
	if existingPorts != "" {
		for _, uuid := range strings.Split(existingPorts, "\n") {
			_, err = shared.RunCommand("ovs-vsctl", "del-port", uuid)
			if err != nil {
				return "", err
			}
		}
	}

	// Create veth pair from project network namespace to host network namespace.
	hostName := networkRandomDevName("extrh")
	peerName := networkRandomDevName("extrp")

	_, err = shared.RunCommand("ip", "link", "add", "dev", hostName, "type", "veth", "peer", "name", peerName)
	if err != nil {
		return "", err
	}

	// No need for auto-generated link-local IPv6 addresses.
	_, err = shared.RunCommand("sysctl",
		fmt.Sprintf("net.ipv6.conf.%s.addr_gen_mode=1", hostName),
		fmt.Sprintf("net.ipv6.conf.%s.autoconf=0", hostName),
		fmt.Sprintf("net.ipv6.conf.%s.accept_ra=0", hostName),
		fmt.Sprintf("net.ipv6.conf.%s.forwarding=1", hostName),
		fmt.Sprintf("net.ipv4.conf.%s.forwarding=1", hostName),
	)
	if err != nil {
		return "", err
	}

	_, err = shared.RunCommand("ovs-vsctl", "add-port", "br-int", hostName)
	if err != nil {
		return "", err
	}

	_, err = shared.RunCommand("ovs-vsctl", "set", "interface", hostName, fmt.Sprintf("external_ids:iface-id=%s", externalSwitchNSPortName))
	if err != nil {
		return "", err
	}

	_, err = shared.RunCommand("ip", "link", "set", hostName, "up")
	if err != nil {
		return "", err
	}

	_, err = shared.RunCommand("ip", "link", "set", "netns", projectName, peerName, "name", "eth1", "address", externalSwitchNSPortMAC)
	if err != nil {
		return "", err
	}

	// No need for auto-generated link-local IPv6 addresses.
	_, err = shared.RunCommand("ip", "netns", "exec", projectName, "sysctl",
		fmt.Sprintf("net.ipv6.conf.%s.addr_gen_mode=1", "eth1"),
		fmt.Sprintf("net.ipv6.conf.%s.autoconf=0", "eth1"),
		fmt.Sprintf("net.ipv6.conf.%s.accept_ra=0", "eth1"),
		fmt.Sprintf("net.ipv6.conf.%s.forwarding=1", "eth1"),
		fmt.Sprintf("net.ipv4.conf.%s.forwarding=1", "eth1"),
	)
	if err != nil {
		return "", err
	}

	_, err = shared.RunCommand("ip", "-n", projectName, "link", "set", "dev", "eth1", "up")
	if err != nil {
		return "", err
	}

	_, err = shared.RunCommand("ip", "-n", projectName, "-4", "address", "add", fmt.Sprintf("%s/32", extNSOVNIPv4), "dev", "eth1")
	if err != nil {
		return "", err
	}

	_, err = shared.RunCommand("ip", "-n", projectName, "-4", "route", "add", fmt.Sprintf("%s/32", extOVNIPv4), "dev", "eth1")
	if err != nil {
		return "", err
	}

	_, err = shared.RunCommand("ip", "-n", projectName, "-6", "address", "add", fmt.Sprintf("%s/128", extNSOVNIPv6), "dev", "eth1")
	if err != nil {
		return "", err
	}

	_, err = shared.RunCommand("ip", "-n", projectName, "-6", "route", "add", fmt.Sprintf("%s/128", extOVNIPv6), "dev", "eth1")
	if err != nil {
		return "", err
	}

	return externalSwitchName, nil
}

// createProjectInternalSwitch creates internal logical switch, connects internal router port to it and returns
// internal switch name and DHCPv4 and DHCPv6 options ID.
func createProjectInternalSwitch(projectName string, network network) (string, string, string, error) {
	// Create router port.
	internalRouterPortName := fmt.Sprintf("%s-%s-lrp-int", projectName, network.name)
	internalRouterPortMAC, err := networkRandomMAC()
	routerIPv4, cidrV4, err := net.ParseCIDR(network.gwV4)
	if err != nil {
		return "", "", "", err
	}

	_, cidrV6, err := net.ParseCIDR(network.gwV6)
	if err != nil {
		return "", "", "", err
	}

	// Create internal logical router port.
	shared.RunCommand("ovn-nbctl", "--if-exists", "lrp-del", internalRouterPortName)
	_, err = shared.RunCommand("ovn-nbctl", "lrp-add", projectName, internalRouterPortName, internalRouterPortMAC, network.gwV4, network.gwV6)
	if err != nil {
		return "", "", "", err
	}

	// Configure IPv6 Router Advertisements.
	_, err = shared.RunCommand("ovn-nbctl", "set", "logical_router_port", internalRouterPortName,
		"ipv6_ra_configs:send_periodic=true",
		"ipv6_ra_configs:address_mode=slaac",
		"ipv6_ra_configs:min_interval=10",
		"ipv6_ra_configs:max_interval=15",
		fmt.Sprintf("ipv6_ra_configs:rdnss=%s", dnsV6),
		fmt.Sprintf("ipv6_ra_configs:dnssl=%s", dnsV6SearchDomains),
	)
	if err != nil {
		return "", "", "", err
	}

	// Create internal project switch.
	internalSwitchName := fmt.Sprintf("%s-%s-ls-int", projectName, network.name)
	shared.RunCommand("ovn-nbctl", "--if-exists", "ls-del", internalSwitchName)
	_, err = shared.RunCommand("ovn-nbctl", "ls-add", internalSwitchName)
	if err != nil {
		return "", "", "", err
	}

	// Setup DHCP.
	_, err = shared.RunCommand("ovn-nbctl", "set", "logical_switch", internalSwitchName,
		fmt.Sprintf("other_config:subnet=%s", cidrV4.String()),
		"other_config:exclude_ips=10.0.0.1..10.0.0.10",
		fmt.Sprintf("other_config:ipv6_prefix=%s", cidrV6.String()),
	)
	if err != nil {
		return "", "", "", err
	}

	// Clear existing DHCP options.
	existingOpts, err := shared.RunCommand("ovn-nbctl", "--format=csv", "--no-headings", "--data=bare", "--colum=_uuid", "find", "dhcp_options", fmt.Sprintf("external_ids:lxd_network=%s", internalSwitchName))
	if err != nil {
		return "", "", "", err
	}

	existingOpts = strings.TrimSpace(existingOpts)
	if existingOpts != "" {
		for _, uuid := range strings.Split(existingOpts, "\n") {
			_, err = shared.RunCommand("ovn-nbctl", "destroy", "dhcp_options", uuid)
			if err != nil {
				return "", "", "", err
			}
		}
	}

	DHCPv4Opt, err := shared.RunCommand("ovn-nbctl", "create", "dhcp_option",
		fmt.Sprintf("external_ids:lxd_network=%s", internalSwitchName),
		fmt.Sprintf("cidr=%s", cidrV4.String()),
	)
	if err != nil {
		return "", "", "", err
	}
	DHCPv4Opt = strings.TrimSpace(DHCPv4Opt)

	// We have to use dhcp-options-set-options rather than the command above as its the only way to allow the
	// domain_name option to be properly escaped.
	_, err = shared.RunCommand("ovn-nbctl", "dhcp-options-set-options", DHCPv4Opt,
		fmt.Sprintf("server_id=%s", routerIPv4.String()),
		fmt.Sprintf("router=%s", routerIPv4.String()),
		fmt.Sprintf("server_mac=%s", internalRouterPortMAC),
		"lease_time=3600",
		fmt.Sprintf("dns_server=%s", dnsV4),
		fmt.Sprintf(`domain_name="%s"`, dnsDomainName),
	)
	if err != nil {
		return "", "", "", err
	}

	DHCPv6Opt, err := shared.RunCommand("ovn-nbctl", "create", "dhcp_option",
		fmt.Sprintf("external_ids:lxd_network=%s", internalSwitchName),
		fmt.Sprintf(`cidr="%s"`, cidrV6.String()),
	)
	if err != nil {
		return "", "", "", err
	}
	DHCPv6Opt = strings.TrimSpace(DHCPv6Opt)

	// We have to use dhcp-options-set-options rather than the command above as its the only way to allow the
	// domain_search option to be properly escaped.
	_, err = shared.RunCommand("ovn-nbctl", "dhcp-options-set-options", DHCPv6Opt,
		fmt.Sprintf("server_id=%s", internalRouterPortMAC),
		fmt.Sprintf(`domain_search="%s"`, dnsDomainName),
		fmt.Sprintf("dns_server=%s", dnsV6),
	)
	if err != nil {
		return "", "", "", err
	}

	// Create logical switch router port.
	internalSwitchRouterPortName := fmt.Sprintf("%s-%s-lsrp-int", projectName, network.name)
	shared.RunCommand("ovn-nbctl", "--if-exists", "lsp-del", internalSwitchRouterPortName)
	_, err = shared.RunCommand("ovn-nbctl", "lsp-add", internalSwitchName, internalSwitchRouterPortName)
	if err != nil {
		return "", "", "", err
	}

	// Connect logical router port to switch.
	_, err = shared.RunCommand("ovn-nbctl", "lsp-set-type", internalSwitchRouterPortName, "router")
	if err != nil {
		return "", "", "", err
	}

	_, err = shared.RunCommand("ovn-nbctl", "lsp-set-addresses", internalSwitchRouterPortName, "router")
	if err != nil {
		return "", "", "", err
	}

	_, err = shared.RunCommand("ovn-nbctl", "lsp-set-options", internalSwitchRouterPortName, fmt.Sprintf("router-port=%s", internalRouterPortName))
	if err != nil {
		return "", "", "", err
	}

	// Add return route in project external namespace.
	_, err = shared.RunCommand("ip", "-n", projectName, "route", "add", cidrV4.String(), "via", extOVNIPv4, "dev", "eth1")
	if err != nil {
		return "", "", "", err
	}

	// Add return route in project external namespace.
	_, err = shared.RunCommand("ip", "-n", projectName, "-6", "route", "add", cidrV6.String(), "via", extOVNIPv6, "dev", "eth1")
	if err != nil {
		return "", "", "", err
	}

	return internalSwitchName, DHCPv4Opt, DHCPv6Opt, nil
}

// addInstancePort creates veth pair and connects host side to internal switch. Returns peer interface name for
// adding to an instance and MAC address of the port.
func addInstancePort(projectName string, internalSwitchName string, instanceName string, DHCPv4Opt string, DHCPv6Opt string) (string, string, error) {
	instancePortName := fmt.Sprintf("%s-ls-inst-%s", projectName, instanceName)
	shared.RunCommand("ovn-nbctl", "--if-exists", "lsp-del", instancePortName)
	_, err := shared.RunCommand("ovn-nbctl", "lsp-add", internalSwitchName, instancePortName)
	if err != nil {
		return "", "", err
	}

	instancePortMAC, err := networkRandomMAC()
	if err != nil {
		return "", "", err
	}

	_, err = shared.RunCommand("ovn-nbctl", "lsp-set-addresses", instancePortName, fmt.Sprintf("%s dynamic", instancePortMAC))
	if err != nil {
		return "", "", err
	}

	_, err = shared.RunCommand("ovn-nbctl", "lsp-set-dhcpv4-options", instancePortName, DHCPv4Opt)
	if err != nil {
		return "", "", err
	}

	_, err = shared.RunCommand("ovn-nbctl", "lsp-set-dhcpv6-options", instancePortName, DHCPv6Opt)
	if err != nil {
		return "", "", err
	}

	// Clear existing OVS ports.
	existingPorts, err := shared.RunCommand("ovs-vsctl", "--format=csv", "--no-headings", "--data=bare", "--colum=name", "find", "interface", fmt.Sprintf("external_ids:iface-id=%s", instancePortName))
	if err != nil {
		return "", "", err
	}

	existingPorts = strings.TrimSpace(existingPorts)
	if existingPorts != "" {
		for _, port := range strings.Split(existingPorts, "\n") {
			_, err = shared.RunCommand("ovs-vsctl", "del-port", port)
			if err != nil {
				return "", "", err
			}

			shared.RunCommand("ip", "link", "del", port)
		}
	}

	// Create veth pair from project network namespace to host network namespace.
	hostName := networkRandomDevName("insth")
	peerName := networkRandomDevName("instp")

	_, err = shared.RunCommand("ip", "link", "add", "dev", hostName, "type", "veth", "peer", "name", peerName)
	if err != nil {
		return "", "", err
	}

	// No need for auto-generated link-local IPv6 addresses.
	_, err = shared.RunCommand("sysctl",
		fmt.Sprintf("net.ipv6.conf.%s.addr_gen_mode=1", hostName),
		fmt.Sprintf("net.ipv6.conf.%s.autoconf=0", hostName),
		fmt.Sprintf("net.ipv6.conf.%s.accept_ra=0", hostName),
		fmt.Sprintf("net.ipv6.conf.%s.forwarding=0", hostName),
		fmt.Sprintf("net.ipv4.conf.%s.forwarding=0", hostName),
	)
	if err != nil {
		return "", "", err
	}

	_, err = shared.RunCommand("ip", "link", "set", "dev", peerName, "address", instancePortMAC)
	if err != nil {
		return "", "", err
	}

	// Connect host end to integration bridge.
	_, err = shared.RunCommand("ovs-vsctl", "add-port", "br-int", hostName)
	if err != nil {
		return "", "", err
	}

	_, err = shared.RunCommand("ovs-vsctl", "set", "interface", hostName, fmt.Sprintf("external_ids:iface-id=%s", instancePortName))
	if err != nil {
		return "", "", err
	}

	_, err = shared.RunCommand("ip", "link", "set", "dev", hostName, "up")
	if err != nil {
		return "", "", err
	}

	return peerName, instancePortMAC, nil
}

func createInstance(projectName string, instanceName string, instPortName string) error {
	instName := fmt.Sprintf("%s-%s", projectName, instanceName)
	shared.RunCommand("lxc", "delete", "-f", instName)

	_, err := shared.RunCommand("lxc", "init", "images:alpine/3.11", instName)
	if err != nil {
		return err
	}

	_, err = shared.RunCommand("lxc", "config", "device", "add", instName, "eth0", "nic", "nictype=physical", "name=eth0", fmt.Sprintf("parent=%s", instPortName))
	if err != nil {
		return err
	}

	_, err = shared.RunCommand("lxc", "start", instName)
	if err != nil {
		return err
	}

	return nil
}
