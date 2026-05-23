// Package core provides the WhatsApp engine implementation.
// This file sets up realistic device fingerprinting so the engine looks
// like a real WhatsApp Web session running in Chrome.
package core

import (
	"math/rand"

	waCompanionReg "go.mau.fi/whatsmeow/proto/waCompanionReg"
	"go.mau.fi/whatsmeow/store"
	"google.golang.org/protobuf/proto"
)

// browserProfile defines the device info sent to WhatsApp during pairing.
// The enum and message are both on DeviceProps, not CompanionProps.
type browserProfile struct {
	os           string
	platformType waCompanionReg.DeviceProps_PlatformType // ← DeviceProps_PlatformType
	browserVer   [3]uint32
}

// Realistic profiles that match actual WhatsApp Web sessions.
var browserProfiles = []browserProfile{
	{
		os:           "Mac OS X",
		platformType: waCompanionReg.DeviceProps_CHROME, // ← DeviceProps_CHROME
		browserVer:   [3]uint32{131, 0, 0},
	},
	{
		os:           "Windows",
		platformType: waCompanionReg.DeviceProps_CHROME,
		browserVer:   [3]uint32{131, 0, 0},
	},
	{
		os:           "Mac OS X",
		platformType: waCompanionReg.DeviceProps_SAFARI, // ← DeviceProps_SAFARI
		browserVer:   [3]uint32{18, 1, 1},
	},
	{
		os:           "Ubuntu",
		platformType: waCompanionReg.DeviceProps_FIREFOX, // ← DeviceProps_FIREFOX
		browserVer:   [3]uint32{133, 0, 0},
	},
}

// selectedProfile holds the profile chosen for this process lifetime.
var selectedProfile = browserProfiles[0]

// SetBrowserProfile overrides the auto-selected profile.
// Call BEFORE waengine.Init(). 0=Chrome/Mac, 1=Chrome/Win, 2=Safari/Mac, 3=Firefox/Linux
func SetBrowserProfile(profileIndex int) {
	if profileIndex >= 0 && profileIndex < len(browserProfiles) {
		selectedProfile = browserProfiles[profileIndex]
	}
}

// applyDeviceProps configures the global whatsmeow store device properties.
// Called automatically on package init.
func applyDeviceProps() {
	selectedProfile = browserProfiles[rand.Intn(len(browserProfiles))]

	p := selectedProfile
	v := p.browserVer

	// store.DeviceProps expects *waCompanionReg.DeviceProps (not CompanionProps)
	store.DeviceProps = &waCompanionReg.DeviceProps{
		Os:           proto.String(p.os),
		PlatformType: p.platformType.Enum(),
		Version: &waCompanionReg.DeviceProps_AppVersion{ // ← DeviceProps_AppVersion
			Primary:   proto.Uint32(v[0]),
			Secondary: proto.Uint32(v[1]),
			Tertiary:  proto.Uint32(v[2]),
		},
		RequireFullSync: proto.Bool(false),
	}
}

func init() {
	applyDeviceProps()
}
