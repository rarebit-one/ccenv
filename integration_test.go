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
if [ "$1" = --version ]; then
  printf '%s (Claude Code)\n' "${FAKE_CLAUDE_VERSION:-2.1.282}"
  exit 0
fi
if [ "$1" = auth ] && [ "$2" = status ] && [ "$3" = --json ]; then
  cat "$CLAUDE_CONFIG_DIR/status.json"
  exit $?
fi
printf 'DIR=%s\n' "$CLAUDE_CONFIG_DIR"
if [ "$CLAUDE_CONFIG_DIR" != "$CLAUDE_SECURESTORAGE_CONFIG_DIR" ]; then
  printf 'CREDENTIALS=%s\n' "$CLAUDE_SECURESTORAGE_CONFIG_DIR"
  printf 'isolated-cache-write\n' > "$CLAUDE_CONFIG_DIR/.claude.json"
fi
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
	shimDir := filepath.Join(root, "shims")
	if err := os.Mkdir(shimDir, 0700); err != nil {
		t.Fatal(err)
	}
	fakeMise := filepath.Join(root, "mise")
	miseScript := `#!/bin/sh
if [ "$1" = which ] && [ "$2" = claude ]; then
  printf '%s\n' "$FAKE_REAL_CLAUDE"
  exit 0
fi
CLAUDE_CONFIG_DIR=/wrong-shim-profile exec "$FAKE_REAL_CLAUDE" "$@"
`
	if err := os.WriteFile(fakeMise, []byte(miseScript), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(fakeMise, filepath.Join(shimDir, "claude")); err != nil {
		t.Fatal(err)
	}
	shimEnv := make([]string, 0, len(env)+2)
	for _, item := range env {
		if !strings.HasPrefix(item, "CCENV_CLAUDE_BIN=") && !strings.HasPrefix(item, "PATH=") {
			shimEnv = append(shimEnv, item)
		}
	}
	shimEnv = append(shimEnv, "PATH="+shimDir+string(os.PathListSeparator)+os.Getenv("PATH"), "FAKE_REAL_CLAUDE="+fakeClaude)
	shimRun := exec.Command(bin, "run", "--profile", "work", "--", "-c")
	shimRun.Dir = root
	shimRun.Env = shimEnv
	if output, err := shimRun.CombinedOutput(); err != nil || !strings.Contains(string(output), "DIR="+work+"\nARG=-c\n") {
		t.Fatalf("mise shim must not override config dir: err=%v, output=%s", err, output)
	}
	shimExplicit := exec.Command(bin, "run", "--profile", "work", "--", "-c")
	shimExplicit.Dir = root
	shimExplicit.Env = append(shimEnv, "CCENV_CLAUDE_BIN="+filepath.Join(shimDir, "claude"))
	if output, err := shimExplicit.CombinedOutput(); err != nil || !strings.Contains(string(output), "DIR="+work+"\nARG=-c\n") {
		t.Fatalf("explicit mise shim must not override config dir: err=%v, output=%s", err, output)
	}
	if output := mustRun(root, "version"); !strings.HasPrefix(output, "ccenv ") {
		t.Fatalf("version output: %s", output)
	}
	initFile := filepath.Join(root, "cc-init.sh")
	if err := os.WriteFile(initFile, []byte(mustRun(root, "init", "bash")), 0600); err != nil {
		t.Fatal(err)
	}
	fakeCCEnv := filepath.Join(shimDir, "ccenv")
	if err := os.WriteFile(fakeCCEnv, []byte("#!/bin/sh\nprintf 'ARG=%s\\n' \"$@\"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	ccEnvPath := append([]string(nil), os.Environ()...)
	ccEnvPath = append(ccEnvPath, "PATH="+shimDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	ccShell := exec.Command("bash", "-c", `source "$1"; cc --account rarebit -p 'hello world'`, "bash", initFile)
	ccShell.Dir = root
	ccShell.Env = ccEnvPath
	if output, err := ccShell.CombinedOutput(); err != nil || string(output) != "ARG=run\nARG=--account\nARG=rarebit\nARG=--\nARG=-p\nARG=hello world\n" {
		t.Fatalf("cc account shortcut: err=%v, output=%s", err, output)
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
	if err := os.WriteFile(filepath.Join(personal, ".claude.json"), []byte(`{"oauthAccount":{"emailAddress":"personal@example.test"}}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(personal, "settings.json"), []byte(`{"theme":"dark"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(personal, "projects"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(personal, ".credentials.json"), []byte("should not be shared"), 0600); err != nil {
		t.Fatal(err)
	}
	output := mustRun(nested, "run", "--profile", "personal", "--account", "work", "--", "-c")
	if !strings.Contains(output, "using personal config with work credentials; account cache is isolated") || !strings.Contains(output, "CREDENTIALS="+work+"\nARG=-c\n") {
		t.Fatalf("separate config and account: %s", output)
	}
	var overlay string
	for _, line := range strings.Split(output, "\n") {
		if strings.HasPrefix(line, "DIR=") {
			overlay = strings.TrimPrefix(line, "DIR=")
		}
	}
	if overlay == "" || overlay == personal || overlay == work {
		t.Fatalf("split launch should use its own config overlay, got %q in %s", overlay, output)
	}
	if cache, err := os.ReadFile(filepath.Join(personal, ".claude.json")); err != nil || string(cache) != `{"oauthAccount":{"emailAddress":"personal@example.test"}}` {
		t.Fatalf("split launch mutated the selected profile's account cache: %q, %v", cache, err)
	}
	for _, name := range []string{"settings.json", "projects"} {
		link, err := os.Readlink(filepath.Join(overlay, name))
		if err != nil || link != filepath.Join(personal, name) {
			t.Fatalf("split overlay should share %s: link=%q err=%v", name, link, err)
		}
	}
	if _, err := os.Lstat(filepath.Join(overlay, ".credentials.json")); !os.IsNotExist(err) {
		t.Fatalf("split overlay must not expose a config-directory credential file: %v", err)
	}
	if output, err := call(nested, "run", "--profile", "personal", "--account", "missing", "--", "-c"); err == nil || !strings.Contains(output, `unknown profile "missing"`) || strings.Contains(output, "DIR=") {
		t.Fatalf("unregistered account should stop launch: err=%v, output=%s", err, output)
	}
	oldVersionEnv := append([]string(nil), env...)
	oldVersionEnv = append(oldVersionEnv, "FAKE_CLAUDE_VERSION=2.1.214")
	oldVersion := exec.Command(bin, "run", "--profile", "personal", "--account", "work", "--", "-c")
	oldVersion.Dir = nested
	oldVersion.Env = oldVersionEnv
	if output, err := oldVersion.CombinedOutput(); err == nil || !strings.Contains(string(output), "requires Claude Code 2.1.215 or newer") || strings.Contains(string(output), "DIR=") {
		t.Fatalf("older Claude must reject separate credentials: err=%v, output=%s", err, output)
	}

	writeStatus(t, personal, "changed@example.test", "changed-org")
	if output, err := call(nested, "run", "--", "-c"); err == nil || !strings.Contains(output, "identity changed") || strings.Contains(output, "DIR=") {
		t.Fatalf("changed login should stop launch: err=%v, output=%s", err, output)
	}
	if output := mustRun(nested, "run", "--ignore-pin", "--", "-c"); !strings.Contains(output, "pin bypassed for account personal: Claude reports changed@example.test / changed-org") || !strings.Contains(output, "DIR="+personal+"\nARG=-c\n") {
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
		if key == "CCENV_CONFIG" || key == "CCENV_CLAUDE_BIN" || key == "CLAUDE_CONFIG_DIR" || key == "CLAUDE_SECURESTORAGE_CONFIG_DIR" || key == "FAKE_CLAUDE_VERSION" || key == "XDG_CACHE_HOME" ||
			strings.HasPrefix(key, "ANTHROPIC_") || strings.HasPrefix(key, "CLAUDE_CODE_OAUTH_") ||
			strings.HasPrefix(key, "CLAUDE_CODE_USE_") {
			continue
		}
		env = append(env, item)
	}
	return append(env, "CCENV_CONFIG="+configFile, "CCENV_CLAUDE_BIN="+claudeBinary, "CLAUDE_CONFIG_DIR=/wrong-inherited-profile", "XDG_CACHE_HOME="+filepath.Join(filepath.Dir(filepath.Dir(configFile)), "cache"))
}
