// Package core provides the WhatsApp engine implementation.
// This file handles message sending operations.
//
// MESSAGE SENDING:
// - All send operations validate connection state first
// - Failed sends emit message.failed events
// - Successful sends emit message.sent events
// - Media is uploaded to WhatsApp servers before sending
//
// MEDIA HANDLING:
// - Supports URL and base64 encoded sources
// - Automatically downloads from URL if provided
// - Uses WhatsMeow's upload API for encryption
// - Cleans up temporary files after upload
package core

import (
	"context"
	"encoding/base64"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/types"
	waProto "go.mau.fi/whatsmeow/binary/proto"
	"google.golang.org/protobuf/proto"
)

// Sender handles all message sending operations.
// Thread-safe and validates connection state before operations.
type Sender struct {
	mu         sync.Mutex
	client     *Client
	eventQueue *EventQueue
	timeout    time.Duration
	httpClient *http.Client
}

// NewSender creates a new Sender instance.
func NewSender(client *Client, eventQueue *EventQueue) *Sender {
	return &Sender{
		client:     client,
		eventQueue: eventQueue,
		timeout:    30 * time.Second,
		httpClient: &http.Client{
			Timeout: 60 * time.Second,
		},
	}
}

// SendText sends a text message to the specified JID.
// Emits message.sent on success, message.failed on error.
func (s *Sender) SendText(jidStr string, text string) (string, error) {
	if !s.client.IsLoggedIn() {
		s.emitSendFailed(jidStr, text, ErrNotConnected)
		return "", ErrNotConnected
	}

	// Parse JID
	jid, err := types.ParseJID(jidStr)
	if err != nil {
		engineErr := WrapError(ErrCodeInvalidJID, "Invalid JID format", err)
		s.emitSendFailed(jidStr, text, engineErr)
		return "", engineErr
	}

	// Create message
	msg := &waProto.Message{
		Conversation: proto.String(text),
	}

	// Send message
	ctx, cancel := context.WithTimeout(context.Background(), s.timeout)
	defer cancel()

	resp, err := s.client.GetClient().SendMessage(ctx, jid, msg)
	if err != nil {
		engineErr := WrapError(ErrCodeSendFailed, "Failed to send message", err)
		s.emitSendFailed(jidStr, text, engineErr)
		return "", engineErr
	}

	// Emit sent event
	s.eventQueue.Push(NewMessageSentEvent(resp.ID, jidStr, text))

	return resp.ID, nil
}

// emitSendFailed emits a message.failed event.
func (s *Sender) emitSendFailed(to, text string, err *EngineError) {
	s.eventQueue.Push(NewMessageFailedEvent(to, text, err.Message, err.Code))
}

// SendTextReply sends a text message as a reply to another message.
func (s *Sender) SendTextReply(jidStr string, text string, quotedID string) (string, error) {
	if !s.client.IsLoggedIn() {
		s.emitSendFailed(jidStr, text, ErrNotConnected)
		return "", ErrNotConnected
	}

	// Parse JID
	jid, err := types.ParseJID(jidStr)
	if err != nil {
		engineErr := WrapError(ErrCodeInvalidJID, "Invalid JID format", err)
		s.emitSendFailed(jidStr, text, engineErr)
		return "", engineErr
	}

	// Create message with context info for reply
	msg := &waProto.Message{
		ExtendedTextMessage: &waProto.ExtendedTextMessage{
			Text: proto.String(text),
			ContextInfo: &waProto.ContextInfo{
				StanzaID:    proto.String(quotedID),
				Participant: proto.String(jidStr),
			},
		},
	}

	// Send message
	ctx, cancel := context.WithTimeout(context.Background(), s.timeout)
	defer cancel()

	resp, err := s.client.GetClient().SendMessage(ctx, jid, msg)
	if err != nil {
		engineErr := WrapError(ErrCodeSendFailed, "Failed to send reply", err)
		s.emitSendFailed(jidStr, text, engineErr)
		return "", engineErr
	}

	// Emit sent event
	s.eventQueue.Push(NewMessageSentEvent(resp.ID, jidStr, text))

	return resp.ID, nil
}

// SendPresence sends a presence update (composing, paused, available, unavailable).
func (s *Sender) SendPresence(presence types.Presence) error {
	if !s.client.IsLoggedIn() {
		return ErrNotConnected
	}

	return s.client.GetClient().SendPresence(context.Background(), presence)
}

