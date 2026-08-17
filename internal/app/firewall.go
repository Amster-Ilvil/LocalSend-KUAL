package app

import (
	"bytes"
	"fmt"
	"log"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

const firewallChain = "LSKUAL"

type FirewallLease struct {
	iptables string
	iface    string
	port     int
	logger   *log.Logger
	active   bool
}

func findIptables() (string, error) {
	if forced := os.Getenv("LOCALSEND_IPTABLES"); forced != "" {
		return forced, nil
	}
	candidates := []string{"/usr/sbin/iptables", "/sbin/iptables", "/usr/bin/iptables", "/bin/iptables"}
	for _, p := range candidates {
		if _, err := exec.LookPath(p); err == nil {
			return p, nil
		}
	}
	if p, err := exec.LookPath("iptables"); err == nil {
		return p, nil
	}
	return "", fmt.Errorf("iptables executable not found")
}

func runIPT(path string, args ...string) error {
	cmd := exec.Command(path, args...)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		text := strings.TrimSpace(out.String())
		if text != "" {
			return fmt.Errorf("iptables %s: %w: %s", strings.Join(args, " "), err, text)
		}
		return fmt.Errorf("iptables %s: %w", strings.Join(args, " "), err)
	}
	return nil
}

func cleanupFirewallWith(path, iface string) {
	if iface == "" {
		iface = "wlan0"
	}
	for i := 0; i < 8; i++ {
		if runIPT(path, "-D", "INPUT", "-i", iface, "-j", firewallChain) != nil {
			break
		}
	}
	_ = runIPT(path, "-F", firewallChain)
	_ = runIPT(path, "-X", firewallChain)
}

func OpenFirewall(iface string, port int, logger *log.Logger) (*FirewallLease, error) {
	if iface == "" {
		iface = "wlan0"
	}
	path, err := findIptables()
	if err != nil {
		return &FirewallLease{}, err
	}
	// Remove a stale lease left by a crash/SIGKILL before recreating it.
	cleanupFirewallWith(path, iface)
	if err := runIPT(path, "-N", firewallChain); err != nil {
		return &FirewallLease{}, err
	}
	fail := func(err error) (*FirewallLease, error) {
		cleanupFirewallWith(path, iface)
		return &FirewallLease{}, err
	}
	p := strconv.Itoa(port)
	if err := runIPT(path, "-A", firewallChain, "-p", "tcp", "--dport", p, "-j", "ACCEPT"); err != nil {
		return fail(err)
	}
	if err := runIPT(path, "-A", firewallChain, "-p", "udp", "--dport", p, "-j", "ACCEPT"); err != nil {
		return fail(err)
	}
	if err := runIPT(path, "-I", "INPUT", "1", "-i", iface, "-j", firewallChain); err != nil {
		return fail(err)
	}
	return &FirewallLease{iptables: path, iface: iface, port: port, logger: logger, active: true}, nil
}

func (f *FirewallLease) Close() error {
	if f == nil || !f.active || f.iptables == "" {
		return nil
	}
	cleanupFirewallWith(f.iptables, f.iface)
	f.active = false
	return nil
}

func CleanupFirewall(iface string, port int, logger *log.Logger) error {
	path, err := findIptables()
	if err != nil {
		// On non-Kindle hosts this is harmless; on Kindle it is diagnostic.
		return err
	}
	cleanupFirewallWith(path, iface)
	if logger != nil {
		logger.Printf("firewall cleanup requested: interface=%s port=%d", iface, port)
	}
	return nil
}
