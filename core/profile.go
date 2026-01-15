package core

import (
	"context"
	"encoding/json"
	"fmt"

	"go.mau.fi/whatsmeow/types"
)

// ProfileInfo contains WhatsApp user profile information
type ProfileInfo struct {
	JID          string `json:"jid"`
	PushName     string `json:"push_name"`
	BusinessName string `json:"business_name"`
	Status       string `json:"status"`
	Avatar       string `json:"avatar"` // Base64 encoded image or URL
	IsVerified   bool   `json:"is_verified"`
	IsBusiness   bool   `json:"is_business"`
}

// GetUserProfile fetches profile information for a JID
func (s *Session) GetUserProfile(jid string) (string, error) {
	if !s.IsConnected() {
		return "", NewError(ErrCodeNotConnected, "Session not connected")
	}

	parsedJID, err := types.ParseJID(jid)
	if err != nil {
		return "", WrapError(ErrCodeInvalidInput, "Invalid JID", err)
	}

	client := s.client.GetClient()
	
	profile := ProfileInfo{
		JID: jid,
	}

	// Get user info from store
	info, err := client.Store.Contacts.GetContact(parsedJID)
	if err == nil && info != nil {
		profile.PushName = info.PushName
		profile.BusinessName = info.BusinessName
	}

	// Get profile picture
	pic, err := client.GetProfilePictureInfo(context.Background(), parsedJID, nil)
	if err == nil && pic != nil {
		profile.Avatar = pic.URL
	}

	// Get status/about
	// Note: WhatsApp doesn't always allow fetching status of other users
	// This mainly works for your own JID
	// For other users, you'll need to use GetStatusPrivacy or similar methods

	result, err := json.Marshal(profile)
	if err != nil {
		return "", WrapError(ErrCodeEncodingFailed, "Failed to encode profile", err)
	}

	return string(result), nil
}

// GetOwnProfile gets the profile of the logged-in user
func (s *Session) GetOwnProfile() (string, error) {
	if !s.IsConnected() {
		return "", NewError(ErrCodeNotConnected, "Session not connected")
	}

	jid := s.client.GetClient().Store.ID
	if jid == nil {
		return "", NewError(ErrCodeNotInitialized, "User JID not available")
	}

	return s.GetUserProfile(jid.String())
}
