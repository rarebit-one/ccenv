package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestProxyOnlyPolicyUsesGatewayAndFailsClosed(t *testing.T) {
	root := t.TempDir()
	bin := filepath.Join(root, "ccenv")
	if output, err := exec.Command("go", "build", "-buildvcs=false", "-o", bin, ".").CombinedOutput(); err != nil {
		t.Fatalf("build: %v %s", err, output)
	}
	claude := filepath.Join(root, "claude")
	if err := os.WriteFile(claude, []byte(`#!/bin/sh
if [ "$1" = auth ]; then printf 'checked\n' > "$FAKE_AUTH_LOG"; exit 1; fi
printf 'DIR=%s\nTOKEN=%s\nHEADERS=%s\n' "$CLAUDE_CONFIG_DIR" "$ANTHROPIC_AUTH_TOKEN" "$ANTHROPIC_CUSTOM_HEADERS"
`), 0700); err != nil {
		t.Fatal(err)
	}
	key := filepath.Join(root, "key")
	if err := os.WriteFile(key, []byte("client-key"), 0600); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" || r.Header.Get("Authorization") != "Bearer client-key" {
			http.Error(w, "unauthorized", 401)
			return
		}
		fmt.Fprint(w, `{"data":[{"id":"work/claude-test"}]}`)
	}))
	defer server.Close()
	selector := fmt.Sprintf(`{"profile":"work","proxy":{"url":%q,"api_key_file":%q,"auth_mode":"claudeai","model":"work/claude-test","default_models":{"sonnet":"work/claude-test","opus":"work/claude-test","haiku":"work/claude-test"}}}`, server.URL, key)
	if err := os.WriteFile(filepath.Join(root, ".ccenv"), []byte(selector), 0600); err != nil {
		t.Fatal(err)
	}
	configFile := filepath.Join(root, "config.json")
	if err := os.WriteFile(configFile, []byte(fmt.Sprintf(`{"profiles":{"work":{"dir":%q,"email":"work@example.test","org_id":"work-org"}}}`, root)), 0600); err != nil {
		t.Fatal(err)
	}
	authLog := filepath.Join(root, "auth-check")
	call := func(args ...string) (string, error) {
		cmd := exec.Command(bin, args...)
		cmd.Dir = root
		for _, value := range os.Environ() {
			if !strings.HasPrefix(value, "ANTHROPIC_") && !strings.HasPrefix(value, "CLAUDE_") && !strings.HasPrefix(value, "CCENV_") {
				cmd.Env = append(cmd.Env, value)
			}
		}
		cmd.Env = append(cmd.Env, "CCENV_CONFIG="+configFile, "CCENV_CLAUDE_BIN="+claude, "FAKE_AUTH_LOG="+authLog)
		output, err := cmd.CombinedOutput()
		return string(output), err
	}
	if output, err := call("proxy-only", "on"); err != nil || !strings.Contains(output, "Proxy-only mode: on") {
		t.Fatalf("enable: %v %s", err, output)
	}
	output, err := call("run", "--")
	if err != nil || !strings.Contains(output, "DIR="+root+"\nTOKEN=client-key\nHEADERS=\n") {
		t.Fatalf("gateway launch: %v %s", err, output)
	}
	if _, err := os.Stat(authLog); !os.IsNotExist(err) {
		t.Fatal("proxy-only launch consulted local Claude login")
	}
	if output, err := call("run", "--direct", "--"); err == nil || !strings.Contains(output, "disabled in proxy-only mode") {
		t.Fatalf("direct launch: %v %s", err, output)
	}
	if output, err := call("run", "--profile", "work", "--"); err == nil || !strings.Contains(output, "requires a folder proxy") {
		t.Fatalf("profile bypass: %v %s", err, output)
	}
	server.Close()
	if output, err := call("run", "--"); err == nil || !strings.Contains(output, "proxy-only mode: gateway unavailable") || strings.Contains(output, "DIR=") {
		t.Fatalf("gateway failure: %v %s", err, output)
	}
	if _, err := os.Stat(authLog); !os.IsNotExist(err) {
		t.Fatal("gateway failure consulted local Claude login")
	}
}
