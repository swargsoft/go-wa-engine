//go:build linux

package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"text/template"
)

const unitPath = "/etc/systemd/system/wa-engine.service"
const sleepHookPath = "/lib/systemd/system-sleep/wa-engine"

const unitTemplate = `[Unit]
Description=WA Engine - WhatsApp HTTP API
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
ExecStart={{.BinaryPath}} --port {{.Port}} --data {{.DataDir}}{{if .APIKey}} --key {{.APIKey}}{{end}}
Restart=on-failure
RestartSec=5
# Clean stop on sleep is handled by the system-sleep hook below.

[Install]
WantedBy=multi-user.target
`

// sleepHookScript is installed to /lib/systemd/system-sleep/wa-engine.
// systemd calls it with "pre suspend" before sleep and "post suspend" after wake.
const sleepHookScript = `#!/bin/sh
# wa-engine sleep/wake hook — managed by wa-engine --install-service
case "$1/$2" in
  pre/suspend|pre/hibernate|pre/hybrid-sleep)
    systemctl stop wa-engine
    ;;
  post/suspend|post/hibernate|post/hybrid-sleep)
    systemctl start wa-engine
    ;;
esac
`

type unitVars struct {
	BinaryPath string
	Port       int
	DataDir    string
	APIKey     string
}

func installService() error {
	if os.Geteuid() != 0 {
		return fmt.Errorf("--install-service requires root (run with sudo)")
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

	// Write unit file
	f, err := os.OpenFile(unitPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0644)
	if err != nil {
		return fmt.Errorf("cannot write unit file: %w", err)
	}
	defer f.Close()

	tmpl := template.Must(template.New("unit").Parse(unitTemplate))
	if err := tmpl.Execute(f, unitVars{
		BinaryPath: binaryPath,
		Port:       *flagPort,
		DataDir:    dataDir,
		APIKey:     *flagAPIKey,
	}); err != nil {
		return fmt.Errorf("cannot render unit file: %w", err)
	}

	// Write sleep hook
	if err := os.MkdirAll(filepath.Dir(sleepHookPath), 0755); err == nil {
		if err := os.WriteFile(sleepHookPath, []byte(sleepHookScript), 0755); err != nil {
			fmt.Printf("warning: could not install sleep hook: %v\n", err)
		}
	}

	run := func(args ...string) error {
		out, err := exec.Command(args[0], args[1:]...).CombinedOutput()
		if err != nil {
			return fmt.Errorf("%v failed: %v\n%s", args, err, out)
		}
		return nil
	}

	if err := run("systemctl", "daemon-reload"); err != nil {
		return err
	}
	if err := run("systemctl", "enable", "wa-engine"); err != nil {
		return err
	}
	if err := run("systemctl", "start", "wa-engine"); err != nil {
		return err
	}

	fmt.Printf("✓ wa-engine service installed and started\n")
	fmt.Printf("  Unit:    %s\n", unitPath)
	fmt.Printf("  Data:    %s\n", dataDir)
	fmt.Printf("  Port:    %d\n", *flagPort)
	fmt.Printf("\nManage with:\n")
	fmt.Printf("  sudo systemctl status wa-engine\n")
	fmt.Printf("  sudo systemctl stop   wa-engine\n")
	fmt.Printf("  sudo systemctl start  wa-engine\n")
	fmt.Printf("  journalctl -u wa-engine -f\n")
	fmt.Printf("  sudo ./waengine --uninstall-service\n")
	return nil
}

func uninstallService() error {
	if os.Geteuid() != 0 {
		return fmt.Errorf("--uninstall-service requires root (run with sudo)")
	}

	_ = exec.Command("systemctl", "stop", "wa-engine").Run()
	_ = exec.Command("systemctl", "disable", "wa-engine").Run()
	_ = os.Remove(unitPath)
	_ = os.Remove(sleepHookPath)
	_ = exec.Command("systemctl", "daemon-reload").Run()

	fmt.Println("✓ wa-engine service removed")
	return nil
}
