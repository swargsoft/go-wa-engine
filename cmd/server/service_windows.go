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

	core "github.com/swargsoft/wa-engine/core"
)

const (
	serviceName        = "MsglyService"
	serviceDisplayName = "Msgly Service"
	serviceDescription = "Background service for Msgly App."
)

// checkWindowsService is called from main() BEFORE flag.Parse().
// It returns true only when running as a Windows SCM service, in which
// case it parses flags itself, sets up logging, runs the service loop,
// and returns true when the service exits.
//
// On non-service runs (normal CLI) it returns false immediately so
// main() can continue with flag.Parse() as usual.
func checkWindowsService() bool {
	inService, err := svc.IsWindowsService()
	if err != nil || !inService {
		return false
	}

	// We are the SCM-launched service process.
	// Parse flags now (SCM does not inject extra args, so this is safe).
	flag.Parse()

	logFile := openLogFile()
	if logFile != nil {
		log.SetOutput(io.MultiWriter(os.Stderr, logFile))
		defer logFile.Close()
	}

	log.Printf("wa-engine %s service starting", core.Version)

	if err := svc.Run(serviceName, &winSvc{}); err != nil {
		log.Fatalf("Windows service failed: %v", err)
	}
	return true
}

func openLogFile() *os.File {
	dataDir := *flagData
	if dataDir == "" {
		dataDir = serviceDataDir()
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

// serviceDataDir returns a data directory that is writable by the SYSTEM
// account (which runs Windows services). os.UserHomeDir() under SYSTEM
// resolves to C:\Windows\system32\config\systemprofile — invisible to users.
// %PROGRAMDATA%\MsglyEngine (C:\ProgramData\MsglyEngine) is the right place.
func serviceDataDir() string {
	pd := os.Getenv("PROGRAMDATA")
	if pd == "" {
		pd = `C:\ProgramData`
	}
	return filepath.Join(pd, "MsglyEngine")
}

type winSvc struct{}

func (w *winSvc) Execute(args []string, r <-chan svc.ChangeRequest, s chan<- svc.Status) (bool, uint32) {
	s <- svc.Status{State: svc.Running, Accepts: svc.AcceptStop | svc.AcceptShutdown}
	log.Printf("service running, starting HTTP server")

	// Plain done channel — inside a Windows service no os.Signal arrives;
	// stop is signalled by closing this channel when SCM sends Stop/Shutdown.
	stop := make(chan struct{})

	errCh := make(chan error, 1)
	go func() {
		defer func() {
			if rec := recover(); rec != nil {
				errCh <- fmt.Errorf("panic: %v", rec)
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

func isElevated() bool {
	var token syscall.Token
	proc, _ := syscall.GetCurrentProcess()
	if err := syscall.OpenProcessToken(proc, syscall.TOKEN_QUERY, &token); err != nil {
		return false
	}
	defer token.Close()

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

	ret, _, _ := procShellExecuteW.Call(
		0,
		uintptr(unsafe.Pointer(verb)),
		uintptr(unsafe.Pointer(file)),
		uintptr(unsafe.Pointer(params)),
		uintptr(unsafe.Pointer(dir)),
		1, // SW_SHOWNORMAL
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
		dataDir = serviceDataDir()
	}
	dataDir, err = filepath.Abs(dataDir)
	if err != nil {
		return fmt.Errorf("cannot resolve data dir: %w", err)
	}
	if err := os.MkdirAll(dataDir, 0700); err != nil {
		return fmt.Errorf("cannot create data dir: %w", err)
	}

	binPath := fmt.Sprintf(`"%s" --port %d --data "%s"`, binaryPath, *flagPort, dataDir)
	if *flagAPIKey != "" {
		binPath += fmt.Sprintf(` --key "%s"`, *flagAPIKey)
	}

	run("sc", "stop", serviceName)
	run("sc", "delete", serviceName)

	if out, err := exec.Command("sc", "create", serviceName,
		"binPath="+binPath,
		"start=auto",
		"DisplayName="+serviceDisplayName,
	).CombinedOutput(); err != nil {
		return fmt.Errorf("sc create failed: %v\n%s", err, out)
	}

	if out, err := exec.Command("sc", "description", serviceName, serviceDescription).CombinedOutput(); err != nil {
		fmt.Printf("warning: sc description: %v — %s\n", err, out)
	}

	exec.Command("sc", "failure", serviceName,
		"reset=86400",
		"actions=restart/5000/restart/5000/restart/30000",
	).Run()

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

func run(name string, args ...string) {
	_ = exec.Command(name, args...).Run()
}
