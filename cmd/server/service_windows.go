//go:build windows

package main

import (
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows/svc"

	core "github.com/mml/wa-engine/core"
)

const (
	serviceName        = "MsglyService"
	serviceDisplayName = "Msgly Service"
	serviceDescription = "Background service for Msgly App."
)

func isWindowsServiceRun() bool {
	inService, err := svc.IsWindowsService()
	if err != nil || !inService {
		return false
	}

	logFile := openLogFile()
	if logFile != nil {
		log.SetOutput(io.MultiWriter(os.Stderr, logFile))
		defer logFile.Close()
	}

	log.Printf("wa-engine %s service starting", core.Version)

	err = svc.Run(serviceName, &winSvc{})
	if err != nil {
		log.Fatalf("Windows service failed: %v", err)
	}
	return true
}

func openLogFile() *os.File {
	dataDir := *flagData
	if dataDir == "" {
		dataDir = defaultDataDir()
	}
	if err := os.MkdirAll(dataDir, 0700); err != nil {
		return nil
	}
	f, err := os.OpenFile(filepath.Join(dataDir, "service.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return nil
	}
	return f
}

type winSvc struct{}

func (w *winSvc) Execute(args []string, r <-chan svc.ChangeRequest, s chan<- svc.Status) (bool, uint32) {
	s <- svc.Status{State: svc.Running, Accepts: svc.AcceptStop | svc.AcceptShutdown}
	log.Printf("service running, starting HTTP server")

	errCh := make(chan error, 1)
	stop := make(chan os.Signal, 1)

	go func() {
		defer func() {
			if r := recover(); r != nil {
				errCh <- fmt.Errorf("panic: %v", r)
			}
		}()
		if err := runServer(stop); err != nil {
			errCh <- err
		}
	}()

	for {
		select {
		case err := <-errCh:
			log.Printf("server error: %v", err)
			return true, 1
		case c, ok := <-r:
			if !ok {
				return false, 0
			}
			switch c.Cmd {
			case svc.Interrogate:
				s <- c.CurrentStatus
			case svc.Stop, svc.Shutdown:
				log.Printf("service stopping")
				s <- svc.Status{State: svc.StopPending}
				close(stop)
				return false, 0
			}
		}
	}
}

var (
	modShell32              = syscall.NewLazyDLL("shell32.dll")
	procShellExecuteW       = modShell32.NewProc("ShellExecuteW")
	modAdvapi32             = syscall.NewLazyDLL("advapi32.dll")
	procGetTokenInformation = modAdvapi32.NewProc("GetTokenInformation")
)

// isElevated returns true if the current process has Administrator privileges.
func isElevated() bool {
	var token syscall.Token
	proc, _ := syscall.GetCurrentProcess()
	if err := syscall.OpenProcessToken(proc, syscall.TOKEN_QUERY, &token); err != nil {
		return false
	}
	defer token.Close()

	// TokenElevation = 20
	var elevation uint32
	var size uint32
	procGetTokenInformation.Call(
		uintptr(token),
		20,
		uintptr(unsafe.Pointer(&elevation)),
		4,
		uintptr(unsafe.Pointer(&size)),
	)
	return elevation != 0
}

// relaunchElevated re-launches this executable with the same args via
// ShellExecuteW "runas", which triggers the Windows UAC prompt.
func relaunchElevated() error {
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("cannot resolve executable: %w", err)
	}
	args := strings.Join(os.Args[1:], " ")

	verb, _ := syscall.UTF16PtrFromString("runas")
	file, _ := syscall.UTF16PtrFromString(exe)
	params, _ := syscall.UTF16PtrFromString(args)
	dir, _ := syscall.UTF16PtrFromString(filepath.Dir(exe))

	// SW_SHOWNORMAL = 1
	ret, _, _ := procShellExecuteW.Call(
		0,
		uintptr(unsafe.Pointer(verb)),
		uintptr(unsafe.Pointer(file)),
		uintptr(unsafe.Pointer(params)),
		uintptr(unsafe.Pointer(dir)),
		1,
	)
	if ret <= 32 {
		return fmt.Errorf("ShellExecuteW returned %d — try running PowerShell as Administrator manually", ret)
	}
	return nil
}

