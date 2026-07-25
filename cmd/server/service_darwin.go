//go:build darwin

package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"text/template"
)

const (
	serviceName = "com.swargsoft.msgly"
	plistPath   = "/Library/LaunchDaemons/com.swargsoft.msgly.plist"
)

const plistTemplate = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>com.swargsoft.msgly</string>

    <key>ProgramArguments</key>
    <array>
        <string>{{.BinaryPath}}</string>
        <string>--port</string>
        <string>{{.Port}}</string>
        <string>--data</string>
        <string>{{.DataDir}}</string>
        {{- if .APIKey}}
        <string>--key</string>
        <string>{{.APIKey}}</string>
        {{- end}}
    </array>

    <key>RunAtLoad</key>
    <true/>
    <key>KeepAlive</key>
    <true/>
    <key>ThrottleInterval</key>
    <integer>5</integer>

    <key>StandardOutPath</key>
    <string>/var/log/msgly-engine.log</string>
    <key>StandardErrorPath</key>
    <string>/var/log/msgly-engine.log</string>
</dict>
</plist>
`

type plistVars struct {
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

	f, err := os.OpenFile(plistPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0644)
	if err != nil {
		return fmt.Errorf("cannot write plist: %w", err)
	}
	defer f.Close()

	tmpl := template.Must(template.New("plist").Parse(plistTemplate))
	if err := tmpl.Execute(f, plistVars{
		BinaryPath: binaryPath,
		Port:       *flagPort,
		DataDir:    dataDir,
		APIKey:     *flagAPIKey,
	}); err != nil {
		return fmt.Errorf("cannot render plist: %w", err)
	}

	_ = exec.Command("launchctl", "unload", plistPath).Run()

	if out, err := exec.Command("launchctl", "load", "-w", plistPath).CombinedOutput(); err != nil {
		return fmt.Errorf("launchctl load failed: %v\n%s", err, out)
	}

	fmt.Printf("✓ Msgly Service installed and started\n")
	fmt.Printf("  Plist:   %s\n", plistPath)
	fmt.Printf("  Data:    %s\n", dataDir)
	fmt.Printf("  Port:    %d\n", *flagPort)
	fmt.Printf("  Logs:    /var/log/msgly-engine.log\n")
	fmt.Printf("\nManage with:\n")
	fmt.Printf("  sudo launchctl stop  %s\n", serviceName)
	fmt.Printf("  sudo launchctl start %s\n", serviceName)
	fmt.Printf("  sudo ./msgly-engine --uninstall-service\n")
	return nil
}

func uninstallService() error {
	if os.Geteuid() != 0 {
		return fmt.Errorf("--uninstall-service requires root (run with sudo)")
	}

	_ = exec.Command("launchctl", "unload", "-w", plistPath).Run()
	if err := os.Remove(plistPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("cannot remove plist: %w", err)
	}

	fmt.Println("✓ Msgly Service removed")
	return nil
}
