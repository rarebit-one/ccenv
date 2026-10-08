package main

import (
	"bytes"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func TestFolderProxySelection(t *testing.T) {
	root := t.TempDir()
	child := filepath.Join(root, "child")
	if err := os.Mkdir(child, 0700); err != nil {
		t.Fatal(err)
	}
	c := config{Profiles: map[string]profile{"work": {Dir: "/work"}}}
	path := filepath.Join(root, ".ccenv")
	if err := os.WriteFile(path, []byte(`{"profile":"work","proxy":{"url":"http://127.0.0.1:8321","api_key_file":"private-key","model":"claude-test"}}`), 0600); err != nil {
		t.Fatal(err)
	}
	s, err := selectProfile(c, child, "")
	if err != nil || s.Proxy == nil || s.Name != "work" || s.Proxy.APIKeyFile != filepath.Join(root, "private-key") {
		t.Fatalf("selection=%+v err=%v", s, err)
	}
	s, err = selectProfile(c, child, "work")
	if err != nil || s.Proxy != nil {
		t.Fatalf("explicit profile must not inherit a folder proxy: %+v %v", s, err)
	}
	for _, value := range []string{
		`{"profile":"work","proxy":{"url":"http://example.test","api_key_file":"key"}}`,
		`{"profile":"work","proxy":{"url":"https://example.test","api_key":"secret"}}`,
		`{"profile":"work","proxy":{"url":"https://example.test/v1","api_key_file":"key"}}`,
		`{"profile":"work","proxy":{"url":"https://user:secret@example.test","api_key_file":"key"}}`,
		`{"profile":"work","proxy":{"url":"http://localhost","api_key_file":"key","timeout_seconds":-1}}`,
		`{"profile":"work"} {"profile":"other"}`,
		`{"profile":"unknown"}`,
	} {
		if err := os.WriteFile(path, []byte(value), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := selectProfile(c, child, ""); err == nil {
			t.Fatalf("accepted invalid settings %s", value)
		}
	}
}

func TestProxyPreflightDoesNotFollowRedirects(t *testing.T) {
	targetHit := false
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { targetHit = true }))
	defer target.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	defer redirect.Close()
	if err := checkProxy(proxyConfig{URL: redirect.URL, TimeoutSeconds: 1}, "test-client-key"); err == nil || targetHit {
		t.Fatalf("redirect must fail without sending credentials to target: hit=%v err=%v", targetHit, err)
	}
}

func TestDirectFallbackRequiresExplicitYes(t *testing.T) {
	for _, answer := range []string{"y\n", "YES\n", "\n", "n\n", "yes", ""} {
		var output bytes.Buffer
		got := confirmDirect(strings.NewReader(answer), &output, errors.New("proxy unavailable"), "work", profile{Email: "work@example.test", OrgID: "work-org"})
		want := answer == "y\n" || answer == "YES\n"
		if got != want || !strings.Contains(output.String(), "work@example.test / work-org") || !strings.Contains(output.String(), "[y/N]") {
			t.Fatalf("answer=%q approved=%v prompt=%s", answer, got, output.String())
		}
	}
}