func elevateIfNeeded() {
	if !isElevated() {
		fmt.Println("Requesting Administrator privileges (UAC prompt will appear)...")
		if err := relaunchElevated(); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		os.Exit(0)
	}
}

func installService() error {
	elevateIfNeeded()

	// Use os.Executable() — more reliable than os.Args[0] after UAC re-launch.
	binaryPath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("cannot resolve binary path: %w", err)
	}
	binaryPath, err = filepath.Abs(binaryPath)
	if err != nil {
		return fmt.Errorf("cannot make binary path absolute: %w", err)
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

	// Build the service binary path string.
	// CRITICAL: binPath= must be a SINGLE argument to sc.exe — key=value with no space
	// between = and value. Passing them as separate exec.Command args breaks SCM parsing.
	binPath := fmt.Sprintf(`"%s" --port %d --data "%s"`, binaryPath, *flagPort, dataDir)
	if *flagAPIKey != "" {
		binPath += fmt.Sprintf(` --key "%s"`, *flagAPIKey)
	}

	// Stop + delete any existing instance (idempotent).
	run("sc", "stop", serviceName)
	run("sc", "delete", serviceName)

	// sc create — binPath= value must be one combined argument.
	if out, err := exec.Command("sc", "create", serviceName,
		"binPath="+binPath,
		"start=auto",
		"DisplayName="+serviceDisplayName,
	).CombinedOutput(); err != nil {
		return fmt.Errorf("sc create failed: %v\n%s", err, out)
	}

	// Set description.
	if out, err := exec.Command("sc", "description", serviceName, serviceDescription).CombinedOutput(); err != nil {
		fmt.Printf("warning: sc description: %v — %s\n", err, out)
	}

	// Configure failure recovery: restart after 5s on first/second failure, 30s after that.
	exec.Command("sc", "failure", serviceName,
		"reset=86400",
		"actions=restart/5000/restart/5000/restart/30000",
	).Run()

	// Start the service now.
	if out, err := exec.Command("sc", "start", serviceName).CombinedOutput(); err != nil {
		return fmt.Errorf("sc start failed: %v\n%s\n\nCheck the log file at %s\\service.log for details.", err, out, dataDir)
	}

	fmt.Printf("\n✓ %s installed and started\n", serviceDisplayName)
	fmt.Printf("  Service:  %s\n", serviceName)
	fmt.Printf("  Binary:   %s\n", binaryPath)
	fmt.Printf("  Data:     %s\n", dataDir)
	fmt.Printf("  Port:     %d\n", *flagPort)
	fmt.Printf("  Log:      %s\\service.log\n", dataDir)
	fmt.Printf("\nManage with:\n")
	fmt.Printf("  sc stop   %s\n", serviceName)
	fmt.Printf("  sc start  %s\n", serviceName)
	fmt.Printf("  sc query  %s\n", serviceName)
	fmt.Printf("  type \"%s\\service.log\"\n", dataDir)
	fmt.Printf("  .\\msgly-engine.exe --uninstall-service\n")
	return nil
}

func uninstallService() error {
	elevateIfNeeded()

	run("sc", "stop", serviceName)
	if out, err := exec.Command("sc", "delete", serviceName).CombinedOutput(); err != nil {
		return fmt.Errorf("sc delete failed: %v\n%s", err, out)
	}
	fmt.Printf("✓ %s removed\n", serviceDisplayName)
	return nil
}

// run executes a command and ignores errors (used for idempotent cleanup steps).
func run(name string, args ...string) {
	_ = exec.Command(name, args...).Run()
}
