package connector

import (
	"maunium.net/go/mautrix/bridgev2/database"
)

func (gc *GaduConnector) GetDBMetaTypes() database.MetaTypes {
	return database.MetaTypes{
		UserLogin: func() any {
			return &UserLoginMetadata{}
		},
		Portal: func() any {
			return &PortalMetadata{}
		},
	}
}

// PortalMetadata is stored with each contact room.
type PortalMetadata struct {
	// CustomAvatar is true when the user set the room picture. The bridge then
	// does not replace it with the GG avatar.
	CustomAvatar bool `json:"custom_avatar,omitempty"`
	// LowPriority is true when the bridge tagged the room as low priority,
	// because the contact is offline. The bridge removes only such tags.
	LowPriority bool `json:"low_priority,omitempty"`
}

// UserLoginMetadata is stored with each login. GG has no login tokens, so the
// bridge must keep the password to reconnect.
type UserLoginMetadata struct {
	Password   string `json:"password"`
	StatusText string `json:"status_text,omitempty"`
}
