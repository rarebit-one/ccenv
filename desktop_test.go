package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func lockAs(t *testing.T, dir string, pid int) {
	t.Helper()
	host, err := os.Hostname()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(fmt.Sprintf("%s-%d", host, pid), filepath.Join(dir, "SingletonLock")); err != nil {
		t.Fatal(err)
	}
}

func TestDesktopRunningReadsSingletonLock(t *testing.T) {
	root := t.TempDir()
	live := filepath.Join(root, "live")
	lockAs(t, live, os.Getpid())
	if !desktopRunning(live) {
		t.Fatal("expected a lock held by this process to count as running")
	}
	stale := filepath.Join(root, "stale")
	lockAs(t, stale, 1<<22+12345)
	if desktopRunning(stale) {
		t.Fatal("a lock naming a dead pid must not count as running")
	}
	if desktopRunning(filepath.Join(root, "missing")) {
		t.Fatal("a directory without a lock must not count as running")
	}
}

func TestRouteDesktopURL(t *testing.T) {
	data := t.TempDir()
	t.Setenv("XDG_DATA_HOME", data)
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	c := config{Default: "family", Profiles: map[string]profile{
		"family":   {Dir: "/tmp/family"},
		"rarebit":  {Dir: "/tmp/rarebit"},
		"sidekick": {Dir: "/tmp/sidekick"},
	}}
	route := func() string {
		t.Helper()
		name, _, err := routeDesktopURL(c)
		if err != nil {
			t.Fatal(err)
		}
		return name
	}
	if got := route(); got != "family" {
		t.Fatalf("nothing running, nothing launched: got %s, want the default", got)
	}
	recordDesktopLaunch("sidekick")
	if got := route(); got != "sidekick" {
		t.Fatalf("nothing running: got %s, want the last launch", got)
	}
	dir := func(n string) string { return filepath.Join(data, "ccenv", "desktop", n) }
	lockAs(t, dir("rarebit"), os.Getpid())
	if got := route(); got != "rarebit" {
		t.Fatalf("one running: got %s, want it", got)
	}
	lockAs(t, dir("family"), os.Getpid())
	if got := route(); got != "family" {
		t.Fatalf("two running, last launch not among them: got %s, want the default", got)
	}
	recordDesktopLaunch("rarebit")
	if got := route(); got != "rarebit" {
		t.Fatalf("two running: got %s, want the last launch", got)
	}
}

func TestDesktopExecQuoting(t *testing.T) {
	if got := desktopExec("/usr/bin/ccenv"); got != "/usr/bin/ccenv" {
		t.Fatalf("plain path: %s", got)
	}
	if got := desktopExec(`/home/a b/$x`); got != `"/home/a b/\\$x"` {
		t.Fatalf("quoted path: %s", got)
	}
}

