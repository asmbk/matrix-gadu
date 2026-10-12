// Package connector implements the bridgev2 network connector for Gadu-Gadu.
//
// The package has no cgo code. It talks to GG only through [gadu.Session],
// created by the [GaduConnector.NewSession] factory.
package connector

import (
	"context"
	"errors"

	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/commands"
	"maunium.net/go/mautrix/bridgev2/networkid"

	"github.com/asmbk/matrix-gadu/pkg/gadu"
)

type GaduConnector struct {
	br     *bridgev2.Bridge
	Config Config
	// NewSession creates GG sessions. main sets it to the libgadu implementation.
	NewSession gadu.Factory
}

var (
	_ bridgev2.NetworkConnector            = (*GaduConnector)(nil)
	_ bridgev2.IdentifierValidatingNetwork = (*GaduConnector)(nil)
)

func (gc *GaduConnector) Init(bridge *bridgev2.Bridge) {
	gc.br = bridge
	if proc, ok := bridge.Commands.(*commands.Processor); ok {
		proc.AddHandlers(cmdReconnect, cmdSyncContacts, cmdStatus, cmdOnline, cmdContactAdd, cmdContactRemove, cmdContactRename, cmdBlock, cmdUnblock)
	}
}

func (gc *GaduConnector) Start(_ context.Context) error {
	if gc.NewSession == nil {
		return errors.New("connector: NewSession is not set")
	}
	return nil
}

func (gc *GaduConnector) GetName() bridgev2.BridgeName {
	return bridgev2.BridgeName{
		DisplayName:          "Gadu-Gadu",
		NetworkURL:           "https://www.gg.pl",
		NetworkID:            "gg",
		BeeperBridgeType:     "github.com/asmbk/matrix-gadu",
		DefaultPort:          29360,
		DefaultCommandPrefix: "!gg",
	}
}

func (gc *GaduConnector) ValidateUserID(id networkid.UserID) bool {
	_, err := parseUserID(id)
	return err == nil
}
