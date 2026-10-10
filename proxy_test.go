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
		`{"profile":"work","proxy":{"url":"http://localhost","api_key_file":"key","auth_mode":"unknown"}}`,
		`{"profile":"work","proxy":{"url":"http://localhost","api_key_file":"key","auth_profile":"work-login"}}`,
		`{"profile":"work","proxy":{"url":"http://localhost","api_key_file":"key","auth_mode":"claudeai","auth_profile":"../work"}}`,
		`{"profile":"work","proxy":{"url":"http://localhost","api_key_file":"key","provider":"unknown"}}`,
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
	t.Setenv("HOME", filepath.Join(root, "home"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(root, "cache"))
	cacheDir, err := os.UserCacheDir()
	if err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(root, "ccenv")
	if output, err := exec.Command("go", "build", "-buildvcs=false", "-o", bin, ".").CombinedOutput(); err != nil {
		t.Fatalf("build: %v %s", err, output)
	}
	claude := filepath.Join(root, "claude")
	script := `#!/bin/sh
if [ "$1" = --version ]; then printf '2.1.293\n'; exit 0; fi
if [ "$1" = auth ]; then
  printf 'checked\n' > "$FAKE_AUTH_LOG"
  printf '{"loggedIn":true,"email":"%s","orgId":"work-org","authMethod":"%s"}\n' "${FAKE_EMAIL:-work@example.test}" "${FAKE_AUTH_METHOD:-claude.ai}"
  exit 0
fi
printf 'DIR=%s\nURL=%s\nTOKEN=%s\nMODEL=%s\n' "$CLAUDE_CONFIG_DIR" "$ANTHROPIC_BASE_URL" "$ANTHROPIC_AUTH_TOKEN" "$ANTHROPIC_MODEL"
for arg in "$@"; do printf 'ARG=%s\n' "$arg"; done
printf 'SONNET=%s\nOPUS=%s\nHAIKU=%s\nSUBAGENT=%s\n' "$ANTHROPIC_DEFAULT_SONNET_MODEL" "$ANTHROPIC_DEFAULT_OPUS_MODEL" "$ANTHROPIC_DEFAULT_HAIKU_MODEL" "$CLAUDE_CODE_SUBAGENT_MODEL"
printf 'HEADERS=%s\n' "$ANTHROPIC_CUSTOM_HEADERS"
printf 'CREDENTIALS=%s\n' "$CLAUDE_SECURESTORAGE_CONFIG_DIR"
`
	if err := os.WriteFile(claude, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	keyFile := filepath.Join(root, "key")
	if err := os.WriteFile(keyFile, []byte("test-client-key"), 0600); err != nil {
		t.Fatal(err)
	}
	configFile := filepath.Join(root, "config.json")
	personalDir := filepath.Join(root, "personal")
	if err := os.Mkdir(personalDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configFile, []byte(fmt.Sprintf(`{"profiles":{"work":{"dir":%q,"email":"work@example.test","org_id":"work-org"},"personal":{"dir":%q,"email":"personal@example.test","org_id":"work-org"}}}`, root, personalDir)), 0600); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" || r.Header.Get("Authorization") != "Bearer test-client-key" {
			http.Error(w, "unauthorized", 401)
			return
		}
		fmt.Fprint(w, `{"data":[{"id":"claude-test"},{"id":"work/claude-test"},{"id":"personal/claude-test"}]}`)
	}))
	defer server.Close()
	selector := fmt.Sprintf(`{"profile":"work","proxy":{"url":%q,"api_key_file":%q,"model":"claude-test","default_models":{"sonnet":"claude-test","opus":"claude-test","haiku":"claude-test"}}}`, server.URL, keyFile)
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
	limited := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/models" {
			fmt.Fprint(w, `{"data":[]}`)
			return
		}
		w.Header().Set("Retry-After", "3600")
		w.WriteHeader(429)
		fmt.Fprint(w, `{"error":{"type":"rate_limit_error","message":"All credentials for model claude-test are cooling down"}}`)
	}))
	defer limited.Close()
	limitedSelector := strings.Replace(selector, server.URL, limited.URL, 1)
	limitedSelector = strings.Replace(limitedSelector, `"proxy":{`, `"proxy":{"provider":"cliproxyapi",`, 1)
	if err := os.WriteFile(filepath.Join(root, ".ccenv"), []byte(limitedSelector), 0600); err != nil {
		t.Fatal(err)
	}
	limitedOutput, limitedErr := call("run", "--")
	if limitedErr == nil || !strings.Contains(limitedOutput, "work account (work@example.test): CLIProxyAPI account is in quota cooldown") || !strings.Contains(limitedOutput, "gateway retry time") || strings.Contains(limitedOutput, "Launch directly") || strings.Contains(limitedOutput, "DIR=") || strings.Contains(limitedOutput, "test-client-key") {
		t.Fatalf("quota diagnostic: %v %s", limitedErr, limitedOutput)
	}
	if _, err := os.Stat(filepath.Join(root, "auth-check")); !os.IsNotExist(err) {
		t.Fatal("cooldown consulted local login")
	}
	if err := os.WriteFile(filepath.Join(root, ".ccenv"), []byte(selector), 0600); err != nil {
		t.Fatal(err)
	}
	output, err := call("run", "--", "-p", "hello world")
	if err != nil || !strings.Contains(output, "DIR="+root+"\nURL="+server.URL+"\nTOKEN=test-client-key\nMODEL=claude-test\nARG=-p\nARG=hello world\n") {
		t.Fatalf("proxy launch: %v %s", err, output)
	}
	if _, err := os.Stat(filepath.Join(root, "auth-check")); !os.IsNotExist(err) {
		t.Fatal("proxy launch consulted the local login")
	}
	if !strings.Contains(output, "SONNET=claude-test\nOPUS=claude-test\nHAIKU=claude-test\nSUBAGENT=claude-test\n") {
		t.Fatalf("proxy defaults were not passed to Claude: %s", output)
	}
	output, err = call("run", "--account", "work", "--")
	if err != nil || !strings.Contains(output, "MODEL=claude-test\n") {
		t.Fatalf("explicit proxy account: %v %s", err, output)
	}
	output, err = call("run", "--ignore-pin", "--")
	if err == nil || !strings.Contains(output, "requires --direct") || strings.Contains(output, "DIR=") {
		t.Fatalf("proxy pin bypass: %v %s", err, output)
	}
	nativeSelector := strings.Replace(selector, `"proxy":{`, `"proxy":{"auth_mode":"claudeai",`, 1)
	if err := os.WriteFile(filepath.Join(root, ".ccenv"), []byte(nativeSelector), 0600); err != nil {
		t.Fatal(err)
	}
	output, err = call("run", "--", "-p", "hello world")
	if err != nil || !strings.Contains(output, "TOKEN=\nMODEL=claude-test\n") || !strings.Contains(output, "HEADERS=x-api-key: test-client-key\n") {
		t.Fatalf("native subscription proxy launch: %v %s", err, output)
	}
	if _, err := os.Stat(filepath.Join(root, "auth-check")); err != nil {
		t.Fatal("native subscription proxy skipped the local identity check")
	}
	sharedConfig, err := os.ReadFile(configFile)
	if err != nil {
		t.Fatal(err)
	}
	sharedConfig = bytes.Replace(sharedConfig, []byte(`"profiles":{`), []byte(fmt.Sprintf(`"profiles":{"work-login":{"dir":%q,"email":"work@example.test","org_id":"work-org"},`, personalDir)), 1)
	if err := os.WriteFile(configFile, sharedConfig, 0600); err != nil {
		t.Fatal(err)
	}
	sharedSelector := strings.Replace(nativeSelector, `"proxy":{`, `"proxy":{"auth_profile":"work-login",`, 1)
	if err := os.WriteFile(filepath.Join(root, ".ccenv"), []byte(sharedSelector), 0600); err != nil {
		t.Fatal(err)
	}
	output, err = call("run", "--", "-p", "hello world")
	if err != nil || !strings.Contains(output, "TOKEN=\nMODEL=claude-test\n") || !strings.Contains(output, "HEADERS=x-api-key: test-client-key\n") || !strings.Contains(output, "CREDENTIALS="+personalDir+"\n") || !strings.Contains(output, "DIR="+filepath.Join(cacheDir, "ccenv", "config-overlays")+string(filepath.Separator)) {
		t.Fatalf("same-account native proxy login: %v %s", err, output)
	}
	wrongLogin := strings.Replace(sharedSelector, `"auth_profile":"work-login"`, `"auth_profile":"personal"`, 1)
	if err := os.WriteFile(filepath.Join(root, ".ccenv"), []byte(wrongLogin), 0600); err != nil {
		t.Fatal(err)
	}
	output, err = call("run", "--")
	if err == nil || !strings.Contains(output, "must have the same pinned identity") || strings.Contains(output, "DIR=") {
		t.Fatalf("proxy auth profile accepted another identity: %v %s", err, output)
	}
	scopedSelector := strings.ReplaceAll(sharedSelector, `"claude-test"`, `"work/claude-test"`)
	if err := os.WriteFile(filepath.Join(root, ".ccenv"), []byte(scopedSelector), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("FAKE_EMAIL", "personal@example.test")
	output, err = call("run", "--account", "personal", "--", "--model", "opus")
	if err != nil || !strings.Contains(output, "MODEL=personal/claude-test\nARG=--model\nARG=opus\n") || !strings.Contains(output, "SONNET=personal/claude-test\nOPUS=personal/claude-test\nHAIKU=personal/claude-test\nSUBAGENT=personal/claude-test\n") || !strings.Contains(output, "CREDENTIALS="+personalDir+"\n") || !strings.Contains(output, "DIR="+filepath.Join(cacheDir, "ccenv", "config-overlays")+string(filepath.Separator)) {
		t.Fatalf("split native proxy account routing: %v %s", err, output)
	}
	if err := os.WriteFile(filepath.Join(root, ".ccenv"), []byte(nativeSelector), 0600); err != nil {
		t.Fatal(err)
	}
	output, err = call("run", "--account", "personal", "--")
	if err == nil || !strings.Contains(output, "requires model and all default_models") || strings.Contains(output, "DIR=") {
		t.Fatalf("unscoped proxy account override: %v %s", err, output)
	}
	t.Setenv("FAKE_EMAIL", "other@example.test")
	output, err = call("run", "--")
	if err == nil || !strings.Contains(output, "requires the pinned claude.ai login") || strings.Contains(output, "DIR=") {
		t.Fatalf("native subscription proxy accepted another account: %v %s", err, output)
	}
	t.Setenv("FAKE_EMAIL", "work@example.test")
	t.Setenv("FAKE_AUTH_METHOD", "api_key")
	output, err = call("run", "--")
	if err == nil || !strings.Contains(output, "not logged in with a claude.ai account") || strings.Contains(output, "DIR=") {
		t.Fatalf("native subscription proxy accepted API authentication: %v %s", err, output)
	}
	t.Setenv("FAKE_AUTH_METHOD", "claude.ai")
	if err := os.WriteFile(filepath.Join(root, ".ccenv"), []byte(selector), 0600); err != nil {
		t.Fatal(err)
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

func TestProxyAccountRoutesRequireCompleteScope(t *testing.T) {
	original := proxyConfig{Model: "work/opus", DefaultModels: map[string]string{"opus": "work/opus", "sonnet": "work/sonnet", "haiku": "work/haiku"}}
	updated, err := proxyForAccount(original, "work", "personal")
	if err != nil || updated.Model != "personal/opus" || updated.DefaultModels["sonnet"] != "personal/sonnet" || original.DefaultModels["sonnet"] != "work/sonnet" {
		t.Fatalf("account route rewrite mutated selector or kept old route: %#v %v", updated, err)
	}
	for _, model := range []string{"", "sonnet", "other/sonnet", "work/"} {
		original.DefaultModels["sonnet"] = model
		if _, err := proxyForAccount(original, "work", "personal"); err == nil {
			t.Fatalf("accepted incomplete or mismatched Sonnet route %q", model)
		}
	}
}
