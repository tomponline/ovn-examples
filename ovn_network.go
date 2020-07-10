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
	name         string
	gw4          string
	gw6          string
	dns4         string
	dns6         string
	extBridge    string
	extIP4       string
	extIP6Prefix string
	extGW4       string
	extGW6       string
}

const ndbIP = "10.109.89.242"
const haChassisGroup = "group1"

// Define DNS settings.
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

	projects := []string{"project1"}
	for _, projectName := range projects {

		// Define the networks we want each project to have.
		networks := []network{
			network{
				name:         "net1",
				gw4:          "10.0.0.1/24",
				gw6:          "fd47:8ac3:9083:35f6::1/64",
				extBridge:    "lxdbr0",
				extIP4:       "10.189.38.100/24",
				extIP6Prefix: "fd42:26e3:9819:5f0a::/64",
				extGW4:       "10.189.38.1",
				extGW6:       "fd42:26e3:9819:5f0a::1",
				dns4:         "10.189.38.1",
				dns6:         "fd42:26e3:9819:5f0a::1",
			},
			/*network{
				name:         "net2",
				gwV4:         "192.168.3.1/24",
				gwV6:         "fd47:8ac3:9083:36f6::1/64",
				extBridge:    "lxdbr0",
				extIP4:       "10.189.38.101/24",
				extIP6Prefix: "fd42:26e3:9819:5f0a::/64",
				extGW4:       "10.189.38.1",
				extGW6:       "fd42:26e3:9819:5f0a::1",
				dns4:         "10.189.38.1",
				dns6:         "fd42:26e3:9819:5f0a::1",
			},*/
		}

		for _, network := range networks {
			err = createLogicalRouter(projectName, chassisName, network)
			if err != nil {
				log.Fatal(err)
			}
			log.Printf("Created logical router and logical external switch for %q", network.name)

			internalSwitchName, DHCPv4Opt, DHCPv6Opt, err := createProjectInternalSwitch(projectName, network)
			if err != nil {
				log.Fatal(err)
			}
			log.Printf("Created project internal switch %q (DHCP %q) and connected router to it", internalSwitchName, DHCPv4Opt)

			// Create the instances we want to connect to this network.
			instances := []string{"c1"}
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

func getExternalOVSBridgeName(projectName string, network network) string {
	return fmt.Sprintf("%s-ext-br", getLogicalRouterName(projectName, network))
}

func getLogicalRouterName(projectName string, network network) string {
	return fmt.Sprintf("%s-%s", projectName, network.name)
}

func getLogicalExtSwitchName(projectName string, network network) string {
	return fmt.Sprintf("%s-%s-ls-ext", projectName, network.name)
}

func getLogicalExtSwitchRouterPortNames(projectName string, network network) (string, string) {
	return fmt.Sprintf("%s-%s-lrp-ext", projectName, network.name), fmt.Sprintf("%s-%s-lsp-router-ext", projectName, network.name)
}

func getLogicalExtSwitchParentPortName(projectName string, network network) string {
	return fmt.Sprintf("%s-%s-lsp-parent-ext", projectName, network.name)
}

func getLogicalIntSwitchName(projectName string, network network) string {
	return fmt.Sprintf("%s-%s-ls-int", projectName, network.name)
}

func clearOVSPort(externalIfaceID string) error {
	// Clear existing ports that have externalIfaceID.
	existingPorts, err := shared.RunCommand("ovs-vsctl", "--format=csv", "--no-headings", "--data=bare", "--colum=name", "find", "interface", fmt.Sprintf("external-ids:iface-id=%s", externalIfaceID))
	if err != nil {
		return err
	}

	existingPorts = strings.TrimSpace(existingPorts)
	if existingPorts != "" {
		for _, port := range strings.Split(existingPorts, "\n") {
			_, err = shared.RunCommand("ovs-vsctl", "del-port", port)
			if err != nil {
				return err
			}

			shared.RunCommand("ip", "link", "del", port)
		}
	}

	return nil
}

func connectOVStoOVN(chassisName string) error {
	// Get our chassis IP.
	output, err := shared.RunCommand("ip", "route", "get", "8.8.8.8")
	if err != nil {
		return err
	}

	fields := strings.Fields(output)
	ip := fields[6]

	/*
		ipParts := strings.Split(ip, ".") // Use last octet as priority.
		// No --may-exist argument is supported by this command.
		shared.RunCommand("ovn-nbctl", "ha-chassis-group-add", haChassisGroup)
		_, err = shared.RunCommand("ovn-nbctl", "ha-chassis-group-add-chassis", haChassisGroup, chassisName, ipParts[3])
		if err != nil {
			return err
		}
	*/

	// Connect local machine OVS to local OVN database.
	// The "." record seems to be a way to specify the first record in this table,
	// although can't find any docs on this, only numerous examples using this style.
	_, err = shared.RunCommand("ovs-vsctl", "set", "open_vswitch", ".",
		fmt.Sprintf("external_ids:ovn-remote=tcp:%s:6642", ndbIP),
		"external_ids:ovn-remote-probe-interval=10000",
		fmt.Sprintf("external_ids:ovn-encap-ip=%s", ip),
		"external_ids:ovn-encap-type=geneve",
		fmt.Sprintf("external_ids:system-id=%s", chassisName),
	)
	if err != nil {
		return err
	}

	return nil
}

// createLogicalRouter creates logical router and external logical switch.
// Connects router to OVS integration bridge and connects integration bridge port to parent network bridge.
func createLogicalRouter(projectName string, chassisName string, network network) error {
	// Generate MAC address for logical router's external port.
	lrpExtMACStr, err := networkRandomMAC()
	if err != nil {
		return err
	}

	lrpExtMAC, err := net.ParseMAC(lrpExtMACStr)
	if err != nil {
		return err
	}

	extIP4, extNet4, err := net.ParseCIDR(network.extIP4)
	if err != nil {
		return err
	}

	extIP6, extNet6, err := net.ParseCIDR(network.extIP6Prefix)
	if err != nil {
		return err
	}

	// Generate external logical router port IPv6 in parent's prefix (Use ULA prefix for EUI64 IP generation).
	extIP6, err = eui64.ParseMAC(extIP6, lrpExtMAC)
	if err != nil {
		return err
	}

	extIP4Net := net.IPNet{
		IP:   extIP4,
		Mask: extNet4.Mask,
	}

	extIP6Net := net.IPNet{
		IP:   extIP6,
		Mask: extNet6.Mask,
	}

	// Create logical router.
	logicalRouterName := getLogicalRouterName(projectName, network)
	shared.RunCommand("ovn-nbctl", "--if-exists", "lr-del", logicalRouterName)
	_, err = shared.RunCommand("ovn-nbctl", "lr-add", logicalRouterName)
	if err != nil {
		return err
	}

	// Create external router port.
	externalRouterPortName, externalSwitchRouterPortName := getLogicalExtSwitchRouterPortNames(projectName, network)

	shared.RunCommand("ovn-nbctl", "--if-exists", "lrp-del", externalRouterPortName)
	_, err = shared.RunCommand("ovn-nbctl", "lrp-add", logicalRouterName, externalRouterPortName, lrpExtMACStr, extIP4Net.String(), extIP6Net.String())
	if err != nil {
		return err
	}

	/*
		// HA chassis ports cause problems with IPv6 SNAT.
		// Assign external router port chassis group.
		chassisGroupID, err := shared.RunCommand("ovn-nbctl", "--format=csv", "--no-headings", "--data=bare", "--colum=_uuid", "find", "ha_chassis_group", fmt.Sprintf("name=%s", haChassisGroup))
		if err != nil {
			return err
		}

		chassisGroupID = strings.TrimSpace(chassisGroupID)
		_, err = shared.RunCommand("ovn-nbctl", "set", "logical_router_port", externalRouterPortName, fmt.Sprintf("ha_chassis_group=%s", chassisGroupID))
		if err != nil {
			return err
		}
	*/

	// Set a static chassis for this logical router (otherwise IPv6 NDP resolution fails when using SNAT).
	_, err = shared.RunCommand("ovn-nbctl", "set", "logical_router", logicalRouterName, fmt.Sprintf("options:chassis=%s", chassisName))
	if err != nil {
		return err
	}

	// Add default IPv4 route.
	_, err = shared.RunCommand("ovn-nbctl", "lr-route-add", logicalRouterName, "0.0.0.0/0", network.extGW4)
	if err != nil {
		return err
	}

	// Add default IPv6 route.
	_, err = shared.RunCommand("ovn-nbctl", "lr-route-add", logicalRouterName, "::/0", network.extGW6)
	if err != nil {
		return err
	}

	// Add SNAT rules.
	_, intNet4, err := net.ParseCIDR(network.gw4)
	if err != nil {
		return err
	}

	_, err = shared.RunCommand("ovn-nbctl", "lr-nat-add", logicalRouterName, "snat", extIP4.String(), intNet4.String())
	if err != nil {
		return err
	}

	_, intNet6, err := net.ParseCIDR(network.gw6)
	if err != nil {
		return err
	}

	_, err = shared.RunCommand("ovn-nbctl", "lr-nat-add", logicalRouterName, "snat", extIP6.String(), intNet6.String())
	if err != nil {
		return err
	}

	// Create logical external network switch.
	externalSwitchName := getLogicalExtSwitchName(projectName, network)
	shared.RunCommand("ovn-nbctl", "--if-exists", "ls-del", externalSwitchName)
	_, err = shared.RunCommand("ovn-nbctl", "ls-add", externalSwitchName)
	if err != nil {
		return err
	}

	// Create logical external switch router port.
	shared.RunCommand("ovn-nbctl", "--if-exists", "lsp-del", externalSwitchRouterPortName)
	_, err = shared.RunCommand("ovn-nbctl", "lsp-add", externalSwitchName, externalSwitchRouterPortName)
	if err != nil {
		return err
	}

	// Connect logical router port to switch.
	_, err = shared.RunCommand("ovn-nbctl", "lsp-set-type", externalSwitchRouterPortName, "router")
	if err != nil {
		return err
	}

	_, err = shared.RunCommand("ovn-nbctl", "lsp-set-addresses", externalSwitchRouterPortName, "router")
	if err != nil {
		return err
	}

	_, err = shared.RunCommand("ovn-nbctl", "lsp-set-options", externalSwitchRouterPortName,
		fmt.Sprintf("router-port=%s", externalRouterPortName),
		fmt.Sprintf("nat-addresses=%s", "router"),
	)
	if err != nil {
		return err
	}

	// Create logical external switch port for parent bridge.
	externalSwitchParentPortName := getLogicalExtSwitchParentPortName(projectName, network)
	shared.RunCommand("ovn-nbctl", "--if-exists", "lsp-del", externalSwitchParentPortName)
	_, err = shared.RunCommand("ovn-nbctl", "lsp-add", externalSwitchName, externalSwitchParentPortName)
	if err != nil {
		return err
	}

	// Forward any unknown MAC frames down this port.
	_, err = shared.RunCommand("ovn-nbctl", "lsp-set-addresses", externalSwitchParentPortName, "unknown")
	if err != nil {
		return err
	}

	_, err = shared.RunCommand("ovn-nbctl", "lsp-set-type", externalSwitchParentPortName, "localport")
	if err != nil {
		return err
	}

	// Create veth pair to connect logical external switch to parent bridge.
	hostName := networkRandomDevName("exth")
	peerName := networkRandomDevName("extp")

	_, err = shared.RunCommand("ip", "link", "add", "dev", hostName, "type", "veth", "peer", "name", peerName)
	if err != nil {
		return err
	}

	// No need for auto-generated link-local IPv6 addresses on host interface connected to bridge.
	_, err = shared.RunCommand("sysctl",
		fmt.Sprintf("net.ipv6.conf.%s.disable_ipv6=1", hostName),
		fmt.Sprintf("net.ipv4.conf.%s.forwarding=0", hostName),
	)
	if err != nil {
		return err
	}

	// No need for auto-generated link-local IPv6 addresses on host interface connected to bridge.
	_, err = shared.RunCommand("sysctl",
		fmt.Sprintf("net.ipv6.conf.%s.disable_ipv6=1", peerName),
		fmt.Sprintf("net.ipv4.conf.%s.forwarding=0", peerName),
	)
	if err != nil {
		return err
	}

	// Connect to parent bridge.
	_, err = shared.RunCommand("ip", "link", "set", "master", network.extBridge, "up", hostName)
	if err != nil {
		return err
	}

	// Connect to OVS integration bridge.
	err = clearOVSPort(externalSwitchParentPortName)
	if err != nil {
		return err
	}

	_, err = shared.RunCommand("ovs-vsctl", "add-port", "br-int", peerName)
	if err != nil {
		return err
	}

	_, err = shared.RunCommand("ovs-vsctl", "set", "interface", peerName, fmt.Sprintf("external_ids:iface-id=%s", externalSwitchParentPortName))
	if err != nil {
		return err
	}

	_, err = shared.RunCommand("ip", "link", "set", peerName, "up")
	if err != nil {
		return err
	}

	return nil
}

// createProjectInternalSwitch creates internal logical switch, connects internal router port to it and returns
// internal switch name and DHCPv4 and DHCPv6 options ID.
func createProjectInternalSwitch(projectName string, network network) (string, string, string, error) {
	logicalRouterName := getLogicalRouterName(projectName, network)

	// Create router port.
	internalRouterPortName := fmt.Sprintf("%s-%s-lrp-int", projectName, network.name)
	internalRouterPortMAC, err := networkRandomMAC()
	routerIPv4, cidrV4, err := net.ParseCIDR(network.gw4)
	if err != nil {
		return "", "", "", err
	}

	_, cidrV6, err := net.ParseCIDR(network.gw6)
	if err != nil {
		return "", "", "", err
	}

	// Create internal logical router port.
	shared.RunCommand("ovn-nbctl", "--if-exists", "lrp-del", internalRouterPortName)
	_, err = shared.RunCommand("ovn-nbctl", "lrp-add", logicalRouterName, internalRouterPortName, internalRouterPortMAC, network.gw4, network.gw6)
	if err != nil {
		return "", "", "", err
	}

	// Configure IPv6 Router Advertisements.
	_, err = shared.RunCommand("ovn-nbctl", "set", "logical_router_port", internalRouterPortName,
		"ipv6_ra_configs:send_periodic=true",
		"ipv6_ra_configs:address_mode=slaac",
		"ipv6_ra_configs:min_interval=10",
		"ipv6_ra_configs:max_interval=15",
		fmt.Sprintf("ipv6_ra_configs:rdnss=%s", network.dns6),
		fmt.Sprintf("ipv6_ra_configs:dnssl=%s", dnsV6SearchDomains),
	)
	if err != nil {
		return "", "", "", err
	}

	// Create internal project switch.
	internalSwitchName := getLogicalIntSwitchName(projectName, network)
	shared.RunCommand("ovn-nbctl", "--if-exists", "ls-del", internalSwitchName)
	_, err = shared.RunCommand("ovn-nbctl", "ls-add", internalSwitchName)
	if err != nil {
		return "", "", "", err
	}

	// Setup DHCP.
	_, err = shared.RunCommand("ovn-nbctl", "set", "logical_switch", internalSwitchName,
		fmt.Sprintf("other_config:subnet=%s", cidrV4.String()),
		fmt.Sprintf("other_config:exclude_ips=%s %s", routerIPv4, network.dns4),
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
		fmt.Sprintf("dns_server=%s", network.dns4),
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
		fmt.Sprintf("dns_server=%s", network.dns6),
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
	err = clearOVSPort(instancePortName)
	if err != nil {
		return "", "", err
	}

	// Create veth pair from project network namespace to host network namespace.
	hostName := networkRandomDevName("insth")
	peerName := networkRandomDevName("instp")

	_, err = shared.RunCommand("ip", "link", "add", "dev", hostName, "type", "veth", "peer", "name", peerName)
	if err != nil {
		return "", "", err
	}

	// No need for auto-generated link-local IPv6 addresses on host interface connected to bridge.
	_, err = shared.RunCommand("sysctl",
		fmt.Sprintf("net.ipv6.conf.%s.disable_ipv6=1", hostName),
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