// SendChatPresence sends a chat presence update (typing indicator).
func (s *Sender) SendChatPresence(jidStr string, state types.ChatPresence, media types.ChatPresenceMedia) error {
	if !s.client.IsLoggedIn() {
		return ErrNotConnected
	}

	// Parse JID
	jid, err := types.ParseJID(jidStr)
	if err != nil {
		return WrapError(ErrCodeInvalidJID, "Invalid JID format", err)
	}

	return s.client.GetClient().SendChatPresence(context.Background(), jid, state, media)
}

// SendReadReceipt marks a message as read.
func (s *Sender) SendReadReceipt(chatJIDStr string, senderJIDStr string, messageIDs []string) error {
	if !s.client.IsLoggedIn() {
		return ErrNotConnected
	}

	// Parse chat JID
	chatJID, err := types.ParseJID(chatJIDStr)
	if err != nil {
		return WrapError(ErrCodeInvalidJID, "Invalid chat JID format", err)
	}

	// Parse sender JID
	senderJID, err := types.ParseJID(senderJIDStr)
	if err != nil {
		return WrapError(ErrCodeInvalidJID, "Invalid sender JID format", err)
	}

	// Convert string message IDs to types.MessageID
	msgIDs := make([]types.MessageID, len(messageIDs))
	for i, id := range messageIDs {
		msgIDs[i] = types.MessageID(id)
	}

	return s.client.GetClient().MarkRead(context.Background(), msgIDs, time.Now(), chatJID, senderJID)
}

// SetSendTimeout sets the timeout for send operations.
func (s *Sender) SetSendTimeout(timeout time.Duration) {
	s.timeout = timeout
}

// ----- Media Sending Methods -----

// SendImageWithCaption sends an image with optional caption.
// imageSource can be:
// - A URL starting with "http://" or "https://"
// - Base64 encoded image data (with or without data URI prefix)
func (s *Sender) SendImageWithCaption(jidStr string, imageSource string, caption string) (string, error) {
	if !s.client.IsLoggedIn() {
		return "", ErrNotConnected
	}

	// Parse JID
	jid, err := types.ParseJID(jidStr)
	if err != nil {
		return "", WrapError(ErrCodeInvalidJID, "Invalid JID format", err)
	}

	// Get image data
	imageData, mimeType, err := s.resolveMediaSource(imageSource)
	if err != nil {
		return "", err
	}

	// Default mime type for images
	if mimeType == "" {
		mimeType = "image/jpeg"
	}

	// Upload to WhatsApp
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	uploaded, err := s.client.GetClient().Upload(ctx, imageData, whatsmeow.MediaImage)
	if err != nil {
		return "", WrapError(ErrCodeMediaFailed, "Failed to upload image", err)
	}

	// Create image message
	msg := &waProto.Message{
		ImageMessage: &waProto.ImageMessage{
			Caption:       proto.String(caption),
			Mimetype:      proto.String(mimeType),
			URL:           proto.String(uploaded.URL),
			DirectPath:    proto.String(uploaded.DirectPath),
			MediaKey:      uploaded.MediaKey,
			FileEncSHA256: uploaded.FileEncSHA256,
			FileSHA256:    uploaded.FileSHA256,
			FileLength:    proto.Uint64(uint64(len(imageData))),
		},
	}

	// Send message
	sendCtx, sendCancel := context.WithTimeout(context.Background(), s.timeout)
	defer sendCancel()

	resp, err := s.client.GetClient().SendMessage(sendCtx, jid, msg)
	if err != nil {
		return "", WrapError(ErrCodeSendFailed, "Failed to send image", err)
	}

	// Emit sent event
	s.eventQueue.Push(NewMessageSentEvent(resp.ID, jidStr, "[image] "+caption))

	return resp.ID, nil
}

// resolveMediaSource resolves an image source (URL or base64) to raw bytes.
func (s *Sender) resolveMediaSource(source string) ([]byte, string, error) {
	// Check if it's a URL
	if strings.HasPrefix(source, "http://") || strings.HasPrefix(source, "https://") {
		return s.downloadMedia(source)
	}

	// Check if it's a data URI
	if strings.HasPrefix(source, "data:") {
		return s.decodeDataURI(source)
	}

	// Assume it's raw base64
	data, err := base64.StdEncoding.DecodeString(source)
	if err != nil {
		return nil, "", WrapError(ErrCodeMediaFailed, "Invalid base64 data", err)
	}

	return data, "", nil
}

