package main

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestExampleConfigAllowsEncryption(t *testing.T) {
	cfg, err := makeExampleConfig()
	if err != nil {
		t.Fatal(err)
	}
	var parsed struct {
		Network struct {
			ContactRooms bool `yaml:"contact_rooms"`
		} `yaml:"network"`
		Encryption struct {
			Allow   bool `yaml:"allow"`
			Default bool `yaml:"default"`
		} `yaml:"encryption"`
		Appservice struct {
			Bot struct {
				Username    string `yaml:"username"`
				DisplayName string `yaml:"displayname"`
			} `yaml:"bot"`
		} `yaml:"appservice"`
	}
	if err := yaml.Unmarshal([]byte(cfg), &parsed); err != nil {
		t.Fatal(err)
	}
	if !parsed.Encryption.Allow || parsed.Encryption.Default {
		t.Errorf("encryption allow=%v default=%v, want true/false", parsed.Encryption.Allow, parsed.Encryption.Default)
	}
	if parsed.Appservice.Bot.Username != "ggbot" {
		t.Errorf("bot username = %q, the template was not executed", parsed.Appservice.Bot.Username)
	}
	if parsed.Appservice.Bot.DisplayName != "☀️ Gadu-Gadu bridge bot" {
		t.Errorf("bot displayname = %q", parsed.Appservice.Bot.DisplayName)
	}
	if !parsed.Network.ContactRooms {
		t.Error("contact_rooms is not true by default")
	}
	if !strings.Contains(cfg, "max_message_length: 1900") {
		t.Error("network section is missing")
	}
}

func TestExampleConfigPath(t *testing.T) {
	for _, tc := range []struct {
		args    []string
		path    string
		handled bool
	}{
		{[]string{"-e"}, "config.yaml", true},
		{[]string{"-c", "/data/config.yaml", "-e"}, "/data/config.yaml", true},
		{[]string{"--generate-example-config", "--config=x.yaml"}, "x.yaml", true},
		{[]string{"-c", "config.yaml", "-r", "registration.yaml"}, "", false},
		{[]string{"-ec", "x.yaml"}, "", false},
		{nil, "config.yaml", false},
	} {
		path, ok := exampleConfigPath(tc.args)
		if ok != tc.handled || (ok && path != tc.path) {
			t.Errorf("exampleConfigPath(%q) = %q, %v", tc.args, path, ok)
		}
	}
}
