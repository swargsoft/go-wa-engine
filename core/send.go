// Package core provides the WhatsApp engine implementation.
// This file handles message sending with anti-ban protections.
//
// ANTI-BAN CHANGES vs original:
//   - NewSenderWithAntiBlock() wires in RateLimiter + PresenceManager
//   - SendText() now calls limiter.Wait() + presence.BeforeSend() + AfterSend()
//   - SetRateLimiter() / SetPresenceManager() allow runtime reconfiguration
//   - All other methods (media, receipts, etc.) unchanged
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

// Sender handles all message sending with anti-ban protections.
type Sender struct {
	mu         sync.RWMutex
	client     *Client
	eventQueue *EventQueue
	timeout    time.Duration
	httpClient *http.Client

	// Anti-ban: set via NewSenderWithAntiBlock or SetRateLimiter/SetPresenceManager
	limiter  *RateLimiter    // may be nil (disables rate limiting)
	presence *PresenceManager // may be nil (disables typing simulation)
}

// NewSender creates a Sender without anti-ban features (legacy / testing).
func NewSender(client *Client, eventQueue *EventQueue) *Sender {
	return &Sender{
		client:     client,
		eventQueue: eventQueue,
		timeout:    30 * time.Second,
		httpClient: &http.Client{Timeout: 60 * time.Second},
	}
}

// NewSenderWithAntiBlock creates a Sender with rate limiting and presence hooks.
// Use this in production.
func NewSenderWithAntiBlock(
	client *Client,
	eventQueue *EventQueue,
	limiter *RateLimiter,
	presence *PresenceManager,
) *Sender {
	s := NewSender(client, eventQueue)
	s.limiter = limiter
	s.presence = presence
	return s
}

// SetRateLimiter replaces the rate limiter at runtime.
func (s *Sender) SetRateLimiter(l *RateLimiter) {
	s.mu.Lock()
	s.limiter = l
	s.mu.Unlock()
}

// SetPresenceManager replaces the presence manager at runtime.
func (s *Sender) SetPresenceManager(p *PresenceManager) {
	s.mu.Lock()
	s.presence = p
	s.mu.Unlock()
}

// --- Anti-ban helpers ---

func (s *Sender) getLimiterAndPresence() (*RateLimiter, *PresenceManager) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.limiter, s.presence
}

// waitBeforeSend applies rate limiting and typing simulation before a send.
// Returns a cleanup function that stops the typing indicator.
func (s *Sender) waitBeforeSend(jidStr string, textLen int) func() {
	limiter, presence := s.getLimiterAndPresence()

	// 1. Typing simulation (includes typing delay sleep)
	var stopTyping func()
	if presence != nil {
		stopTyping = presence.BeforeSend(jidStr, textLen)
	} else {
		stopTyping = func() {}
	}

	// 2. Rate limit wait (after typing delay, before actual send)
	if limiter != nil {
		limiter.Wait()
	}

	return stopTyping
}

// afterSend resets idle timer after a successful send.
func (s *Sender) afterSend() {
	_, presence := s.getLimiterAndPresence()
	if presence != nil {
		presence.AfterSend()
	}
}

// --- Core send methods ---

// SendText sends a text message to the specified JID.
func (s *Sender) SendText(jidStr string, text string) (string, error) {
	if !s.client.IsLoggedIn() {
		s.emitSendFailed(jidStr, text, ErrNotConnected)
		return "", ErrNotConnected
	}

	jid, err := types.ParseJID(jidStr)
	if err != nil {
		e := WrapError(ErrCodeInvalidJID, "Invalid JID format", err)
		s.emitSendFailed(jidStr, text, e)
		return "", e
	}

	// Anti-ban: type first, then rate-limit, then send
	stopTyping := s.waitBeforeSend(jidStr, len(text))
	defer stopTyping()

	msg := &waProto.Message{Conversation: proto.String(text)}

	ctx, cancel := context.WithTimeout(context.Background(), s.timeout)
	defer cancel()

	resp, err := s.client.GetClient().SendMessage(ctx, jid, msg)
	if err != nil {
		e := WrapError(ErrCodeSendFailed, "Failed to send message", err)
		s.emitSendFailed(jidStr, text, e)
		return "", e
	}

	s.afterSend()
	s.eventQueue.Push(NewMessageSentEvent(resp.ID, jidStr, text))
	return resp.ID, nil
}