// The integration test drives the compiled binary against a fake Desktop
// executable and throwaway XDG directories.
func TestDesktopCLI(t *testing.T) {
	root := t.TempDir()
	bin := filepath.Join(root, "ccenv")
	if out, err := exec.Command("go", "build", "-buildvcs=false", "-o", bin, ".").CombinedOutput(); err != nil {
		t.Fatalf("build ccenv: %v\n%s", err, out)
	}
	fakeDesktop := filepath.Join(root, "fake-desktop")
	// The fake records what it received in $FAKE_OUT, written whole by rename:
	// on macOS ccenv starts Desktop detached, so the test polls for the file.
	script := `#!/bin/sh
{
  printf 'CONFIG=%s\n' "$CLAUDE_CONFIG_DIR"
  env | grep -E '^(CLAUDECODE|CLAUDE_CODE_SESSION_ID|CLAUDE_CODE_MESSAGING_TOKEN)=' | sed 's/^/LEAK=/'
  for a in "$@"; do printf 'ARG=%s\n' "$a"; done
} > "$FAKE_OUT.tmp" && mv "$FAKE_OUT.tmp" "$FAKE_OUT"
`
	if err := os.WriteFile(fakeDesktop, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	configFile := filepath.Join(root, "config.json")
	data := filepath.Join(root, "data")
	t.Setenv("CCENV_CONFIG", configFile)
	c := config{Default: "work", Profiles: map[string]profile{
		"work":     {Dir: filepath.Join(root, "work"), Email: "work@example.test", OrgID: "w"},
		"personal": {Dir: filepath.Join(root, "personal"), Email: "me@example.test", OrgID: "p", DesktopDir: filepath.Join(root, "custom")},
	}}
	if err := saveConfig(c); err != nil {
		t.Fatal(err)
	}
	env := []string{
		"HOME=" + root,
		"PATH=" + os.Getenv("PATH"),
		"CCENV_CONFIG=" + configFile,
		"CCENV_DESKTOP_BIN=" + fakeDesktop,
		"XDG_DATA_HOME=" + data,
		"XDG_STATE_HOME=" + filepath.Join(root, "state"),
		"XDG_CONFIG_HOME=" + filepath.Join(root, "xdg-config"),
		"FAKE_OUT=" + filepath.Join(root, "desktop.out"),
	}
	call := func(extra []string, args ...string) (string, error) {
		cmd := exec.Command(bin, args...)
		cmd.Dir = root
		cmd.Env = append(append([]string{}, env...), extra...)
		out, err := cmd.CombinedOutput()
		return string(out), err
	}
	// launch returns ccenv's own output and what the fake Desktop received.
	launch := func(extra []string, args ...string) (string, string, error) {
		t.Helper()
		outFile := filepath.Join(root, "desktop.out")
		_ = os.Remove(outFile)
		out, err := call(extra, args...)
		if err != nil {
			return out, "", err
		}
		for i := 0; i < 100; i++ {
			if b, readErr := os.ReadFile(outFile); readErr == nil {
				return out, string(b), nil
			}
			time.Sleep(50 * time.Millisecond)
		}
		t.Fatalf("Desktop was not started by ccenv %v:\n%s", args, out)
		return "", "", nil
	}

	_, got, err := launch([]string{"CLAUDECODE=1", "CLAUDE_CODE_SESSION_ID=x", "CLAUDE_CODE_MESSAGING_TOKEN=t"}, "desktop", "--", "claude://code/new")
	want := "CONFIG=" + filepath.Join(root, "work") + "\nARG=--user-data-dir=" + filepath.Join(data, "ccenv", "desktop", "work") + "\nARG=claude://code/new\n"
	if err != nil || got != want {
		t.Fatalf("default launch: %v\n%s\nwant:\n%s", err, got, want)
	}
	out, got, err := launch(nil, "desktop", "--profile", "personal")
	if err != nil || !strings.Contains(got, "ARG=--user-data-dir="+filepath.Join(root, "custom")+"\n") {
		t.Fatalf("explicit desktop_dir: %v\n%s%s", err, out, got)
	}
	if out, err = call(nil, "desktop", "--", "--user-data-dir=/elsewhere"); err == nil {
		t.Fatalf("a caller-supplied --user-data-dir must be refused:\n%s", out)
	}
	if out, err = call([]string{"ANTHROPIC_API_KEY=x"}, "desktop"); err == nil || !strings.Contains(out, "ANTHROPIC_API_KEY") {
		t.Fatalf("override env must be refused: %v\n%s", err, out)
	}
	// personal was launched last, so an incoming sign-in link goes there.
	out, got, err = launch(nil, "desktop", "handle", "claude://claude.ai/magic-link#x")
	if err != nil || !strings.Contains(out, "routing claude:// link to personal") || !strings.Contains(got, "ARG=claude://claude.ai/magic-link#x\n") {
		t.Fatalf("handle routing: %v\n%s%s", err, out, got)
	}

	out, err = call(nil, "list", "--json")
	if err != nil {
		t.Fatalf("list --json: %v\n%s", err, out)
	}
	var listed struct {
		Default  string          `json:"default"`
		Profiles []listedProfile `json:"profiles"`
	}
	if err := json.Unmarshal([]byte(out), &listed); err != nil || listed.Default != "work" || len(listed.Profiles) != 2 ||
		listed.Profiles[0].Name != "personal" || !listed.Profiles[1].Default || listed.Profiles[1].Email != "work@example.test" {
		t.Fatalf("list --json: %v\n%s", err, out)
	}

	if runtime.GOOS != "linux" {
		for _, args := range [][]string{{"desktop", "install"}, {"omarchy", "install", "--dir", filepath.Join(root, "plugins")}} {
			if out, err = call(nil, args...); err == nil || !strings.Contains(out, "Linux only") {
				t.Fatalf("%v must refuse off Linux: %v\n%s", args, err, out)
			}
		}
		return
	}
	apps := filepath.Join(data, "applications")
	if out, err = call(nil, "desktop", "install"); err != nil {
		t.Fatalf("install: %v\n%s", err, out)
	}
	for _, f := range []string{desktopEntryID, "ccenv-claude-work.desktop", "ccenv-claude-personal.desktop"} {
		b, err := os.ReadFile(filepath.Join(apps, f))
		if err != nil || !strings.Contains(string(b), desktopManagedBy) {
			t.Fatalf("%s: %v\n%s", f, err, b)
		}
	}
	if b, _ := os.ReadFile(filepath.Join(apps, desktopEntryID)); !strings.Contains(string(b), "Exec="+bin+" desktop handle %U\n") {
		t.Fatalf("handler Exec:\n%s", b)
	}
	stale := filepath.Join(apps, "ccenv-claude-gone.desktop")
	if err := os.WriteFile(stale, []byte("[Desktop Entry]\n"+desktopManagedBy+"\n"), 0644); err != nil {
		t.Fatal(err)
	}
	foreign := filepath.Join(apps, "ccenv-claude-mine.desktop")
	if err := os.WriteFile(foreign, []byte("[Desktop Entry]\nName=mine\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if out, err = call(nil, "desktop", "install"); err != nil {
		t.Fatalf("reinstall: %v\n%s", err, out)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatal("install must remove launchers for unregistered profiles")
	}
	if out, err = call(nil, "desktop", "uninstall"); err != nil {
		t.Fatalf("uninstall: %v\n%s", err, out)
	}
	if _, err := os.Stat(foreign); err != nil {
		t.Fatal("uninstall must leave entries ccenv did not write")
	}
	if _, err := os.Stat(filepath.Join(apps, desktopEntryID)); !os.IsNotExist(err) {
		t.Fatal("uninstall must remove the claude:// handler")
	}
	if err := os.WriteFile(filepath.Join(apps, desktopEntryID), []byte("[Desktop Entry]\nName=user's own\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if out, err = call(nil, "desktop", "install"); err == nil {
		t.Fatalf("install must not overwrite a handler it did not write:\n%s", out)
	}

	plugins := filepath.Join(root, "omarchy", "plugins")
	if out, err = call(nil, "omarchy", "install", "--dir", plugins); err != nil || !strings.Contains(out, "omarchy plugin enable") {
		t.Fatalf("omarchy install: %v\n%s", err, out)
	}
	if err := os.WriteFile(filepath.Join(root, "omarchy", "shell.json"), []byte(`{"plugins":["rarebit.ccenv"]}`), 0644); err != nil {
		t.Fatal(err)
	}
	if out, err = call(nil, "omarchy", "install", "--dir", plugins); err != nil || strings.Contains(out, "omarchy plugin enable") || !strings.Contains(out, "omarchy-restart-shell") {
		t.Fatalf("omarchy reinstall of an enabled plugin: %v\n%s", err, out)
	}
	for _, f := range []string{"manifest.json", "BarWidget.qml", "Panel.qml"} {
		if _, err := os.Stat(filepath.Join(plugins, omarchyPluginID, f)); err != nil {
			t.Fatal(err)
		}
	}
	if b, _ := os.ReadFile(filepath.Join(plugins, omarchyPluginID, "Ccenv.js")); !strings.Contains(string(b), `var BIN = "`+bin+`"`) {
		t.Fatalf("Ccenv.js must pin the binary:\n%s", b)
	}
}

func TestDiscoverSkipsRegisteredAndNonConfigDirs(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CCENV_CONFIG", filepath.Join(home, "config.json"))
	for dir, marker := range map[string]string{".claude-work": "settings.json", ".claude-known": ".claude.json", ".claude-empty": ""} {
		if err := os.MkdirAll(filepath.Join(home, dir), 0700); err != nil {
			t.Fatal(err)
		}
		if marker != "" {
			if err := os.WriteFile(filepath.Join(home, dir, marker), []byte("{}"), 0600); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := saveConfig(config{Profiles: map[string]profile{"known": {Dir: filepath.Join(home, ".claude-known")}}}); err != nil {
		t.Fatal(err)
	}
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout := os.Stdout
	os.Stdout = w
	err = discover()
	os.Stdout = stdout
	w.Close()
	if err != nil {
		t.Fatal(err)
	}
	b := make([]byte, 4096)
	n, _ := r.Read(b)
	if got, want := string(b[:n]), "ccenv add work "+filepath.Join(home, ".claude-work")+"\n"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestDesktopBinaryFindsClaudeAppOnMac(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("macOS app bundle lookup")
	}
	if _, err := os.Stat("/Applications/Claude.app"); err == nil {
		t.Skip("a system Claude.app would take precedence")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CCENV_DESKTOP_BIN", "")
	if _, err := desktopBinary(); err == nil || !strings.Contains(err.Error(), "CCENV_DESKTOP_BIN") {
		t.Fatalf("missing app must point at CCENV_DESKTOP_BIN, got %v", err)
	}
	app := filepath.Join(home, "Applications/Claude.app/Contents/MacOS/Claude")
	if err := os.MkdirAll(filepath.Dir(app), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(app, []byte("#!/bin/sh\n"), 0755); err != nil {
		t.Fatal(err)
	}
	if got, err := desktopBinary(); err != nil || got != app {
		t.Fatalf("got %q, %v; want %q", got, err, app)
	}
}
