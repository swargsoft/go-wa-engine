// Package core provides the WhatsApp engine implementation.
// This file defines error types and error handling utilities.
//
// ERROR CODE CONTRACT (STABLE - DO NOT CHANGE EXISTING CODES):
// These error codes are part of the public API contract.
// Mobile apps depend on these exact strings for error handling.
// Adding new codes is allowed; changing existing codes is NOT.
package core

import (
	"encoding/json"
	"fmt"
)

// ErrorCode represents categorized error types for the SDK.
// These codes are stable and must not be changed once released.
type ErrorCode string

const (
	// ----- Initialization & Lifecycle Errors -----

	// ErrCodeNotInitialized indicates the engine has not been initialized.
	ErrCodeNotInitialized ErrorCode = "ERR_NOT_INITIALIZED"

	// ErrCodeAlreadyRunning indicates the engine is already running.
	ErrCodeAlreadyRunning ErrorCode = "ERR_ALREADY_RUNNING"

	// ErrCodeNotRunning indicates the engine is not running.
	ErrCodeNotRunning ErrorCode = "ERR_NOT_RUNNING"

	// ----- Authentication & Pairing Errors -----

	// ErrCodeNotPaired indicates no valid pairing/session exists.
	// This is distinct from NotConnected - the device was never paired.
	ErrCodeNotPaired ErrorCode = "ERR_NOT_PAIRED"

	// ErrCodeNotConnected indicates no active connection to WhatsApp servers.
	// The device may be paired but currently offline.
	ErrCodeNotConnected ErrorCode = "ERR_NOT_CONNECTED"

	// ErrCodeNotLoggedIn indicates no active WhatsApp session.
	// Deprecated: Use ErrCodeNotPaired or ErrCodeNotConnected instead.
	ErrCodeNotLoggedIn ErrorCode = "ERR_NOT_LOGGED_IN"

	// ErrCodeQRExpired indicates the QR code has expired and needs refresh.
	ErrCodeQRExpired ErrorCode = "ERR_QR_EXPIRED"

	// ErrCodePairingFailed indicates pairing attempt failed.
	ErrCodePairingFailed ErrorCode = "ERR_PAIRING_FAILED"

	// ErrCodeLoggedOut indicates the user was logged out by WhatsApp.
	// Session is invalidated and re-pairing is required.
	ErrCodeLoggedOut ErrorCode = "ERR_LOGGED_OUT"

	// ErrCodeSessionInvalid indicates the stored session is corrupted or invalid.
	ErrCodeSessionInvalid ErrorCode = "ERR_SESSION_INVALID"

	// ----- Connection Errors -----

	// ErrCodeConnectionFailed indicates a connection failure.
	ErrCodeConnectionFailed ErrorCode = "ERR_CONNECTION_FAILED"

	// ErrCodeNetworkUnavailable indicates no network connectivity.
	ErrCodeNetworkUnavailable ErrorCode = "ERR_NETWORK_UNAVAILABLE"

	// ErrCodeConnectionTimeout indicates connection attempt timed out.
	ErrCodeConnectionTimeout ErrorCode = "ERR_CONNECTION_TIMEOUT"

	// ----- Message & Send Errors -----

	// ErrCodeSendFailed indicates a message send failure.
	ErrCodeSendFailed ErrorCode = "ERR_SEND_FAILED"

	// ErrCodeInvalidJID indicates an invalid WhatsApp JID.
	ErrCodeInvalidJID ErrorCode = "ERR_INVALID_JID"

	// ErrCodeMessageTooLong indicates message exceeds size limit.
	ErrCodeMessageTooLong ErrorCode = "ERR_MESSAGE_TOO_LONG"

	// ----- Media Errors -----

	// ErrCodeMediaFailed indicates media processing/upload failed.
	ErrCodeMediaFailed ErrorCode = "ERR_MEDIA_FAILED"

	// ErrCodeMediaTooLarge indicates media file exceeds size limit.
	ErrCodeMediaTooLarge ErrorCode = "ERR_MEDIA_TOO_LARGE"

	// ErrCodeMediaInvalidType indicates unsupported media type.
	ErrCodeMediaInvalidType ErrorCode = "ERR_MEDIA_INVALID_TYPE"

	// ErrCodeMediaDownloadFailed indicates failed to download media from URL.
	ErrCodeMediaDownloadFailed ErrorCode = "ERR_MEDIA_DOWNLOAD_FAILED"

	// ----- Storage Errors -----

	// ErrCodeStorageFailed indicates a storage operation failure.
	ErrCodeStorageFailed ErrorCode = "ERR_STORAGE_FAILED"

	// ErrCodeStorageCorrupted indicates database is corrupted.
	ErrCodeStorageCorrupted ErrorCode = "ERR_STORAGE_CORRUPTED"

	// ----- Internal Errors -----

	// ErrCodeInternal indicates an internal error.
	ErrCodeInternal ErrorCode = "ERR_INTERNAL"

	// ErrCodeConcurrencyViolation indicates unsafe concurrent operation.
	ErrCodeConcurrencyViolation ErrorCode = "ERR_CONCURRENCY_VIOLATION"

	// ----- Multi-Session Errors -----

	// ErrCodeSessionNotFound indicates the specified session doesn't exist.
	ErrCodeSessionNotFound ErrorCode = "ERR_SESSION_NOT_FOUND"

	// ErrCodeMaxSessionsExceeded indicates the maximum session limit has been reached.
	ErrCodeMaxSessionsExceeded ErrorCode = "ERR_MAX_SESSIONS_EXCEEDED"

	// ErrCodeSessionAlreadyExists indicates attempting to create a duplicate session.
	ErrCodeSessionAlreadyExists ErrorCode = "ERR_SESSION_ALREADY_EXISTS"
)