// SendTextReply sends a text message as a reply to another message.
func (s *Sender) SendTextReply(jidStr string, text string, quotedID string) (string, error) {
	if !s.client.IsLoggedIn() {
		s.emitSendFailed(jidStr, text, ErrNotConnected)
		return "", ErrNotConnected
	}

	jid, err := types.ParseJID(jidStr)
	if err != nil {
		e := WrapError(ErrCodeInvalidJID, "Invalid JID format", err)
		s.emitSendFailed(jidStr, text, e)
		return "", e
	}

	stopTyping := s.waitBeforeSend(jidStr, len(text))
	defer stopTyping()

	msg := &waProto.Message{
		ExtendedTextMessage: &waProto.ExtendedTextMessage{
			Text: proto.String(text),
			ContextInfo: &waProto.ContextInfo{
				StanzaID:    proto.String(quotedID),
				Participant: proto.String(jidStr),
			},
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), s.timeout)
	defer cancel()

	resp, err := s.client.GetClient().SendMessage(ctx, jid, msg)
	if err != nil {
		e := WrapError(ErrCodeSendFailed, "Failed to send reply", err)
		s.emitSendFailed(jidStr, text, e)
		return "", e
	}

	s.afterSend()
	s.eventQueue.Push(NewMessageSentEvent(resp.ID, jidStr, text))
	return resp.ID, nil
}

// SendImageWithCaption sends an image with optional caption.
// imageSource: URL (http/https) or base64 / data URI.
func (s *Sender) SendImageWithCaption(jidStr string, imageSource string, caption string) (string, error) {
	if !s.client.IsLoggedIn() {
		return "", ErrNotConnected
	}

	jid, err := types.ParseJID(jidStr)
	if err != nil {
		return "", WrapError(ErrCodeInvalidJID, "Invalid JID format", err)
	}

	imageData, mimeType, err := s.resolveMediaSource(imageSource)
	if err != nil {
		return "", err
	}
	if mimeType == "" {
		mimeType = "image/jpeg"
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	uploaded, err := s.client.GetClient().Upload(ctx, imageData, whatsmeow.MediaImage)
	if err != nil {
		return "", WrapError(ErrCodeMediaFailed, "Failed to upload image", err)
	}

	// Anti-ban: rate limit image sends too (heavier operation, no typing sim)
	if limiter, _ := s.getLimiterAndPresence(); limiter != nil {
		limiter.Wait()
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
			FileLength:    proto.Uint64(uint64(len(imageData))),
		},
	}

	sendCtx, sendCancel := context.WithTimeout(context.Background(), s.timeout)
	defer sendCancel()

	resp, err := s.client.GetClient().SendMessage(sendCtx, jid, msg)
	if err != nil {
		return "", WrapError(ErrCodeSendFailed, "Failed to send image", err)
	}

	s.afterSend()
	s.eventQueue.Push(NewMessageSentEvent(resp.ID, jidStr, "[image] "+caption))
	return resp.ID, nil
}

// SendImage sends raw image bytes with caption.
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
	if limiter, _ := s.getLimiterAndPresence(); limiter != nil {
		limiter.Wait()
	}
	msg := &waProto.Message{
		ImageMessage: &waProto.ImageMessage{
			Caption: proto.String(caption), Mimetype: proto.String(mimeType),
			URL: proto.String(uploaded.URL), DirectPath: proto.String(uploaded.DirectPath),
			MediaKey: uploaded.MediaKey, FileEncSHA256: uploaded.FileEncSHA256,
			FileSHA256: uploaded.FileSHA256, FileLength: proto.Uint64(uint64(len(data))),
		},
	}
	sendCtx, sendCancel := context.WithTimeout(context.Background(), s.timeout)
	defer sendCancel()
	resp, err := s.client.GetClient().SendMessage(sendCtx, jid, msg)
	if err != nil {
		return "", WrapError(ErrCodeSendFailed, "Failed to send image", err)
	}
	s.afterSend()
	s.eventQueue.Push(NewMessageSentEvent(resp.ID, jidStr, "[image] "+caption))
	return resp.ID, nil
}

