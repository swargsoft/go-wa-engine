//go:build windows

package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

const serviceName = "WaEngine"

func installService() error {
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

	// Add description
	_ = exec.Command("sc", "description", serviceName, "WhatsApp HTTP API engine").Run()

	// Start immediately
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
	_ = exec.Command("sc", "stop", serviceName).Run()
	out, err := exec.Command("sc", "delete", serviceName).CombinedOutput()
	if err != nil {
		return fmt.Errorf("sc delete failed: %v\n%s", err, out)
	}
	fmt.Println("✓ wa-engine service removed")
	return nil
}
