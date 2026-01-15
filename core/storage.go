// Package core provides the WhatsApp engine implementation.
// This file handles session persistence and storage operations.
//
// STORAGE DESIGN:
// - Uses SQLite via whatsmeow's sqlstore for session persistence
// - Session survives app kills and restarts
// - Thread-safe for concurrent access
// - Automatic recovery from corrupted state
//
// MOBILE LIFECYCLE:
// - Initialize() can be called multiple times safely (idempotent)
// - Session persists across process death
// - No explicit Close() needed (SQLite handles this)
package core

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"go.mau.fi/whatsmeow/store"
	"go.mau.fi/whatsmeow/store/sqlstore"
	waLog "go.mau.fi/whatsmeow/util/log"

	_ "github.com/mattn/go-sqlite3" // SQLite driver
)

// Storage handles session persistence for WhatsApp authentication.
// It uses SQLite for storing device and session information.
// Thread-safe and idempotent across app restarts.
type Storage struct {
	mu          sync.RWMutex
	dataDir     string
	container   *sqlstore.Container
	device      *store.Device
	logger      waLog.Logger
	initialized bool
}

// NewStorage creates a new storage instance with the specified data directory.
// This does NOT initialize the database - call Initialize() for that.
func NewStorage(dataDir string, logger waLog.Logger) (*Storage, error) {
	if dataDir == "" {
		return nil, NewError(ErrCodeStorageFailed, "Data directory is required")
	}

	// Ensure data directory exists
	if err := os.MkdirAll(dataDir, 0700); err != nil {
		return nil, WrapError(ErrCodeStorageFailed, "Failed to create data directory", err)
	}

	return &Storage{
		dataDir: dataDir,
		logger:  logger,
	}, nil
}

// Initialize sets up the SQLite database and loads or creates a device.
// This method is IDEMPOTENT - safe to call multiple times.
// On app restart, this will load the existing session from SQLite.
func (s *Storage) Initialize() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Already initialized - return success (idempotent)
	if s.initialized && s.container != nil && s.device != nil {
		return nil
	}

	dbPath := filepath.Join(s.dataDir, "whatsapp.db")
	dbURI := fmt.Sprintf("file:%s?_foreign_keys=on&_busy_timeout=5000", dbPath)

	// Create SQL store container
	container, err := sqlstore.New(context.Background(), "sqlite3", dbURI, s.logger)
	if err != nil {
		// Check if DB is corrupted
		if isCorruptedDBError(err) {
			// Attempt recovery by removing corrupted DB
			if removeErr := s.removeCorruptedDB(dbPath); removeErr == nil {
				// Retry with fresh DB
				container, err = sqlstore.New(context.Background(), "sqlite3", dbURI, s.logger)
			}
		}
		if err != nil {
			return WrapError(ErrCodeStorageFailed, "Failed to create database", err)
		}
	}
	s.container = container

	// Get or create device - this loads existing session if available
	device, err := s.container.GetFirstDevice(context.Background())
	if err != nil {
		return WrapError(ErrCodeStorageFailed, "Failed to get device", err)
	}
	
	// Set platform to appear as Chrome on Ubuntu (like WhatsApp Web)
	// This makes the session appear as "Google Chrome (Ubuntu)" instead of "whatsmeow"
	if device != nil {
		device.Platform = "chrome_ubuntu"
	}
	
	s.device = device
	s.initialized = true

	return nil
}

// isCorruptedDBError checks if the error indicates database corruption.
func isCorruptedDBError(err error) bool {
	if err == nil {
		return false
	}
	errStr := err.Error()
	return contains(errStr, "malformed") ||
		contains(errStr, "corrupt") ||
		contains(errStr, "disk image")
}

// contains is a simple string contains check.
func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsAt(s, substr))
}

func containsAt(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

// removeCorruptedDB removes a corrupted database file.
func (s *Storage) removeCorruptedDB(dbPath string) error {
	// Remove main DB and any WAL/SHM files
	os.Remove(dbPath)
	os.Remove(dbPath + "-wal")
	os.Remove(dbPath + "-shm")
	return nil
}

// GetDevice returns the current device for WhatsApp connection.
// Returns nil if not initialized.
func (s *Storage) GetDevice() *store.Device {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.device
}

// GetContainer returns the SQL store container.
// Returns nil if not initialized.
func (s *Storage) GetContainer() *sqlstore.Container {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.container
}

// IsPaired checks if there is an existing authenticated session.
// This works across app restarts - checks SQLite state.
func (s *Storage) IsPaired() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	
	if s.device == nil {
		return false
	}
	return s.device.ID != nil
}

// HasSession is an alias for IsPaired for backward compatibility.
func (s *Storage) HasSession() bool {
	return s.IsPaired()
}

// GetJID returns the stored JID if paired, empty string otherwise.
func (s *Storage) GetJID() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	
	if s.device == nil || s.device.ID == nil {
		return ""
	}
	return s.device.ID.String()
}

// ClearSession removes all session data, forcing re-authentication.
// The device will need to be paired again via QR code.
func (s *Storage) ClearSession() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.container == nil {
		return nil
	}

	// Delete all devices
	devices, err := s.container.GetAllDevices(context.Background())
	if err != nil {
		return WrapError(ErrCodeStorageFailed, "Failed to get devices", err)
	}

	for _, device := range devices {
		if err := device.Delete(context.Background()); err != nil {
			return WrapError(ErrCodeStorageFailed, "Failed to delete device", err)
		}
	}

	// Reinitialize with a fresh device
	device, err := s.container.GetFirstDevice(context.Background())
	if err != nil {
		return WrapError(ErrCodeStorageFailed, "Failed to create new device", err)
	}
	s.device = device

	return nil
}

// Close closes the storage and releases resources.
// Safe to call multiple times (idempotent).
func (s *Storage) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	
	// SQLite connections are managed by the container
	// We just nil out our references
	s.container = nil
	s.device = nil
	s.initialized = false
	return nil
}

// GetDataDir returns the data directory path.
func (s *Storage) GetDataDir() string {
	return s.dataDir
}

// DatabasePath returns the full path to the database file.
func (s *Storage) DatabasePath() string {
	return filepath.Join(s.dataDir, "whatsapp.db")
}

// IsInitialized returns true if storage has been initialized.
func (s *Storage) IsInitialized() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.initialized
}

// Reinitialize forces re-initialization of storage.
// Use after ClearSession() to refresh the device.
func (s *Storage) Reinitialize() error {
	s.mu.Lock()
	s.initialized = false
	s.mu.Unlock()
	return s.Initialize()
}