// SendDocument sends a document/file message.
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
	if limiter, _ := s.getLimiterAndPresence(); limiter != nil {
		limiter.Wait()
	}
	msg := &waProto.Message{
		DocumentMessage: &waProto.DocumentMessage{
			Title: proto.String(filename), FileName: proto.String(filename),
			Mimetype: proto.String(mimeType), URL: proto.String(uploaded.URL),
			DirectPath: proto.String(uploaded.DirectPath), MediaKey: uploaded.MediaKey,
			FileEncSHA256: uploaded.FileEncSHA256, FileSHA256: uploaded.FileSHA256,
			FileLength: proto.Uint64(uint64(len(data))),
		},
	}
	sendCtx, sendCancel := context.WithTimeout(context.Background(), s.timeout)
	defer sendCancel()
	resp, err := s.client.GetClient().SendMessage(sendCtx, jid, msg)
	if err != nil {
		return "", WrapError(ErrCodeSendFailed, "Failed to send document", err)
	}
	s.afterSend()
	s.eventQueue.Push(NewMessageSentEvent(resp.ID, jidStr, "[document] "+filename))
	return resp.ID, nil
}

// SendLocation sends a location message.
func (s *Sender) SendLocation(jidStr string, latitude, longitude float64, name, address string) (string, error) {
	if !s.client.IsLoggedIn() {
		return "", ErrNotLoggedIn
	}
	jid, err := types.ParseJID(jidStr)
	if err != nil {
		return "", WrapError(ErrCodeInvalidJID, "Invalid JID format", err)
	}
	msg := &waProto.Message{
		LocationMessage: &waProto.LocationMessage{
			DegreesLatitude: proto.Float64(latitude), DegreesLongitude: proto.Float64(longitude),
			Name: proto.String(name), Address: proto.String(address),
		},
	}
	ctx, cancel := context.WithTimeout(context.Background(), s.timeout)
	defer cancel()
	resp, err := s.client.GetClient().SendMessage(ctx, jid, msg)
	if err != nil {
		return "", WrapError(ErrCodeSendFailed, "Failed to send location", err)
	}
	return resp.ID, nil
}

// SendContact sends a contact card message.
func (s *Sender) SendContact(jidStr string, displayName string, vcard string) (string, error) {
	if !s.client.IsLoggedIn() {
		return "", ErrNotLoggedIn
	}
	jid, err := types.ParseJID(jidStr)
	if err != nil {
		return "", WrapError(ErrCodeInvalidJID, "Invalid JID format", err)
	}
	msg := &waProto.Message{
		ContactMessage: &waProto.ContactMessage{
			DisplayName: proto.String(displayName),
			Vcard:       proto.String(vcard),
		},
	}
	ctx, cancel := context.WithTimeout(context.Background(), s.timeout)
	defer cancel()
	resp, err := s.client.GetClient().SendMessage(ctx, jid, msg)
	if err != nil {
		return "", WrapError(ErrCodeSendFailed, "Failed to send contact", err)
	}
	return resp.ID, nil
}