// downloadMedia downloads media from a URL.
func (s *Sender) downloadMedia(url string) ([]byte, string, error) {
	resp, err := s.httpClient.Get(url)
	if err != nil {
		return nil, "", WrapError(ErrCodeMediaDownloadFailed, "Failed to download media", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, "", NewErrorWithDetails(ErrCodeMediaDownloadFailed, "Download failed", resp.Status)
	}

	// Check content length to prevent huge downloads
	if resp.ContentLength > 50*1024*1024 { // 50MB limit
		return nil, "", NewError(ErrCodeMediaTooLarge, "Media file too large (max 50MB)")
	}

	data, err := io.ReadAll(io.LimitReader(resp.Body, 50*1024*1024))
	if err != nil {
		return nil, "", WrapError(ErrCodeMediaDownloadFailed, "Failed to read media data", err)
	}

	mimeType := resp.Header.Get("Content-Type")
	return data, mimeType, nil
}

// decodeDataURI decodes a data URI (e.g., "data:image/jpeg;base64,...")
func (s *Sender) decodeDataURI(dataURI string) ([]byte, string, error) {
	// Format: data:[<mediatype>][;base64],<data>
	parts := strings.SplitN(dataURI, ",", 2)
	if len(parts) != 2 {
		return nil, "", NewError(ErrCodeMediaFailed, "Invalid data URI format")
	}

	header := parts[0] // e.g., "data:image/jpeg;base64"
	encoded := parts[1]

	// Extract mime type
	mimeType := ""
	if strings.HasPrefix(header, "data:") {
		headerParts := strings.Split(header[5:], ";")
		if len(headerParts) > 0 && headerParts[0] != "" {
			mimeType = headerParts[0]
		}
	}

	// Decode base64
	data, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, "", WrapError(ErrCodeMediaFailed, "Failed to decode base64", err)
	}

	return data, mimeType, nil
}

// ----- Placeholder methods for future media types -----

