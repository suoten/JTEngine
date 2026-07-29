package module

import (
	"crypto/sha256"
	"fmt"
	"net"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
)

// P1-FIX: 指纹缓存——进程生命周期内指纹不变，避免每次校验都 fork 进程
var (
	fingerprintOnce sync.Once
	fingerprintVal  string
	fingerprintErr  error
)

func GetMachineFingerprint() (string, error) {
	fingerprintOnce.Do(func() {
		var data string

		data += getCPUID()
		data += getMACAddresses()
		data += getDiskSerial()
		data += getHostname()
		data += getKernelVersion()
		data += getBaseboardSerial()
		data += getBIOSSerial()

		hash := sha256.Sum256([]byte(data))
		fingerprintVal = fmt.Sprintf("%x", hash[:16])
	})
	return fingerprintVal, fingerprintErr
}

// getBaseboardSerial 获取主板序列号（Linux: dmidecode 不可用则降级到 /sys；
// Windows: wmic baseboard get SerialNumber）。
func getBaseboardSerial() string {
	switch runtime.GOOS {
	case "linux":
		// 优先读取 /sys 经典路径（无需 root）
		for _, path := range []string{
			"/sys/class/dmi/id/board_serial",
			"/sys/devices/virtual/dmi/id/board_serial",
		} {
			if out, err := os.ReadFile(path); err == nil {
				s := strings.TrimSpace(string(out))
				if s != "" {
					return s
				}
			}
		}
	case "windows":
		if out, err := exec.Command("wmic", "baseboard", "get", "SerialNumber").Output(); err == nil {
			lines := strings.Split(strings.TrimSpace(string(out)), "\n")
			if len(lines) >= 2 {
				return strings.TrimSpace(lines[1])
			}
		}
	}
	return ""
}

// getBIOSSerial 获取 BIOS 序列号（Linux: /sys/class/dmi/id/bios_version + bios_date；
// Windows: wmic bios get SerialNumber）。
func getBIOSSerial() string {
	switch runtime.GOOS {
	case "linux":
		var biosInfo string
		for _, path := range []string{
			"/sys/class/dmi/id/bios_version",
			"/sys/class/dmi/id/bios_date",
			"/sys/class/dmi/id/bios_vendor",
		} {
			if out, err := os.ReadFile(path); err == nil {
				biosInfo += strings.TrimSpace(string(out))
			}
		}
		return biosInfo
	case "windows":
		if out, err := exec.Command("wmic", "bios", "get", "SerialNumber").Output(); err == nil {
			lines := strings.Split(strings.TrimSpace(string(out)), "\n")
			if len(lines) >= 2 {
				return strings.TrimSpace(lines[1])
			}
		}
	}
	return ""
}

func getCPUID() string {
	switch runtime.GOOS {
	case "linux":
		if out, err := exec.Command("cat", "/proc/cpuinfo").Output(); err == nil {
			for _, line := range strings.Split(string(out), "\n") {
				if strings.HasPrefix(line, "model name") || strings.HasPrefix(line, "Serial") {
					return strings.TrimSpace(strings.SplitN(line, ":", 2)[1])
				}
			}
		}
	case "windows":
		if out, err := exec.Command("wmic", "cpu", "get", "ProcessorId").Output(); err == nil {
			lines := strings.Split(strings.TrimSpace(string(out)), "\n")
			if len(lines) >= 2 {
				return strings.TrimSpace(lines[1])
			}
		}
	}
	return runtime.GOARCH
}

func getMACAddresses() string {
	interfaces, err := net.Interfaces()
	if err != nil {
		return ""
	}

	var macs string
	for _, iface := range interfaces {
		if len(iface.HardwareAddr) > 0 && !isVirtualInterface(iface.Name) {
			macs += iface.HardwareAddr.String()
		}
	}
	return macs
}

func isVirtualInterface(name string) bool {
	virtual := []string{"lo", "docker", "veth", "br-", "virbr", "vnic", "vmnet"}
	lower := strings.ToLower(name)
	for _, v := range virtual {
		if strings.HasPrefix(lower, v) {
			return true
		}
	}
	return false
}

func getDiskSerial() string {
	switch runtime.GOOS {
	case "linux":
		if out, err := exec.Command("lsblk", "-ndo", "SERIAL").Output(); err == nil {
			lines := strings.Split(strings.TrimSpace(string(out)), "\n")
			for _, line := range lines {
				s := strings.TrimSpace(line)
				if s != "" {
					return s
				}
			}
		}
	case "windows":
		if out, err := exec.Command("wmic", "diskdrive", "get", "serialnumber").Output(); err == nil {
			lines := strings.Split(strings.TrimSpace(string(out)), "\n")
			if len(lines) >= 2 {
				return strings.TrimSpace(lines[1])
			}
		}
	}
	return ""
}

func getHostname() string {
	h, err := os.Hostname()
	if err != nil {
		return ""
	}
	return h
}

func getKernelVersion() string {
	switch runtime.GOOS {
	case "linux":
		if out, err := exec.Command("uname", "-r").Output(); err == nil {
			return strings.TrimSpace(string(out))
		}
	case "windows":
		if out, err := exec.Command("cmd", "/c", "ver").Output(); err == nil {
			return strings.TrimSpace(string(out))
		}
	}
	return runtime.GOOS
}
