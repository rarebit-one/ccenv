package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// This test uses a fake Claude executable, so it exercises the compiled launcher
// and its process environment without reading real credentials or making requests.
func TestCLIWorkflow(t *testing.T) {
	root := t.TempDir()
	bin := filepath.Join(root, "ccenv")
	build := exec.Command("go", "build", "-buildvcs=false", "-o", bin, ".")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build ccenv: %v\n%s", err, output)
	}

	fakeClaude := filepath.Join(root, "fake-claude")
	script := `#!/bin/sh
if [ "$1" = auth ] && [ "$2" = status ] && [ "$3" = --json ]; then
  cat "$CLAUDE_CONFIG_DIR/status.json"
  exit $?
fi
printf 'DIR=%s\n' "$CLAUDE_CONFIG_DIR"
for arg in "$@"; do printf 'ARG=%s\n' "$arg"; done
`
	if err := os.WriteFile(fakeClaude, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	work := filepath.Join(root, "work-config")
	personal := filepath.Join(root, "personal-config")
	for _, dir := range []string{work, personal} {
		if err := os.Mkdir(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	writeStatus(t, work, "work@example.test", "work-org")
	writeStatus(t, personal, "personal@example.test", "personal-org")

	project := filepath.Join(root, "project")
	nested := filepath.Join(project, "nested")
	if err := os.MkdirAll(nested, 0700); err != nil {
		t.Fatal(err)
	}
	configFile := filepath.Join(root, "config", "config.json")
	env := testCLIEnv(configFile, fakeClaude)
	call := func(cwd string, args ...string) (string, error) {
		cmd := exec.Command(bin, args...)
		cmd.Dir = cwd
		cmd.Env = env
		output, err := cmd.CombinedOutput()
		return string(output), err
	}
	mustRun := func(cwd string, args ...string) string {
		t.Helper()
		output, err := call(cwd, args...)
		if err != nil {
			t.Fatalf("ccenv %v: %v\n%s", args, err, output)
		}
		return output
	}

	mustRun(root, "add", "work", work)
	if output := mustRun(root, "version"); !strings.HasPrefix(output, "ccenv ") {
		t.Fatalf("version output: %s", output)
	}
	mustRun(root, "add", "personal", personal)
	unauthed := filepath.Join(root, "unauthed-config")
	if err := os.Mkdir(unauthed, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(unauthed, "status.json"), []byte(`{"loggedIn":false}`), 0600); err != nil {
		t.Fatal(err)
	}
	if output, err := call(root, "add", "unauthed", unauthed); err == nil || !strings.Contains(output, "not logged in with a claude.ai account") {
		t.Fatalf("unauthenticated profile should be rejected: err=%v, output=%s", err, output)
	}
	if output, err := call(root, "current"); err == nil || !strings.Contains(output, "no global default set") {
		t.Fatalf("unconfigured fallback should fail: err=%v, output=%s", err, output)
	}
	mustRun(root, "default", "work")
	if output := mustRun(root, "current"); !strings.Contains(output, "work → "+work+" (from global default)") {
		t.Fatalf("global default: %s", output)
	}
	mustRun(project, "local", "personal")
	if value, err := os.ReadFile(filepath.Join(project, ".ccenv")); err != nil || string(value) != "personal\n" {
		t.Fatalf("local selector: %q, %v", value, err)
	}
	if output := mustRun(nested, "current"); !strings.Contains(output, "personal → "+personal) || !strings.Contains(output, filepath.Join(project, ".ccenv")) {
		t.Fatalf("parent selector: %s", output)
	}
	if output := mustRun(nested, "run", "--", "-c"); !strings.Contains(output, "DIR="+personal+"\nARG=-c\n") {
		t.Fatalf("launch profile or arguments: %s", output)
	}
	if output := mustRun(nested, "run", "--profile", "work", "--", "-p", "hello world"); !strings.Contains(output, "DIR="+work+"\nARG=-p\nARG=hello world\n") {
		t.Fatalf("explicit override: %s", output)
	}

	writeStatus(t, personal, "changed@example.test", "changed-org")
	if output, err := call(nested, "run", "--", "-c"); err == nil || !strings.Contains(output, "identity changed") || strings.Contains(output, "DIR=") {
		t.Fatalf("changed login should stop launch: err=%v, output=%s", err, output)
	}
	if output := mustRun(nested, "run", "--ignore-pin", "--", "-c"); !strings.Contains(output, "pin bypassed for personal: Claude reports changed@example.test / changed-org") || !strings.Contains(output, "DIR="+personal+"\nARG=-c\n") {
		t.Fatalf("one-run pin bypass: %s", output)
	}
	if output, err := call(nested, "run", "--", "-c"); err == nil || !strings.Contains(output, "identity changed") || strings.Contains(output, "DIR=") {
		t.Fatalf("pin bypass should not persist: err=%v, output=%s", err, output)
	}
	if err := os.WriteFile(filepath.Join(personal, "status.json"), []byte(`{"loggedIn":false}`), 0600); err != nil {
		t.Fatal(err)
	}
	if output, err := call(nested, "run", "--ignore-pin", "--", "-c"); err == nil || !strings.Contains(output, "not logged in with a claude.ai account") || strings.Contains(output, "DIR=") {
		t.Fatalf("pin bypass must still require a login: err=%v, output=%s", err, output)
	}
	writeStatus(t, personal, "changed@example.test", "changed-org")
	mustRun(root, "refresh", "personal")
	if output := mustRun(nested, "run", "--", "-c"); !strings.Contains(output, "DIR="+personal) {
		t.Fatalf("refreshed login should launch: %s", output)
	}
	twin := filepath.Join(root, "twin-config")
	if err := os.Mkdir(twin, 0700); err != nil {
		t.Fatal(err)
	}
	writeStatus(t, twin, "work@example.test", "work-org")
	mustRun(root, "add", "twin", twin)
	if output := mustRun(root, "check"); !strings.Contains(output, "SAME ACCOUNT: twin, work") {
		t.Fatalf("duplicate account report: %s", output)
	}
	writeStatus(t, twin, "different@example.test", "different-org")
	if output, err := call(root, "check"); err == nil || !strings.Contains(output, "FAIL twin") || strings.Contains(output, "SAME ACCOUNT:") {
		t.Fatalf("changed account should fail without a stale duplicate warning: err=%v, output=%s", err, output)
	}

	if err := os.WriteFile(filepath.Join(project, ".ccenv"), []byte("missing\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if output, err := call(nested, "run", "--", "-c"); err == nil || !strings.Contains(output, `unknown profile "missing"`) || strings.Contains(output, "DIR=") {
		t.Fatalf("unknown selector should stop launch: err=%v, output=%s", err, output)
	}
	if err := os.Remove(filepath.Join(project, ".ccenv")); err != nil {
		t.Fatal(err)
	}
	env = append(env, "ANTHROPIC_API_KEY=test-only")
	if output, err := call(root, "run", "--ignore-pin", "--", "-c"); err == nil || !strings.Contains(output, "ANTHROPIC_API_KEY is set") || strings.Contains(output, "DIR=") {
		t.Fatalf("API key should stop subscription launch: err=%v, output=%s", err, output)
	}
}

func writeStatus(t *testing.T, dir, email, org string) {
	t.Helper()
	data := `{"loggedIn":true,"email":"` + email + `","orgId":"` + org + `","authMethod":"claude.ai"}`
	if err := os.WriteFile(filepath.Join(dir, "status.json"), []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
}

func testCLIEnv(configFile, claudeBinary string) []string {
	env := make([]string, 0, len(os.Environ())+2)
	for _, item := range os.Environ() {
		key := strings.SplitN(item, "=", 2)[0]
		if key == "CCENV_CONFIG" || key == "CCENV_CLAUDE_BIN" || key == "CLAUDE_CONFIG_DIR" ||
			strings.HasPrefix(key, "ANTHROPIC_") || strings.HasPrefix(key, "CLAUDE_CODE_OAUTH_") ||
			strings.HasPrefix(key, "CLAUDE_CODE_USE_") {
			continue
		}
		env = append(env, item)
	}
	return append(env, "CCENV_CONFIG="+configFile, "CCENV_CLAUDE_BIN="+claudeBinary, "CLAUDE_CONFIG_DIR=/wrong-inherited-profile")
}
