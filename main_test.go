package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSelectionWalksUpAndUsesNearestFile(t *testing.T) {
	root := t.TempDir()
	parent := filepath.Join(root, "parent")
	child := filepath.Join(parent, "child")
	if err := os.MkdirAll(child, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".ccenv"), []byte("family\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(parent, ".ccenv"), []byte("rarebit # project\n"), 0600); err != nil {
		t.Fatal(err)
	}
	c := config{Default: "fundbright", Profiles: map[string]profile{
		"fundbright": {Dir: "/tmp/fundbright"},
		"family":     {Dir: "/tmp/family"},
		"rarebit":    {Dir: "/tmp/rarebit"},
	}}
	s, err := selectProfile(c, child, "")
	if err != nil {
		t.Fatal(err)
	}
	if s.Name != "rarebit" || s.Source != filepath.Join(parent, ".ccenv") {
		t.Fatalf("got %+v", s)
	}
	s, err = selectProfile(c, child, "family")
	if err != nil || s.Name != "family" || s.Source != "--profile" {
		t.Fatalf("override: %+v, %v", s, err)
	}
}

func TestSelectionFailsClosed(t *testing.T) {
	root := t.TempDir()
	c := config{Default: "fundbright", Profiles: map[string]profile{
		"fundbright": {Dir: "/tmp/fundbright"},
	}}
	for _, value := range []string{"unregistered\n", "../family\n", "\n"} {
		if err := os.WriteFile(filepath.Join(root, ".ccenv"), []byte(value), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := selectProfile(c, root, ""); err == nil {
			t.Fatalf("expected an error for %q", value)
		}
	}
	if err := os.Remove(filepath.Join(root, ".ccenv")); err != nil {
		t.Fatal(err)
	}
	s, err := selectProfile(c, root, "")
	if err != nil || s.Name != "fundbright" || s.Source != "global default" {
		t.Fatalf("default: %+v, %v", s, err)
	}
}

func TestConfigRoundTrip(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.json")
	t.Setenv("CCENV_CONFIG", p)
	c := config{Default: "work", Profiles: map[string]profile{
		"work": {Dir: "/tmp/claude-work", Email: "work@example.test", OrgID: "org-1"},
	}}
	if err := saveConfig(c); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("config mode %o", info.Mode().Perm())
	}
	got, err := loadConfig()
	if err != nil || got.Default != "work" || got.Profiles["work"].Email != "work@example.test" {
		t.Fatalf("load: %+v, %v", got, err)
	}
}

func TestVerifyRejectsChangedAccount(t *testing.T) {
	root := t.TempDir()
	bin := filepath.Join(root, "claude")
	script := "#!/bin/sh\nprintf '%s\\n' '{\"loggedIn\":true,\"email\":\"other@example.test\",\"orgId\":\"other-org\",\"authMethod\":\"claude.ai\"}'\n"
	if err := os.WriteFile(bin, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CCENV_CLAUDE_BIN", bin)
	err := verify("work", profile{Dir: root, Email: "work@example.test", OrgID: "work-org"})
	if err == nil || !strings.Contains(err.Error(), "identity changed") {
		t.Fatalf("expected identity mismatch, got %v", err)
	}
}