func TestProxyKeyPermissions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "key")
	if err := os.WriteFile(path, []byte("test-client-key\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if got, err := readProxyKey(path); err != nil || got != "test-client-key" {
		t.Fatalf("key=%q err=%v", got, err)
	}
	if err := os.Chmod(path, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := readProxyKey(path); err == nil {
		t.Fatal("publicly readable key accepted")
	}
	if err := os.Chmod(path, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("first\nsecond"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := readProxyKey(path); err == nil {
		t.Fatal("multiline credential accepted")
	}
}

func TestCLIProxyLaunchAndExplicitDirect(t *testing.T) {
	root := t.TempDir()
	bin := filepath.Join(root, "ccenv")
	if output, err := exec.Command("go", "build", "-buildvcs=false", "-o", bin, ".").CombinedOutput(); err != nil {
		t.Fatalf("build: %v %s", err, output)
	}
	claude := filepath.Join(root, "claude")
	script := `#!/bin/sh
if [ "$1" = auth ]; then
  printf 'checked\n' > "$FAKE_AUTH_LOG"
  printf '{"loggedIn":true,"email":"%s","orgId":"work-org","authMethod":"claude.ai"}\n' "${FAKE_EMAIL:-work@example.test}"
  exit 0
fi
printf 'DIR=%s\nURL=%s\nTOKEN=%s\nMODEL=%s\n' "$CLAUDE_CONFIG_DIR" "$ANTHROPIC_BASE_URL" "$ANTHROPIC_AUTH_TOKEN" "$ANTHROPIC_MODEL"
for arg in "$@"; do printf 'ARG=%s\n' "$arg"; done
`
	if err := os.WriteFile(claude, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	keyFile := filepath.Join(root, "key")
	if err := os.WriteFile(keyFile, []byte("test-client-key"), 0600); err != nil {
		t.Fatal(err)
	}
	configFile := filepath.Join(root, "config.json")
	if err := os.WriteFile(configFile, []byte(fmt.Sprintf(`{"profiles":{"work":{"dir":%q,"email":"work@example.test","org_id":"work-org"}}}`, root)), 0600); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" || r.Header.Get("Authorization") != "Bearer test-client-key" {
			http.Error(w, "unauthorized", 401)
			return
		}
		fmt.Fprint(w, `{"data":[{"id":"claude-test"}]}`)
	}))
	defer server.Close()
	selector := fmt.Sprintf(`{"profile":"work","proxy":{"url":%q,"api_key_file":%q,"model":"claude-test"}}`, server.URL, keyFile)
	if err := os.WriteFile(filepath.Join(root, ".ccenv"), []byte(selector), 0600); err != nil {
		t.Fatal(err)
	}
	call := func(args ...string) (string, error) {
		cmd := exec.Command(bin, args...)
		cmd.Dir = root
		cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
		for _, value := range os.Environ() {
			if !strings.HasPrefix(value, "ANTHROPIC_") && !strings.HasPrefix(value, "CLAUDE_CODE_") && !strings.HasPrefix(value, "CCENV_") {
				cmd.Env = append(cmd.Env, value)
			}
		}
		cmd.Env = append(cmd.Env, "CCENV_CONFIG="+configFile, "CCENV_CLAUDE_BIN="+claude, "FAKE_AUTH_LOG="+filepath.Join(root, "auth-check"))
		output, err := cmd.CombinedOutput()
		return string(output), err
	}
	output, err := call("run", "--", "-p", "hello world")
	if err != nil || !strings.Contains(output, "DIR="+root+"\nURL="+server.URL+"\nTOKEN=test-client-key\nMODEL=claude-test\nARG=-p\nARG=hello world\n") {
		t.Fatalf("proxy launch: %v %s", err, output)
	}
	if _, err := os.Stat(filepath.Join(root, "auth-check")); !os.IsNotExist(err) {
		t.Fatal("proxy launch consulted the local login")
	}
	output, err = call("run", "--account", "work", "--")
	if err == nil || !strings.Contains(output, "require --direct") || strings.Contains(output, "DIR=") {
		t.Fatalf("conflicting account override: %v %s", err, output)
	}
	server.Close()
	output, err = call("run", "--", "-c")
	if err == nil || !strings.Contains(output, "no interactive terminal") || strings.Contains(output, "DIR=") {
		t.Fatalf("noninteractive failure launched Claude: %v %s", err, output)
	}
	output, err = call("run", "--direct", "--", "-c")
	if err != nil || !strings.Contains(output, "URL=\nTOKEN=\nMODEL=\nARG=-c\n") {
		t.Fatalf("explicit direct launch leaked proxy config: %v %s", err, output)
	}
	t.Setenv("FAKE_EMAIL", "other@example.test")
	output, err = call("run", "--direct", "--", "-c")
	if err == nil || !strings.Contains(output, "identity changed") || strings.Contains(output, "DIR=") {
		t.Fatalf("direct launch bypassed the identity pin: %v %s", err, output)
	}
}
