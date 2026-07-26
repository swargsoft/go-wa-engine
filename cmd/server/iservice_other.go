//go:build !windows

package main

func isWindowsServiceRun() bool { return false }