// EngineError represents a structured error from the engine.
type EngineError struct {
	Code    ErrorCode `json:"code"`
	Message string    `json:"message"`
	Details string    `json:"details,omitempty"`
}

// Error implements the error interface.
func (e *EngineError) Error() string {
	if e.Details != "" {
		return fmt.Sprintf("[%s] %s: %s", e.Code, e.Message, e.Details)
	}
	return fmt.Sprintf("[%s] %s", e.Code, e.Message)
}

// ToJSON returns the error as a JSON string.
func (e *EngineError) ToJSON() string {
	data, _ := json.Marshal(e)
	return string(data)
}

// NewError creates a new EngineError.
func NewError(code ErrorCode, message string) *EngineError {
	return &EngineError{
		Code:    code,
		Message: message,
	}
}

// NewErrorWithDetails creates a new EngineError with additional details.
func NewErrorWithDetails(code ErrorCode, message, details string) *EngineError {
	return &EngineError{
		Code:    code,
		Message: message,
		Details: details,
	}
}

// WrapError wraps an existing error with an EngineError.
func WrapError(code ErrorCode, message string, err error) *EngineError {
	details := ""
	if err != nil {
		details = err.Error()
	}
	return &EngineError{
		Code:    code,
		Message: message,
		Details: details,
	}
}

// Common error instances for reuse.
// These are pre-allocated for performance and consistency.
var (
	ErrNotInitialized      = NewError(ErrCodeNotInitialized, "Engine not initialized")
	ErrAlreadyRunning      = NewError(ErrCodeAlreadyRunning, "Engine is already running")
	ErrNotRunning          = NewError(ErrCodeNotRunning, "Engine is not running")
	ErrNotLoggedIn         = NewError(ErrCodeNotLoggedIn, "Not logged in to WhatsApp")
	ErrNotPaired           = NewError(ErrCodeNotPaired, "Device not paired with WhatsApp")
	ErrNotConnected        = NewError(ErrCodeNotConnected, "Not connected to WhatsApp servers")
	ErrQRExpired           = NewError(ErrCodeQRExpired, "QR code expired")
	ErrMediaFailed         = NewError(ErrCodeMediaFailed, "Media operation failed")
	ErrSessionNotFound     = NewError(ErrCodeSessionNotFound, "Session not found")
	ErrMaxSessionsExceeded = NewError(ErrCodeMaxSessionsExceeded, "Maximum sessions exceeded")
	ErrStorageFailed       = NewError(ErrCodeStorageFailed, "Storage operation failed")
)
