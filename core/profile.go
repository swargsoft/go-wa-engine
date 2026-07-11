package core

import (
	"context"
	"encoding/json"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/types"
)

// ProfileInfo contains WhatsApp user profile information
type ProfileInfo struct {
	JID          string `json:"jid"`
	PushName     string `json:"push_name"`
	BusinessName string `json:"business_name"`
	Status       string `json:"status"`
	Avatar       string `json:"avatar"`
	IsVerified   bool   `json:"is_verified"`
	IsBusiness   bool   `json:"is_business"`
	BusinessAddr string `json:"business_address,omitempty"`
	BusinessCat  string `json:"business_category,omitempty"`
	BotName      string `json:"bot_name,omitempty"`
	IsBot        bool   `json:"is_bot"`
}

func fetchProfilePicture(client *whatsmeow.Client, jid types.JID) string {
	if client == nil {
		return ""
	}
	params := &whatsmeow.GetProfilePictureParams{Preview: true}
	pic, _ := client.GetProfilePictureInfo(context.Background(), jid, params)
	if pic != nil && pic.URL != "" {
		return pic.URL
	}
	params.Preview = false
	pic, _ = client.GetProfilePictureInfo(context.Background(), jid, params)
	if pic != nil && pic.URL != "" {
		return pic.URL
	}
	return ""
}

func fetchBusinessProfile(client *whatsmeow.Client, jid types.JID) *types.BusinessProfile {
	if client == nil {
		return nil
	}
	bp, err := client.GetBusinessProfile(context.Background(), jid)
	if err != nil {
		return nil
	}
	return bp
}

// GetOwnProfile gets the profile of the logged-in user
func (e *Engine) GetOwnProfile() (string, error) {
	if !e.IsConnected() {
		return "", NewError(ErrCodeNotConnected, "Engine not connected")
	}

	client := e.client.GetClient()
	if client.Store.ID == nil {
		return "", NewError(ErrCodeNotInitialized, "User JID not available")
	}

	jid := client.Store.ID.String()
	profile := ProfileInfo{
		JID:      jid,
		PushName: client.Store.PushName,
	}

	parsedJID, err := types.ParseJID(jid)
	if err == nil {
		profile.Avatar = fetchProfilePicture(client, parsedJID)
		bp := fetchBusinessProfile(client, parsedJID)
		if bp != nil {
			profile.BusinessAddr = bp.Address
			if len(bp.Categories) > 0 {
				profile.BusinessCat = bp.Categories[0].Name
				profile.IsBusiness = true
			}
		}
	}

	result, err := json.Marshal(profile)
	if err != nil {
		return "", WrapError(ErrCodeInternal, "Failed to encode profile", err)
	}

	return string(result), nil
}

// GetUserProfile fetches profile information for a given JID (other users)
func (e *Engine) GetUserProfile(jid string) (string, error) {
	if !e.IsConnected() {
		return "", NewError(ErrCodeNotConnected, "Engine not connected")
	}

	parsedJID, err := types.ParseJID(jid)
	if err != nil {
		return "", WrapError(ErrCodeInvalidJID, "Invalid JID", err)
	}

	client := e.client.GetClient()

	profile := ProfileInfo{
		JID: jid,
	}

	// Check if this is the own profile — use Store.PushName
	if client.Store.ID != nil && client.Store.ID.String() == jid {
		profile.PushName = client.Store.PushName
	}

	profile.Avatar = fetchProfilePicture(client, parsedJID)
	bp := fetchBusinessProfile(client, parsedJID)
	if bp != nil {
		profile.BusinessAddr = bp.Address
		if len(bp.Categories) > 0 {
			profile.BusinessCat = bp.Categories[0].Name
			profile.IsBusiness = true
		}
	}

	result, err := json.Marshal(profile)
	if err != nil {
		return "", WrapError(ErrCodeInternal, "Failed to encode profile", err)
	}

	return string(result), nil
}
