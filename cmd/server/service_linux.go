//go:build linux

package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"text/template"
)

const (
	unitPath      = "/etc/systemd/system/msgly-engine.service"
	sleepHookPath = "/lib/systemd/system-sleep/msgly-engine"
)

const unitTemplate = `[Unit]
Description=Background service for Msgly App.
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
ExecStart={{.BinaryPath}} --port {{.Port}} --data {{.DataDir}}{{if .APIKey}} --key {{.APIKey}}{{end}}
Restart=on-failure
RestartSec=5

[Install]
WantedBy=multi-user.target
`

const sleepHookScript = `#!/bin/sh
# Msgly engine sleep/wake hook — managed by msgly-engine --install-service
case "$1/$2" in
  pre/suspend|pre/hibernate|pre/hybrid-sleep)
    systemctl stop msgly-engine
    ;;
  post/suspend|post/hibernate|post/hybrid-sleep)
    systemctl start msgly-engine
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

	if err := os.MkdirAll(filepath.Dir(sleepHookPath), 0755); err == nil {
		if err := os.WriteFile(sleepHookPath, []byte(sleepHookScript), 0755); err != nil {
			fmt.Printf("warning: could not install sleep hook: %v\n", err)
		}
	}

	mustRun := func(args ...string) error {
		out, err := exec.Command(args[0], args[1:]...).CombinedOutput()
		if err != nil {
			return fmt.Errorf("%v: %v\n%s", args, err, out)
		}
		return nil
	}

	if err := mustRun("systemctl", "daemon-reload"); err != nil {
		return err
	}
	if err := mustRun("systemctl", "enable", "msgly-engine"); err != nil {
		return err
	}
	if err := mustRun("systemctl", "start", "msgly-engine"); err != nil {
		return err
	}

	fmt.Printf("✓ Msgly Service installed and started\n")
	fmt.Printf("  Unit:    %s\n", unitPath)
	fmt.Printf("  Data:    %s\n", dataDir)
	fmt.Printf("  Port:    %d\n", *flagPort)
	fmt.Printf("\nManage with:\n")
	fmt.Printf("  sudo systemctl status msgly-engine\n")
	fmt.Printf("  sudo systemctl stop   msgly-engine\n")
	fmt.Printf("  sudo systemctl start  msgly-engine\n")
	fmt.Printf("  journalctl -u msgly-engine -f\n")
	fmt.Printf("  sudo ./msgly-engine --uninstall-service\n")
	return nil
}

func uninstallService() error {
	if os.Geteuid() != 0 {
		return fmt.Errorf("--uninstall-service requires root (run with sudo)")
	}

	_ = exec.Command("systemctl", "stop", "msgly-engine").Run()
	_ = exec.Command("systemctl", "disable", "msgly-engine").Run()
	_ = os.Remove(unitPath)
	_ = os.Remove(sleepHookPath)
	_ = exec.Command("systemctl", "daemon-reload").Run()

	fmt.Println("✓ Msgly Service removed")
	return nil
}
