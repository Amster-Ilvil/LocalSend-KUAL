package app

import (
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type SelfTestResult struct {
	OK    bool
	Lines []string
}

func (r SelfTestResult) Text() string {
	return strings.Join(r.Lines, "\n") + "\n"
}

func SelfTest(root string, cfg Config) SelfTestResult {
	result := SelfTestResult{OK: true}
	pidPath := filepath.Join(root, "state", "daemon.pid")
	if b, err := os.ReadFile(pidPath); err == nil {
		pidText := strings.TrimSpace(string(b))
		if running, _ := IsDaemonRunning(root); running {
			result.Lines = append(result.Lines, "PID=OK "+pidText)
		} else {
			result.OK = false
			result.Lines = append(result.Lines, "PID=FAIL stale-or-wrong-process "+pidText)
		}
	} else {
		result.OK = false
		result.Lines = append(result.Lines, "PID=FAIL")
	}

	client := &http.Client{Timeout: 2 * time.Second}
	url := fmt.Sprintf("http://127.0.0.1:%d/_kindle/health", cfg.Port)
	if resp, err := client.Get(url); err == nil {
		resp.Body.Close()
		if resp.StatusCode == http.StatusOK {
			result.Lines = append(result.Lines, fmt.Sprintf("LOCAL_HTTP=OK 127.0.0.1:%d", cfg.Port))
		} else {
			result.OK = false
			result.Lines = append(result.Lines, fmt.Sprintf("LOCAL_HTTP=FAIL status=%d", resp.StatusCode))
		}
	} else {
		result.OK = false
		result.Lines = append(result.Lines, "LOCAL_HTTP=FAIL "+err.Error())
	}

	if c, err := net.DialTimeout("tcp4", fmt.Sprintf("127.0.0.1:%d", cfg.Port), time.Second); err == nil {
		c.Close()
		result.Lines = append(result.Lines, fmt.Sprintf("TCP_LISTEN=OK :%d", cfg.Port))
	} else {
		result.OK = false
		result.Lines = append(result.Lines, "TCP_LISTEN=FAIL "+err.Error())
	}

	if ip, err := findInterfaceIPv4(cfg.Interface); err == nil {
		result.Lines = append(result.Lines, "WIFI_IP="+ip)
	} else {
		result.Lines = append(result.Lines, "WIFI_IP=UNKNOWN")
	}

	if free, err := availableBytes(cfg.ReceiveDir); err == nil {
		freeMiB := free / (1 << 20)
		if free < MinFreeReserveBytes {
			result.OK = false
			result.Lines = append(result.Lines, fmt.Sprintf("STORAGE=LOW free=%dMiB reserve=16MiB", freeMiB))
		} else {
			result.Lines = append(result.Lines, fmt.Sprintf("STORAGE=OK free=%dMiB reserve=16MiB", freeMiB))
		}
	} else {
		result.OK = false
		result.Lines = append(result.Lines, "STORAGE=FAIL "+err.Error())
	}

	if path, err := findIptables(); err == nil {
		if err := runIPT(path, "-L", firewallChain, "-n"); err == nil {
			result.Lines = append(result.Lines, "FIREWALL=OPEN tcp/udp "+fmt.Sprint(cfg.Port))
		} else {
			result.OK = false
			result.Lines = append(result.Lines, "FIREWALL=FAIL chain missing")
		}
	} else {
		result.OK = false
		result.Lines = append(result.Lines, "FIREWALL=FAIL iptables missing")
	}
	return result
}

func findInterfaceIPv4(name string) (string, error) {
	iface, err := net.InterfaceByName(name)
	if err != nil {
		return "", err
	}
	addrs, err := iface.Addrs()
	if err != nil {
		return "", err
	}
	for _, a := range addrs {
		ip, _, err := net.ParseCIDR(a.String())
		if err == nil && ip.To4() != nil {
			return ip.String(), nil
		}
	}
	return "", fmt.Errorf("no IPv4")
}
