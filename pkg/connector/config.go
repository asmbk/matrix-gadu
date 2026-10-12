package connector

import (
	_ "embed"
	"fmt"
	"strings"
	"text/template"

	up "go.mau.fi/util/configupgrade"
	"gopkg.in/yaml.v3"

	"github.com/asmbk/matrix-gadu/pkg/gadu"
)

//go:embed example-config.yaml
var ExampleConfig string

type Config struct {
	DisplaynameTemplate string `yaml:"displayname_template"`
	InitialStatus       string `yaml:"initial_status"`
	Multilogon          bool   `yaml:"multilogon"`
	TLS                 string `yaml:"tls"`
	MaxMessageLength    int    `yaml:"max_message_length"`
	ContactRooms        bool   `yaml:"contact_rooms"`
	StatusInTopic       bool   `yaml:"status_in_topic"`
	StatusInName        bool   `yaml:"status_in_name"`
	ImageLinks          bool   `yaml:"image_links"`
	OfflineLowPriority  bool   `yaml:"offline_low_priority"`
	StatusChangesRead   bool   `yaml:"status_changes_read"`

	displaynameTemplate *template.Template `yaml:"-"`
	initialStatus       gadu.Status        `yaml:"-"`
	tlsMode             gadu.TLSMode       `yaml:"-"`
}

type umConfig Config

func (c *Config) UnmarshalYAML(node *yaml.Node) error {
	if err := node.Decode((*umConfig)(c)); err != nil {
		return err
	}
	return c.PostProcess()
}

func (c *Config) PostProcess() error {
	var err error
	c.displaynameTemplate, err = template.New("displayname").Parse(c.DisplaynameTemplate)
	if err != nil {
		return fmt.Errorf("invalid displayname_template: %w", err)
	}
	if c.InitialStatus == "" {
		c.InitialStatus = "available"
	}
	if c.initialStatus, err = gadu.ParseStatus(c.InitialStatus); err != nil {
		return fmt.Errorf("invalid initial_status: %w", err)
	}
	switch c.TLS {
	case "", "required":
		c.tlsMode = gadu.TLSRequired
	case "enabled":
		c.tlsMode = gadu.TLSEnabled
	default:
		return fmt.Errorf("invalid tls value %q (use required or enabled)", c.TLS)
	}
	if c.MaxMessageLength <= 0 {
		c.MaxMessageLength = 1900
	}
	return nil
}

func upgradeConfig(helper up.Helper) {
	helper.Copy(up.Str, "displayname_template")
	helper.Copy(up.Str, "initial_status")
	helper.Copy(up.Bool, "multilogon")
	helper.Copy(up.Str, "tls")
	helper.Copy(up.Int, "max_message_length")
	helper.Copy(up.Bool, "contact_rooms")
	helper.Copy(up.Bool, "status_in_topic")
	helper.Copy(up.Bool, "status_in_name")
	helper.Copy(up.Bool, "image_links")
	helper.Copy(up.Bool, "offline_low_priority")
	helper.Copy(up.Bool, "status_changes_read")
}

// DisplaynameParams are the fields of displayname_template.
type DisplaynameParams struct {
	UIN       string
	ShowName  string
	Nickname  string
	FirstName string
	LastName  string
	FullName  string
}

// FormatDisplayname returns the ghost name for a contact.
func (c *Config) FormatDisplayname(contact gadu.Contact) string {
	var buf strings.Builder
	err := c.displaynameTemplate.Execute(&buf, &DisplaynameParams{
		UIN:       contact.UIN.String(),
		ShowName:  contact.ShowName,
		Nickname:  contact.Nickname,
		FirstName: contact.FirstName,
		LastName:  contact.LastName,
		FullName:  contact.FullName(),
	})
	if err != nil || strings.TrimSpace(buf.String()) == "" {
		return contact.UIN.String()
	}
	return strings.TrimSpace(buf.String())
}

func (gc *GaduConnector) GetConfig() (string, any, up.Upgrader) {
	return ExampleConfig, &gc.Config, &up.StructUpgrader{
		SimpleUpgrader: up.SimpleUpgrader(upgradeConfig),
		Base:           ExampleConfig,
	}
}
