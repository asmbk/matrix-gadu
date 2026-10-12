// matrix-gadu is a Matrix puppeting bridge for Gadu-Gadu.
package main

import (
	"fmt"
	"os"

	"maunium.net/go/mautrix/bridgev2/matrix/mxmain"

	"github.com/asmbk/matrix-gadu/pkg/connector"
	"github.com/asmbk/matrix-gadu/pkg/gadu/libgadu"
)

// These are set at build time with -ldflags "-X main.Tag=...".
var (
	Tag       = "unknown"
	Commit    = "unknown"
	BuildTime = "unknown"
)

var m = mxmain.BridgeMain{
	Name:        "matrix-gadu",
	URL:         "https://github.com/asmbk/matrix-gadu",
	Description: "A Matrix-Gadu-Gadu puppeting bridge.",
	Version:     "0.1.0",
	Connector:   &connector.GaduConnector{NewSession: libgadu.New},
}

func main() {
	m.InitVersion(Tag, Commit, BuildTime)
	if path, ok := exampleConfigPath(os.Args[1:]); ok {
		if err := writeExampleConfig(path); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	m.Run()
}
