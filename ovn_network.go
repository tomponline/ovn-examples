package main

import (
	"bytes"
	cryptoRand "crypto/rand"
	"encoding/hex"
	"fmt"
	"log"
	"math/big"
	"math/rand"
	"os"
	"strings"

	"github.com/lxc/lxd/shared"
)

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
		extHostName, err := createProjectExternalNamespace(projectName)
		if err != nil {
			log.Fatal(err)
		}
		log.Printf("Created external host veth interface %q linked to netns %q", extHostName, projectName)

		extRouterPortName, intRouterPortName, intRouterPortMAC, err := createProjectRouter(chassisName, projectName)
		if err != nil {
			log.Fatal(err)
		}
		log.Printf("Created project router %q and ports (%q/%q) for chassis %q", projectName, intRouterPortName, extRouterPortName, chassisName)

		externalSwitchName, err := createProjectExternalSwitch(projectName, extRouterPortName)
		if err != nil {
			log.Fatal(err)
		}
		log.Printf("Created project external switch %q and linked external port %q to it", externalSwitchName, extRouterPortName)

		networks := []string{"net1"}
		for _, networkName := range networks {
			internalSwitchName, DHCPv4Opt, err := createProjectInternalSwitch(projectName, intRouterPortName, intRouterPortMAC, networkName)
			if err != nil {
				log.Fatal(err)
			}
			log.Printf("Created project internal switch %q (DHCP %q) and linked router port %q to it", internalSwitchName, DHCPv4Opt, intRouterPortName)

			instances := []string{"c1", "c2"}
			for _, instance := range instances {
				instPortName, instPortMac, err := addInstancePort(projectName, internalSwitchName, instance, DHCPv4Opt)
				if err != nil {
					log.Fatal(err)
				}
				log.Printf("Created instance port %q (%q)", instPortName, instPortMac)

				err = createInstance(projectName, instance, instPortName)
				if err != nil {
					log.Fatal(err)
				}
				log.Printf("Created instance %q using port %q", instance, instPortName)
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

	// Create veth pair from project network namespace to host network namespace.
	hostName := networkRandomDevName("exth")
	peerName := networkRandomDevName("extp")

	_, err = shared.RunCommand("ip", "link", "add", "dev", hostName, "type", "veth", "peer", "name", peerName)
	if err != nil {
		return "", err
	}

	_, err = shared.RunCommand("ip", "address", "add", "169.254.0.1/30", "dev", hostName)
	if err != nil {
		return "", err
	}

	_, err = shared.RunCommand("ip", "link", "set", "dev", hostName, "up")
	if err != nil {
		return "", err
	}

	_, err = shared.RunCommand("ip", "link", "set", "netns", projectName, peerName)
	if err != nil {
		return "", err
	}

	_, err = shared.RunCommand("ip", "-n", projectName, "link", "set", "dev", peerName, "name", "eth0", "up")
	if err != nil {
		return "", err
	}

	_, err = shared.RunCommand("ip", "-n", projectName, "address", "add", "169.254.0.2/30", "dev", "eth0")
	if err != nil {
		return "", err
	}

	_, err = shared.RunCommand("ip", "-n", projectName, "route", "add", "default", "via", "169.254.0.1", "dev", "eth0")
	if err != nil {
		return "", err
	}

	_, err = shared.RunCommand("ip", "netns", "exec", projectName, "iptables", "-t", "nat", "-A", "POSTROUTING", "-o", "eth0", "-j", "MASQUERADE")
	if err != nil {
		return "", err
	}

	return hostName, nil
}

// createProjectRouter creates a project logical router and internal/external logical router ports, returns the
// name of the external and internal router ports created, and the internal port MAC address.
func createProjectRouter(chassisName string, projectName string) (string, string, string, error) {
	shared.RunCommand("ovn-nbctl", "--if-exists", "lr-del", projectName)
	_, err := shared.RunCommand("ovn-nbctl", "lr-add", projectName)
	if err != nil {
		return "", "", "", err
	}

	_, err = shared.RunCommand("ovn-nbctl", "set", "logical_router", projectName, fmt.Sprintf("options:chassis=%s", chassisName))
	if err != nil {
		return "", "", "", err
	}

	// Create external router port.
	externalPortName := fmt.Sprintf("%s-lrp-ext", projectName)
	externalPortMAC, err := networkRandomMAC()
	if err != nil {
		return "", "", "", err
	}

	shared.RunCommand("ovn-nbctl", "--if-exists", "lrp-del", externalPortName)
	_, err = shared.RunCommand("ovn-nbctl", "lrp-add", projectName, externalPortName, externalPortMAC, "169.254.1.2/30")
	if err != nil {
		return "", "", "", err
	}

	internalPortName := fmt.Sprintf("%s-lrp-int", projectName)
	internalPortMAC, err := networkRandomMAC()
	shared.RunCommand("ovn-nbctl", "--if-exists", "lrp-del", internalPortName)
	_, err = shared.RunCommand("ovn-nbctl", "lrp-add", projectName, internalPortName, internalPortMAC, "10.0.0.1/24")
	if err != nil {
		return "", "", "", err
	}

	// Add default route.
	_, err = shared.RunCommand("ovn-nbctl", "lr-route-add", projectName, "0.0.0.0/0", "169.254.1.1")
	if err != nil {
		return "", "", "", err
	}

	return externalPortName, internalPortName, internalPortMAC, nil
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

	_, err = shared.RunCommand("ovn-nbctl", "lsp-set-addresses", externalSwitchNSPortName, fmt.Sprintf("%s %s", externalSwitchNSPortMAC, "169.254.1.1"))
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

	_, err = shared.RunCommand("ip", "link", "set", "netns", projectName, peerName, "name", "eth1", "address", externalSwitchNSPortMAC, "up")
	if err != nil {
		return "", err
	}

	_, err = shared.RunCommand("ip", "-n", projectName, "address", "add", "169.254.1.1/30", "dev", "eth1")
	if err != nil {
		return "", err
	}

	return externalSwitchName, nil
}

// createProjectInternalSwitch creates internal logical switch, connects internal router port to it and returns
// internal switch name and DHCP options ID.
func createProjectInternalSwitch(projectName string, internalRouterPort string, internalRouterPortMAC string, networkName string) (string, string, error) {
	cidr := "10.0.0.0/24"
	routerIP := "10.0.0.1"

	// Create internal project switch.
	internalSwitchName := fmt.Sprintf("%s-%s-ls-int", projectName, networkName)
	shared.RunCommand("ovn-nbctl", "--if-exists", "ls-del", internalSwitchName)
	_, err := shared.RunCommand("ovn-nbctl", "ls-add", internalSwitchName)
	if err != nil {
		return "", "", err
	}

	// Setup DHCP.
	_, err = shared.RunCommand("ovn-nbctl", "set", "logical_switch", internalSwitchName,
		fmt.Sprintf("other_config:subnet=%s", cidr),
		"other_config:exclude_ips=10.0.0.1..10.0.0.10",
	)
	if err != nil {
		return "", "", err
	}

	// Clear existing DHCP options.
	existingOpts, err := shared.RunCommand("ovn-nbctl", "--format=csv", "--no-headings", "--data=bare", "--colum=_uuid", "find", "dhcp_options", fmt.Sprintf("external_ids:lxd_network=%s", internalSwitchName))
	if err != nil {
		return "", "", err
	}

	existingOpts = strings.TrimSpace(existingOpts)
	if existingOpts != "" {
		for _, uuid := range strings.Split(existingOpts, "\n") {
			_, err = shared.RunCommand("ovn-nbctl", "destroy", "dhcp_options", uuid)
			if err != nil {
				return "", "", err
			}
		}
	}

	DHCPv4Opt, err := shared.RunCommand("ovn-nbctl", "create", "dhcp_option", fmt.Sprintf("external_ids:lxd_network=%s", internalSwitchName), fmt.Sprintf("cidr=%s", cidr))
	if err != nil {
		return "", "", err
	}
	DHCPv4Opt = strings.TrimSpace(DHCPv4Opt)

	// We have to use dhcp-options-set-options rather than the command above as its the only way to allow the
	// domain_name option to be properly escaped.
	_, err = shared.RunCommand("ovn-nbctl", "dhcp-options-set-options", DHCPv4Opt,
		fmt.Sprintf("server_id=%s", routerIP),
		fmt.Sprintf("router=%s", routerIP),
		fmt.Sprintf("server_mac=%s", internalRouterPortMAC),
		"lease_time=3600",
		"dns_server=8.8.8.8",
		`domain_name="lxd"`,
	)
	if err != nil {
		return "", "", err
	}

	// Create logical switch router port.
	internalSwitchRouterPortName := fmt.Sprintf("%s-lsrp-int", projectName)
	shared.RunCommand("ovn-nbctl", "--if-exists", "lsp-del", internalSwitchRouterPortName)
	_, err = shared.RunCommand("ovn-nbctl", "lsp-add", internalSwitchName, internalSwitchRouterPortName)
	if err != nil {
		return "", "", err
	}

	// Connect logical router port to switch.
	_, err = shared.RunCommand("ovn-nbctl", "lsp-set-type", internalSwitchRouterPortName, "router")
	if err != nil {
		return "", "", err
	}

	_, err = shared.RunCommand("ovn-nbctl", "lsp-set-addresses", internalSwitchRouterPortName, "router")
	if err != nil {
		return "", "", err
	}

	_, err = shared.RunCommand("ovn-nbctl", "lsp-set-options", internalSwitchRouterPortName, fmt.Sprintf("router-port=%s", internalRouterPort))
	if err != nil {
		return "", "", err
	}

	// Add return route in project external namespace.
	_, err = shared.RunCommand("ip", "-n", projectName, "route", "add", cidr, "via", "169.254.1.2", "dev", "eth1")
	if err != nil {
		return "", "", err
	}

	return internalSwitchName, DHCPv4Opt, nil
}

// addInstancePort creates veth pair and connects host side to internal switch. Returns peer interface name for
// adding to an instance and MAC address of the port.
func addInstancePort(projectName string, internalSwitchName string, instanceName string, DHCPv4Opt string) (string, string, error) {
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

	_, err := shared.RunCommand("lxc", "init", "images:ubuntu/focal", instName)
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
