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

	projects := []string{"project1", "project2", "project3"}
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

	return hostName, nil
}

// createProjectRouter creates a project logical router and external logical router port, returns the name of port.
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
	_, err = shared.RunCommand("ovn-nbctl", "lrp-add", projectName, externalPortName, externalPortMAC, "169.254.1.2/30")
	if err != nil {
		return "", err
	}

	_, err = shared.RunCommand("ovn-nbctl", "lr-route-add", projectName, "0.0.0.0/0", "169.254.1.1")
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

	// Create internal switch port on external switch and move into project network namespace.
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

	_, err = shared.RunCommand("ovn-nbctl", "lsp-set-addresses", externalSwitchNSPortName, fmt.Sprintf("%s %s", externalSwitchNSPortMAC, "169.254.1.1/30"))
	if err != nil {
		return "", err
	}

	// Materialise the port on host via OVS integration bridge.
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
