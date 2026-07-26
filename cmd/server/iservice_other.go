//go:build !windows

package main

// checkWindowsService is a no-op on non-Windows platforms.
// On Windows, service_windows.go provides the real implementation.
func checkWindowsService() bool { return false }