// SendVideo sends a video message with optional caption.
func (s *Sender) SendVideo(jidStr string, data []byte, caption string, mimeType string) (string, error) {
	if !s.client.IsLoggedIn() {
		return "", ErrNotConnected
	}
	jid, err := types.ParseJID(jidStr)
	if err != nil {
		return "", WrapError(ErrCodeInvalidJID, "Invalid JID format", err)
	}
	if mimeType == "" {
		mimeType = "video/mp4"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	uploaded, err := s.client.GetClient().Upload(ctx, data, whatsmeow.MediaVideo)
	if err != nil {
		return "", WrapError(ErrCodeMediaFailed, "Failed to upload video", err)
	}
	if limiter, _ := s.getLimiterAndPresence(); limiter != nil {
		limiter.Wait()
	}
	msg := &waProto.Message{
		VideoMessage: &waProto.VideoMessage{
			Caption: proto.String(caption), Mimetype: proto.String(mimeType),
			URL: proto.String(uploaded.URL), DirectPath: proto.String(uploaded.DirectPath),
			MediaKey: uploaded.MediaKey, FileEncSHA256: uploaded.FileEncSHA256,
			FileSHA256: uploaded.FileSHA256, FileLength: proto.Uint64(uint64(len(data))),
		},
	}
	sendCtx, sendCancel := context.WithTimeout(context.Background(), s.timeout)
	defer sendCancel()
	resp, err := s.client.GetClient().SendMessage(sendCtx, jid, msg)
	if err != nil {
		return "", WrapError(ErrCodeSendFailed, "Failed to send video", err)
	}
	s.afterSend()
	s.eventQueue.Push(NewMessageSentEvent(resp.ID, jidStr, "[video] "+caption))
	return resp.ID, nil
}

// SendAudio sends an audio message.
// Set ptt=true for voice notes (push-to-talk), false for regular audio.
func (s *Sender) SendAudio(jidStr string, data []byte, mimeType string, ptt bool) (string, error) {
	if !s.client.IsLoggedIn() {
		return "", ErrNotConnected
	}
	jid, err := types.ParseJID(jidStr)
	if err != nil {
		return "", WrapError(ErrCodeInvalidJID, "Invalid JID format", err)
	}
	if mimeType == "" {
		if ptt {
			mimeType = "audio/ogg; codecs=opus"
		} else {
			mimeType = "audio/mpeg"
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	uploaded, err := s.client.GetClient().Upload(ctx, data, whatsmeow.MediaAudio)
	if err != nil {
		return "", WrapError(ErrCodeMediaFailed, "Failed to upload audio", err)
	}
	if limiter, _ := s.getLimiterAndPresence(); limiter != nil {
		limiter.Wait()
	}
	msg := &waProto.Message{
		AudioMessage: &waProto.AudioMessage{
			Mimetype: proto.String(mimeType), PTT: proto.Bool(ptt),
			URL: proto.String(uploaded.URL), DirectPath: proto.String(uploaded.DirectPath),
			MediaKey: uploaded.MediaKey, FileEncSHA256: uploaded.FileEncSHA256,
			FileSHA256: uploaded.FileSHA256, FileLength: proto.Uint64(uint64(len(data))),
		},
	}
	sendCtx, sendCancel := context.WithTimeout(context.Background(), s.timeout)
	defer sendCancel()
	resp, err := s.client.GetClient().SendMessage(sendCtx, jid, msg)
	if err != nil {
		return "", WrapError(ErrCodeSendFailed, "Failed to send audio", err)
	}
	s.afterSend()
	s.eventQueue.Push(NewMessageSentEvent(resp.ID, jidStr, "[audio]"))
	return resp.ID, nil
}

// SendSticker - stub, pending implementation.
func (s *Sender) SendSticker(jidStr string, data []byte) (string, error) {
	return "", NewError(ErrCodeInternal, "Sticker sending not yet implemented")
}

// --- Presence / Receipt helpers ---

// SendPresence sends a presence update (available / unavailable).
func (s *Sender) SendPresence(presence types.Presence) error {
	if !s.client.IsLoggedIn() {
		return ErrNotConnected
	}
	return s.client.GetClient().SendPresence(context.Background(), presence)
}

// SendChatPresence sends a typing indicator to a chat.
// state: "composing" or "paused"  |  media: "" or "audio"
func (s *Sender) SendChatPresence(jidStr string, state string, media string) error {
	if !s.client.IsLoggedIn() {
		return ErrNotConnected
	}
	jid, err := types.ParseJID(jidStr)
	if err != nil {
		return WrapError(ErrCodeInvalidJID, "Invalid JID format", err)
	}
	chatState := types.ChatPresenceComposing
	if state == "paused" {
		chatState = types.ChatPresencePaused
	}
	chatMedia := types.ChatPresenceMediaText
	if media == "audio" {
		chatMedia = types.ChatPresenceMediaAudio
	}
	return s.client.GetClient().SendChatPresence(context.Background(), jid, chatState, chatMedia)
}

// SendReadReceipt marks messages as read.
func (s *Sender) SendReadReceipt(chatJIDStr string, senderJIDStr string, messageIDs []string) error {
	if !s.client.IsLoggedIn() {
		return ErrNotConnected
	}
	chatJID, err := types.ParseJID(chatJIDStr)
	if err != nil {
		return WrapError(ErrCodeInvalidJID, "Invalid chat JID format", err)
	}
	senderJID, err := types.ParseJID(senderJIDStr)
	if err != nil {
		return WrapError(ErrCodeInvalidJID, "Invalid sender JID format", err)
	}
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

// emitSendFailed emits a message.failed event.
func (s *Sender) emitSendFailed(to, text string, err *EngineError) {
	s.eventQueue.Push(NewMessageFailedEvent(to, text, err.Message, err.Code))
}

// --- Media resolution helpers ---

func (s *Sender) resolveMediaSource(source string) ([]byte, string, error) {
	if strings.HasPrefix(source, "http://") || strings.HasPrefix(source, "https://") {
		return s.downloadMedia(source)
	}
	if strings.HasPrefix(source, "data:") {
		return s.decodeDataURI(source)
	}
	data, err := base64.StdEncoding.DecodeString(source)
	if err != nil {
		return nil, "", WrapError(ErrCodeMediaFailed, "Invalid base64 data", err)
	}
	return data, "", nil
}

func (s *Sender) downloadMedia(url string) ([]byte, string, error) {
	resp, err := s.httpClient.Get(url)
	if err != nil {
		return nil, "", WrapError(ErrCodeMediaDownloadFailed, "Failed to download media", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, "", NewErrorWithDetails(ErrCodeMediaDownloadFailed, "Download failed", resp.Status)
	}
	const maxSize = 50 * 1024 * 1024 // 50MB
	if resp.ContentLength > maxSize {
		return nil, "", NewError(ErrCodeMediaTooLarge, "Media file too large (max 50MB)")
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxSize))
	if err != nil {
		return nil, "", WrapError(ErrCodeMediaDownloadFailed, "Failed to read media data", err)
	}
	return data, resp.Header.Get("Content-Type"), nil
}

func (s *Sender) decodeDataURI(dataURI string) ([]byte, string, error) {
	parts := strings.SplitN(dataURI, ",", 2)
	if len(parts) != 2 {
		return nil, "", NewError(ErrCodeMediaFailed, "Invalid data URI format")
	}
	mimeType := ""
	if strings.HasPrefix(parts[0], "data:") {
		headerParts := strings.Split(parts[0][5:], ";")
		if len(headerParts) > 0 {
			mimeType = headerParts[0]
		}
	}
	data, err := base64.StdEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, "", WrapError(ErrCodeMediaFailed, "Failed to decode base64", err)
	}
	return data, mimeType, nil
}

// --- JID utilities ---

func FormatPhoneToJID(phone string) string  { return phone + "@s.whatsapp.net" }
func FormatGroupToJID(groupID string) string { return groupID + "@g.us" }
func IsValidJID(jidStr string) bool {
	_, err := types.ParseJID(jidStr)
	return err == nil
}
func NormalizeJID(jidStr string) (string, error) {
	jid, err := types.ParseJID(jidStr)
	if err != nil {
		return "", err
	}
	return jid.String(), nil
}
