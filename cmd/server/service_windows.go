//go:build windows

package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"unsafe"
)

const serviceName = "WaEngine"

var (
	modShell32       = syscall.NewLazyDLL("shell32.dll")
	procShellExecute = modShell32.NewProc("ShellExecuteW")

	modAdvapi32      = syscall.NewLazyDLL("advapi32.dll")
	procOpenProcessToken   = modAdvapi32.NewProc("OpenProcessToken")
	procGetTokenInformation = modAdvapi32.NewProc("GetTokenInformation")
)

// isElevated returns true if the current process has Administrator privileges.
func isElevated() bool {
	var token syscall.Token
	proc, _ := syscall.GetCurrentProcess()
	// TOKEN_QUERY = 0x0008
	if err := syscall.OpenProcessToken(proc, 0x0008, &token); err != nil {
		return false
	}
	defer token.Close()

	// TokenElevation = 20
	var elevation uint32
	var size uint32
	procGetTokenInformation.Call(
		uintptr(token),
		20, // TokenElevation
		uintptr(unsafe.Pointer(&elevation)),
		4,
		uintptr(unsafe.Pointer(&size)),
	)
	return elevation != 0
}

// relaunchElevated re-launches the current executable with the same arguments
// via ShellExecuteW "runas" — this triggers the Windows UAC prompt.
func relaunchElevated() error {
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("cannot resolve executable path: %w", err)
	}

	// Rebuild args string (skip os.Args[0])
	args := strings.Join(os.Args[1:], " ")

	verb, _ := syscall.UTF16PtrFromString("runas")
	file, _ := syscall.UTF16PtrFromString(exe)
	params, _ := syscall.UTF16PtrFromString(args)
	dir, _ := syscall.UTF16PtrFromString(filepath.Dir(exe))

	// SW_SHOWNORMAL = 1
	ret, _, _ := procShellExecute.Call(
		0,
		uintptr(unsafe.Pointer(verb)),
		uintptr(unsafe.Pointer(file)),
		uintptr(unsafe.Pointer(params)),
		uintptr(unsafe.Pointer(dir)),
		1,
	)
	// ShellExecute returns > 32 on success
	if ret <= 32 {
		return fmt.Errorf("UAC elevation failed (ShellExecute returned %d)", ret)
	}
	return nil
}

func installService() error {
	// If not elevated, trigger UAC prompt and re-launch — same UX as sudo on macOS.
	if !isElevated() {
		fmt.Println("Administrator privileges required. Requesting elevation via UAC...")
		if err := relaunchElevated(); err != nil {
			return fmt.Errorf("could not elevate: %w\nTry right-clicking PowerShell and selecting 'Run as Administrator'")
		}
		// The elevated process will do the actual work; exit this one.
		os.Exit(0)
	}

	binaryPath, err := filepath.Abs(os.Args[0])
	if err != nil {
		return fmt.Errorf("cannot resolve binary path: %w", err)
	}

	dataDir := *flagData
	if dataDir == "" {
		dataDir = defaultDataDir()
	}
	dataDir, err = filepath.Abs(dataDir)
	if err != nil {
		return fmt.Errorf("cannot resolve data dir: %w", err)
	}
	if err := os.MkdirAll(dataDir, 0700); err != nil {
		return fmt.Errorf("cannot create data dir: %w", err)
	}

	binCmd := fmt.Sprintf(`"%s" --port %d --data "%s"`, binaryPath, *flagPort, dataDir)
	if *flagAPIKey != "" {
		binCmd += fmt.Sprintf(` --key "%s"`, *flagAPIKey)
	}

	// Remove existing service first (idempotent).
	_ = exec.Command("sc", "stop", serviceName).Run()
	_ = exec.Command("sc", "delete", serviceName).Run()

	out, err := exec.Command(
		"sc", "create", serviceName,
		"binPath=", binCmd,
		"start=", "auto",
		"DisplayName=", "WA Engine",
	).CombinedOutput()
	if err != nil {
		return fmt.Errorf("sc create failed: %v\n%s", err, out)
	}

	_ = exec.Command("sc", "description", serviceName, "WhatsApp HTTP API engine").Run()

	if out, err := exec.Command("sc", "start", serviceName).CombinedOutput(); err != nil {
		return fmt.Errorf("sc start failed: %v\n%s", err, out)
	}

	fmt.Printf("✓ wa-engine service installed and started\n")
	fmt.Printf("  Service: %s\n", serviceName)
	fmt.Printf("  Data:    %s\n", dataDir)
	fmt.Printf("  Port:    %d\n", *flagPort)
	fmt.Printf("\nManage with:\n")
	fmt.Printf("  sc stop   %s\n", serviceName)
	fmt.Printf("  sc start  %s\n", serviceName)
	fmt.Printf("  .\\waengine.exe --uninstall-service\n")
	return nil
}

func uninstallService() error {
	if !isElevated() {
		fmt.Println("Administrator privileges required. Requesting elevation via UAC...")
		if err := relaunchElevated(); err != nil {
			return fmt.Errorf("could not elevate: %w\nTry right-clicking PowerShell and selecting 'Run as Administrator'")
		}
		os.Exit(0)
	}

	_ = exec.Command("sc", "stop", serviceName).Run()
	out, err := exec.Command("sc", "delete", serviceName).CombinedOutput()
	if err != nil {
		return fmt.Errorf("sc delete failed: %v\n%s", err, out)
	}
	fmt.Println("✓ wa-engine service removed")
	return nil
}
