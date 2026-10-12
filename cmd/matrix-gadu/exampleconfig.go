package main

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"text/template"

	"maunium.net/go/mautrix/bridgev2/matrix/mxmain"
)

// encryptionAllowDefault is the line of the mautrix example config that this
// bridge changes. With encryption allowed, the bot works in the encrypted DMs
// that most clients create. Contact rooms stay unencrypted (encryption.default
// is false), because some clients cannot read encrypted bridge rooms.
const encryptionAllowDefault = "encryption:\n    # Whether to enable encryption at all. If false, the bridge will not function in encrypted rooms.\n    allow: false"

// botDisplayNameDefault is the line of the mautrix example config that sets
// the bot name. The bridge puts the GG sun in front of it.
const botDisplayNameDefault = "displayname: $<<.DisplayName>> bridge bot"

const botDisplayName = "displayname: ☀️ $<<.DisplayName>> bridge bot"

// exampleConfigPath returns the config path if the arguments ask for an
// example config (-e), in the simple forms this function understands.
// For any other form, mxmain handles the flag itself.
func exampleConfigPath(args []string) (string, bool) {
	path, example := "config.yaml", false
	for i := 0; i < len(args); i++ {
		switch a := args[i]; {
		case a == "-e" || a == "--generate-example-config":
			example = true
		case (a == "-c" || a == "--config") && i+1 < len(args):
			path = args[i+1]
			i++
		case strings.HasPrefix(a, "--config="):
			path = strings.TrimPrefix(a, "--config=")
		case strings.HasPrefix(a, "-"):
			// An unknown flag: let mxmain parse everything.
			return "", false
		}
	}
	return path, example
}

// makeExampleConfig builds the same example config as mxmain, with encryption
// allowed and the sun in the bot name.
func makeExampleConfig() (string, error) {
	base := mxmain.MatrixExampleConfigBase
	if !strings.Contains(base, encryptionAllowDefault) {
		return "", errors.New("the mautrix example config changed: update encryptionAllowDefault")
	}
	if !strings.Contains(base, botDisplayNameDefault) {
		return "", errors.New("the mautrix example config changed: update botDisplayNameDefault")
	}
	base = strings.Replace(base, encryptionAllowDefault, strings.TrimSuffix(encryptionAllowDefault, "false")+"true", 1)
	base = strings.Replace(base, botDisplayNameDefault, botDisplayName, 1)
	tpl, err := template.New("example-config.yaml").Delims("$<<", ">>").Parse(base)
	if err != nil {
		return "", err
	}
	networkExample, _, _ := m.Connector.GetConfig()
	var buf strings.Builder
	buf.WriteString("# Network-specific config options\nnetwork:\n")
	for _, line := range strings.Split(networkExample, "\n") {
		buf.WriteString("    " + line + "\n")
	}
	buf.WriteString("\n")
	if err := tpl.Execute(&buf, m.Connector.GetName()); err != nil {
		return "", err
	}
	return buf.String(), nil
}

func writeExampleConfig(path string) error {
	cfg, err := makeExampleConfig()
	if err != nil {
		return err
	}
	if path == "-" {
		fmt.Print(cfg)
		return nil
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("%s already exists, please remove it if you want to generate a new example", path)
	}
	if err := os.WriteFile(path, []byte(cfg), 0o600); err != nil {
		return err
	}
	fmt.Println("Wrote example config to", path)
	return nil
}
