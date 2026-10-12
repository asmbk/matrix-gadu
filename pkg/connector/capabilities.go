package connector

import (
	"context"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/event"
)

var generalCaps = &bridgev2.NetworkGeneralCapabilities{}

func (gc *GaduConnector) GetCapabilities() *bridgev2.NetworkGeneralCapabilities {
	return generalCaps
}

func (gc *GaduConnector) GetBridgeInfoVersion() (info, capabilities int) {
	return 1, 2
}

func (gc *GaduClient) GetCapabilities(ctx context.Context, portal *bridgev2.Portal) *event.RoomFeatures {
	return &event.RoomFeatures{
		ID: "pl.gadu.capabilities.2026_10_12",
		Formatting: event.FormattingFeatureMap{
			event.FmtBold:          event.CapLevelFullySupported,
			event.FmtItalic:        event.CapLevelFullySupported,
			event.FmtUnderline:     event.CapLevelFullySupported,
			event.FmtInlineLink:    event.CapLevelPartialSupport,
			event.FmtUnorderedList: event.CapLevelPartialSupport,
			event.FmtOrderedList:   event.CapLevelPartialSupport,
			event.FmtBlockquote:    event.CapLevelDropped,
			event.FmtInlineCode:    event.CapLevelDropped,
			event.FmtCodeBlock:     event.CapLevelDropped,
		},
		File: event.FileFeatureMap{
			event.MsgImage:    fileCaps,
			event.MsgVideo:    fileCaps,
			event.MsgAudio:    fileCaps,
			event.MsgFile:     fileCaps,
			event.CapMsgGIF:   fileCaps,
			event.CapMsgVoice: fileCaps,
		},
		Reply:               event.CapLevelDropped,
		Edit:                event.CapLevelRejected,
		Delete:              event.CapLevelRejected,
		Reaction:            event.CapLevelRejected,
		TypingNotifications: true,
	}
}