// SendImage sends an image message.
func (s *Sender) SendImage(jidStr string, data []byte, caption string, mimeType string) (string, error) {
	if !s.client.IsLoggedIn() {
		return "", ErrNotConnected
	}

	jid, err := types.ParseJID(jidStr)
	if err != nil {
		return "", WrapError(ErrCodeInvalidJID, "Invalid JID format", err)
	}

	if mimeType == "" {
		mimeType = "image/jpeg"
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	uploaded, err := s.client.GetClient().Upload(ctx, data, whatsmeow.MediaImage)
	if err != nil {
		return "", WrapError(ErrCodeMediaFailed, "Failed to upload image", err)
	}

	msg := &waProto.Message{
		ImageMessage: &waProto.ImageMessage{
			Caption:       proto.String(caption),
			Mimetype:      proto.String(mimeType),
			URL:           proto.String(uploaded.URL),
			DirectPath:    proto.String(uploaded.DirectPath),
			MediaKey:      uploaded.MediaKey,
			FileEncSHA256: uploaded.FileEncSHA256,
			FileSHA256:    uploaded.FileSHA256,
			FileLength:    proto.Uint64(uint64(len(data))),
		},
	}

	sendCtx, sendCancel := context.WithTimeout(context.Background(), s.timeout)
	defer sendCancel()

	resp, err := s.client.GetClient().SendMessage(sendCtx, jid, msg)
	if err != nil {
		return "", WrapError(ErrCodeSendFailed, "Failed to send image", err)
	}

	s.eventQueue.Push(NewMessageSentEvent(resp.ID, jidStr, "[image] "+caption))
	return resp.ID, nil
}

// SendVideo sends a video message.
// Note: Implementation pending - needs video thumbnail generation.
func (s *Sender) SendVideo(jidStr string, data []byte, caption string, mimeType string) (string, error) {
	return "", NewError(ErrCodeInternal, "Video sending not yet implemented")
}

// SendDocument sends a document message.
func (s *Sender) SendDocument(jidStr string, data []byte, filename string, mimeType string) (string, error) {
	if !s.client.IsLoggedIn() {
		return "", ErrNotConnected
	}

	jid, err := types.ParseJID(jidStr)
	if err != nil {
		return "", WrapError(ErrCodeInvalidJID, "Invalid JID format", err)
	}

	if mimeType == "" {
		mimeType = "application/octet-stream"
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	uploaded, err := s.client.GetClient().Upload(ctx, data, whatsmeow.MediaDocument)
	if err != nil {
		return "", WrapError(ErrCodeMediaFailed, "Failed to upload document", err)
	}

	msg := &waProto.Message{
		DocumentMessage: &waProto.DocumentMessage{
			Title:         proto.String(filename),
			FileName:      proto.String(filename),
			Mimetype:      proto.String(mimeType),
			URL:           proto.String(uploaded.URL),
			DirectPath:    proto.String(uploaded.DirectPath),
			MediaKey:      uploaded.MediaKey,
			FileEncSHA256: uploaded.FileEncSHA256,
			FileSHA256:    uploaded.FileSHA256,
			FileLength:    proto.Uint64(uint64(len(data))),
		},
	}

	sendCtx, sendCancel := context.WithTimeout(context.Background(), s.timeout)
	defer sendCancel()

	resp, err := s.client.GetClient().SendMessage(sendCtx, jid, msg)
	if err != nil {
		return "", WrapError(ErrCodeSendFailed, "Failed to send document", err)
	}

	s.eventQueue.Push(NewMessageSentEvent(resp.ID, jidStr, "[document] "+filename))
	return resp.ID, nil
}

// SendAudio sends an audio message.
// Note: Implementation pending - needs audio duration detection.
func (s *Sender) SendAudio(jidStr string, data []byte, mimeType string, ptt bool) (string, error) {
	return "", NewError(ErrCodeInternal, "Audio sending not yet implemented")
}

// SendSticker sends a sticker message.
// Note: Implementation pending - needs sticker format validation.
func (s *Sender) SendSticker(jidStr string, data []byte) (string, error) {
	return "", NewError(ErrCodeInternal, "Sticker sending not yet implemented")
}

// SendLocation sends a location message.
// Note: This is a placeholder for future implementation.
func (s *Sender) SendLocation(jidStr string, latitude, longitude float64, name, address string) (string, error) {
	if !s.client.IsLoggedIn() {
		return "", ErrNotLoggedIn
	}

	// Parse JID
	jid, err := types.ParseJID(jidStr)
	if err != nil {
		return "", WrapError(ErrCodeInvalidJID, "Invalid JID format", err)
	}

	// Create location message
	msg := &waProto.Message{
		LocationMessage: &waProto.LocationMessage{
			DegreesLatitude:  proto.Float64(latitude),
			DegreesLongitude: proto.Float64(longitude),
			Name:             proto.String(name),
			Address:          proto.String(address),
		},
	}

	// Send message
	ctx, cancel := context.WithTimeout(context.Background(), s.timeout)
	defer cancel()

	resp, err := s.client.GetClient().SendMessage(ctx, jid, msg)
	if err != nil {
		return "", WrapError(ErrCodeSendFailed, "Failed to send location", err)
	}

	return resp.ID, nil
}

// SendContact sends a contact card message.
// Note: This is a placeholder for future implementation.
func (s *Sender) SendContact(jidStr string, displayName string, vcard string) (string, error) {
	if !s.client.IsLoggedIn() {
		return "", ErrNotLoggedIn
	}

	// Parse JID
	jid, err := types.ParseJID(jidStr)
	if err != nil {
		return "", WrapError(ErrCodeInvalidJID, "Invalid JID format", err)
	}

	// Create contact message
	msg := &waProto.Message{
		ContactMessage: &waProto.ContactMessage{
			DisplayName: proto.String(displayName),
			Vcard:       proto.String(vcard),
		},
	}

	// Send message
	ctx, cancel := context.WithTimeout(context.Background(), s.timeout)
	defer cancel()

	resp, err := s.client.GetClient().SendMessage(ctx, jid, msg)
	if err != nil {
		return "", WrapError(ErrCodeSendFailed, "Failed to send contact", err)
	}

	return resp.ID, nil
}

// ----- Utility functions -----

// FormatPhoneToJID converts a phone number to a WhatsApp JID.
// Phone number should include country code without '+' or '00' prefix.
// Example: "1234567890" -> "1234567890@s.whatsapp.net"
func FormatPhoneToJID(phone string) string {
	return phone + "@s.whatsapp.net"
}

// FormatGroupToJID converts a group ID to a WhatsApp group JID.
// Example: "1234567890-1234567890" -> "1234567890-1234567890@g.us"
func FormatGroupToJID(groupID string) string {
	return groupID + "@g.us"
}

// IsValidJID checks if a JID string is valid.
func IsValidJID(jidStr string) bool {
	_, err := types.ParseJID(jidStr)
	return err == nil
}

// NormalizeJID normalizes a JID string.
func NormalizeJID(jidStr string) (string, error) {
	jid, err := types.ParseJID(jidStr)
	if err != nil {
		return "", err
	}
	return jid.String(), nil
}
