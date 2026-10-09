package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestSplitCacheUsesCredentialAccountIdentity(t *testing.T) {
	for _, tc := range []struct {
		name           string
		defaultAccount bool
		missingAccount bool
	}{
		{name: "named account"},
		{name: "default account", defaultAccount: true},
		{name: "uncached account", missingAccount: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("XDG_CACHE_HOME", filepath.Join(home, "cache"))
			config := filepath.Join(home, ".claude-work")
			account := filepath.Join(home, ".claude-personal")
			accountCache := filepath.Join(account, ".claude.json")
			if tc.defaultAccount {
				account = filepath.Join(home, ".claude")
				accountCache = filepath.Join(home, ".claude.json")
			}
			for _, dir := range []string{config, account} {
				if err := os.MkdirAll(dir, 0700); err != nil {
					t.Fatal(err)
				}
			}
			original := `{"oauthAccount":{"emailAddress":"work@example.test","organizationUuid":"work-org"},"theme":"dark"}`
			if err := os.WriteFile(filepath.Join(config, ".claude.json"), []byte(original), 0600); err != nil {
				t.Fatal(err)
			}
			if !tc.missingAccount {
				if err := os.WriteFile(accountCache, []byte(`{"oauthAccount":{"emailAddress":"personal@example.test","organizationUuid":"personal-org"},"theme":"light"}`), 0600); err != nil {
					t.Fatal(err)
				}
			}
			overlay, err := splitConfigDir(config, account)
			if err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(filepath.Join(overlay, ".claude.json"))
			if err != nil {
				t.Fatal(err)
			}
			var state struct {
				Theme string `json:"theme"`
				OAuth *struct {
					Email string `json:"emailAddress"`
					Org   string `json:"organizationUuid"`
				} `json:"oauthAccount"`
			}
			if err := json.Unmarshal(data, &state); err != nil {
				t.Fatal(err)
			}
			if state.Theme != "dark" {
				t.Fatalf("config preferences changed: %s", data)
			}
			if tc.missingAccount {
				if state.OAuth != nil {
					t.Fatalf("uncached credential account retained another identity: %s", data)
				}
			} else if state.OAuth == nil || state.OAuth.Email != "personal@example.test" || state.OAuth.Org != "personal-org" {
				t.Fatalf("split cache does not identify the credential account: %s", data)
			}
			if data, err := os.ReadFile(filepath.Join(config, ".claude.json")); err != nil || string(data) != original {
				t.Fatalf("config profile cache was modified: %s, %v", data, err)
			}
		})
	}
}
