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

func TestSplitConfigDirDisplacesStaleRuntimeCaches(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, "cache"))
	cache, err := os.UserCacheDir()
	if err != nil {
		t.Fatal(err)
	}
	config := filepath.Join(home, ".claude-work")
	account := filepath.Join(home, ".claude-personal")
	for _, dir := range []string{filepath.Join(config, "state"), account} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	overlay, err := splitConfigDir(config, account)
	if err != nil {
		t.Fatal(err)
	}
	// Claude wrote these into the overlay before the shared directory had them.
	if err := os.Remove(filepath.Join(overlay, "state")); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(overlay, "state"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(overlay, "state", "verdicts.json"), []byte("old"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(overlay, ".last-cleanup"), []byte("1"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(config, ".last-cleanup"), []byte("2"), 0600); err != nil {
		t.Fatal(err)
	}

	if _, err := splitConfigDir(config, account); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"state", ".last-cleanup"} {
		target, err := os.Readlink(filepath.Join(overlay, name))
		if err != nil || target != filepath.Join(config, name) {
			t.Fatalf("%s not linked to the shared entry: %q, %v", name, target, err)
		}
	}
	moved, err := filepath.Glob(filepath.Join(cache, "ccenv", "displaced", filepath.Base(overlay), "*", "state", "verdicts.json"))
	if err != nil || len(moved) != 1 {
		t.Fatalf("stale state was not kept aside: %v, %v", moved, err)
	}
	if data, err := os.ReadFile(moved[0]); err != nil || string(data) != "old" {
		t.Fatalf("displaced state changed: %q, %v", data, err)
	}
}

func TestSplitConfigDirRefusesUnknownOverlayFiles(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, "cache"))
	config := filepath.Join(home, ".claude-work")
	account := filepath.Join(home, ".claude-personal")
	for _, dir := range []string{config, account} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(config, "settings.json"), []byte("{}"), 0600); err != nil {
		t.Fatal(err)
	}
	overlay, err := splitConfigDir(config, account)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(overlay, "settings.json")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(overlay, "settings.json"), []byte(`{"local":true}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := splitConfigDir(config, account); err == nil {
		t.Fatal("split replaced an unknown overlay file")
	}
	if data, err := os.ReadFile(filepath.Join(overlay, "settings.json")); err != nil || string(data) != `{"local":true}` {
		t.Fatalf("unknown overlay file changed: %q, %v", data, err)
	}
}
