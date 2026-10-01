package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"syscall"
)

// Claude Desktop is an Electron app. Electron keeps the whole app state (login,
// chats, settings, connectors) in its user data directory and takes a
// single-instance lock inside it, so giving each profile its own
// --user-data-dir yields separate logins that can run side by side. The
// packaged app ignores CLAUDE_USER_DATA_DIR, so the switch is the only lever.

const (
	desktopEntryID   = "com.anthropic.Claude.desktop"
	desktopManagedBy = "X-Ccenv-Managed=true"
	launcherPrefix   = "ccenv-claude-"
)

func desktopBinary() (string, error) {
	p := os.Getenv("CCENV_DESKTOP_BIN")
	if p == "" {
		p = "claude-desktop"
	}
	if strings.ContainsRune(p, os.PathSeparator) {
		return p, nil
	}
	found, err := exec.LookPath(p)
	if err != nil {
		return "", fmt.Errorf("find Claude Desktop (set CCENV_DESKTOP_BIN to its executable): %w", err)
	}
	return found, nil
}

func xdgDir(env, fallback string) (string, error) {
	if d := os.Getenv(env); filepath.IsAbs(d) {
		return d, nil
	}
	h, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(h, fallback), nil
}

// desktopDir is the profile's Electron user data directory: an explicit
// desktop_dir from the config, or one ccenv manages under XDG_DATA_HOME.
func desktopDir(name string, p profile) (string, error) {
	if p.DesktopDir != "" {
		return expandPath(p.DesktopDir)
	}
	data, err := xdgDir("XDG_DATA_HOME", ".local/share")
	if err != nil {
		return "", err
	}
	return filepath.Join(data, "ccenv", "desktop", name), nil
}

// desktopRunning reports whether an Electron instance holds the profile's
// single-instance lock. The lock is a symlink to "<hostname>-<pid>".
func desktopRunning(dir string) bool {
	target, err := os.Readlink(filepath.Join(dir, "SingletonLock"))
	if err != nil {
		return false
	}
	i := strings.LastIndex(target, "-")
	if i < 0 {
		return false
	}
	if host, err := os.Hostname(); err == nil && target[:i] != host {
		return false
	}
	pid, err := strconv.Atoi(target[i+1:])
	if err != nil || pid <= 0 {
		return false
	}
	err = syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

func lastDesktopPath() (string, error) {
	state, err := xdgDir("XDG_STATE_HOME", ".local/state")
	if err != nil {
		return "", err
	}
	return filepath.Join(state, "ccenv", "desktop-last"), nil
}

func recordDesktopLaunch(name string) {
	p, err := lastDesktopPath()
	if err != nil {
		return
	}
	if os.MkdirAll(filepath.Dir(p), 0700) == nil {
		_ = os.WriteFile(p, []byte(name+"\n"), 0600)
	}
}

func lastDesktopLaunch() string {
	p, err := lastDesktopPath()
	if err != nil {
		return ""
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

func desktop(args []string) error {
	if len(args) > 0 {
		switch args[0] {
		case "install":
			if len(args) != 1 {
				return errors.New("usage: ccenv desktop install")
			}
			return desktopInstall()
		case "uninstall":
			if len(args) != 1 {
				return errors.New("usage: ccenv desktop uninstall")
			}
			return desktopUninstall()
		case "handle":
			return desktopHandle(args[1:])
		}
	}
	fs := flag.NewFlagSet("desktop", flag.ContinueOnError)
	name := fs.String("profile", "", "profile override")
	if err := fs.Parse(args); err != nil {
		return err
	}
	c, err := loadConfig()
	if err != nil {
		return err
	}
	s, _, err := selected(c, *name)
	if err != nil {
		return err
	}
	return launchDesktop(c, s.Name, fs.Args())
}

func launchDesktop(c config, name string, args []string) error {
	p, err := requireProfile(c, name)
	if err != nil {
		return err
	}
	for _, a := range args {
		if a == "--user-data-dir" || strings.HasPrefix(a, "--user-data-dir=") {
			return errors.New("ccenv desktop sets --user-data-dir from the profile; do not pass it")
		}
	}
	if err := refuseOverrideEnv(); err != nil {
		return err
	}
	dir, err := desktopDir(name, p)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("create desktop data directory: %w", err)
	}
	bin, err := desktopBinary()
	if err != nil {
		return err
	}
	recordDesktopLaunch(name)
	argv := append([]string{bin, "--user-data-dir=" + dir}, args...)
	// The Code tab reads CLAUDE_CONFIG_DIR for settings, plugins, and history;
	// Desktop signs Claude Code in with its own login.
	return syscall.Exec(bin, argv, withClaudeDirs(withoutSessionEnv(os.Environ()), p.Dir, p.Dir))
}

// withoutSessionEnv drops the variables a running Claude Code session exports
// to its tools. Launched from such a terminal, Desktop would otherwise pass
// them on to the sessions it starts.
func withoutSessionEnv(env []string) []string {
	session := map[string]bool{
		"CLAUDECODE": true, "CLAUDE_PID": true, "CLAUDE_EFFORT": true,
		"CLAUDE_CODE_SESSION_ID": true, "CLAUDE_CODE_ENTRYPOINT": true,
		"CLAUDE_CODE_CHILD_SESSION": true, "CLAUDE_CODE_SESSION_ATTENDED": true,
		"CLAUDE_CODE_EXECPATH": true,
	}
	result := make([]string, 0, len(env))
	for _, e := range env {
		key, _, _ := strings.Cut(e, "=")
		if !session[key] && !strings.HasPrefix(key, "CLAUDE_CODE_MESSAGING_") {
			result = append(result, e)
		}
	}
	return result
}

// desktopHandle is the Exec target of ccenv's com.anthropic.Claude.desktop.
// Without a URL it launches the selected profile. A claude:// URL (sign-in
// callbacks, "open in Desktop" links) goes to the profile that most plausibly
// asked for it: the only running one, else the last one ccenv launched.
func desktopHandle(args []string) error {
	c, err := loadConfig()
	if err != nil {
		return err
	}
	var urls []string
	for _, a := range args {
		if strings.HasPrefix(a, "claude:") {
			urls = append(urls, a)
		}
	}
	if len(urls) == 0 {
		s, _, err := selected(c, "")
		if err != nil {
			return err
		}
		return launchDesktop(c, s.Name, args)
	}
	name, why, err := routeDesktopURL(c)
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "ccenv: routing claude:// link to %s (%s)\n", name, why)
	return launchDesktop(c, name, args)
}

func routeDesktopURL(c config) (string, string, error) {
	var running []string
	for _, n := range sortedProfileNames(c) {
		if dir, err := desktopDir(n, c.Profiles[n]); err == nil && desktopRunning(dir) {
			running = append(running, n)
		}
	}
	last := lastDesktopLaunch()
	_, lastKnown := c.Profiles[last]
	switch {
	case len(running) == 1:
		return running[0], "only running profile", nil
	case len(running) > 1:
		for _, n := range running {
			if n == last {
				return n, "most recently launched of the running profiles", nil
			}
		}
		for _, n := range running {
			if n == c.Default {
				return n, "global default among the running profiles", nil
			}
		}
		return running[0], "first running profile", nil
	case lastKnown:
		return last, "most recently launched", nil
	case c.Default != "":
		if _, err := requireProfile(c, c.Default); err != nil {
			return "", "", err
		}
		return c.Default, "global default", nil
	}
	return "", "", errors.New("cannot route claude:// link: no running profile and no global default")
}

func sortedProfileNames(c config) []string {
	names := make([]string, 0, len(c.Profiles))
	for n := range c.Profiles {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

func applicationsDir() (string, error) {
	data, err := xdgDir("XDG_DATA_HOME", ".local/share")
	if err != nil {
		return "", err
	}
	return filepath.Join(data, "applications"), nil
}

// desktopExec quotes an Exec= argument per the Desktop Entry specification.
func desktopExec(arg string) string {
	if !strings.ContainsAny(arg, " \t\n\"'\\><~|&;$*?#()`%=") {
		return arg
	}
	r := strings.NewReplacer(`\`, `\\\\`, `"`, `\\"`, "`", "\\\\`", `$`, `\\$`, `%`, `%%`)
	return `"` + r.Replace(arg) + `"`
}

func isManagedEntry(path string) bool {
	b, err := os.ReadFile(path)
	return err == nil && strings.Contains(string(b), "\n"+desktopManagedBy+"\n")
}

func desktopInstall() error {
	if runtime.GOOS != "linux" {
		return errors.New("ccenv desktop install writes XDG launchers and supports Linux only")
	}
	c, err := loadConfig()
	if err != nil {
		return err
	}
	if len(c.Profiles) == 0 {
		return errors.New("no profiles registered; add one with ccenv add")
	}
	self, err := os.Executable()
	if err != nil {
		return err
	}
	if self, err = filepath.EvalSymlinks(self); err != nil {
		return err
	}
	apps, err := applicationsDir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(apps, 0755); err != nil {
		return err
	}
	handler := filepath.Join(apps, desktopEntryID)
	if _, err := os.Lstat(handler); err == nil && !isManagedEntry(handler) {
		return fmt.Errorf("%s exists and was not written by ccenv; move it aside first", handler)
	}
	// Shadow the packaged entry so the claude:// handler Desktop registers on
	// every start resolves to ccenv, which routes the link to a profile.
	if err := writeEntry(handler, fmt.Sprintf(`[Desktop Entry]
Type=Application
Name=Claude
Comment=Claude Desktop, routed to a ccenv profile
Exec=%s desktop handle %%U
Icon=claude-desktop
NoDisplay=true
StartupWMClass=com.anthropic.Claude
MimeType=x-scheme-handler/claude;
%s
`, desktopExec(self), desktopManagedBy)); err != nil {
		return err
	}
	fmt.Println("Wrote", handler)
	keep := map[string]bool{}
	for _, n := range sortedProfileNames(c) {
		file := launcherPrefix + n + ".desktop"
		keep[file] = true
		p := c.Profiles[n]
		comment := "Claude Desktop with the " + n + " profile"
		if p.Email != "" {
			comment += " (" + p.Email + ")"
		}
		path := filepath.Join(apps, file)
		if err := writeEntry(path, fmt.Sprintf(`[Desktop Entry]
Type=Application
Name=Claude (%s)
Comment=%s
Exec=%s desktop --profile %s
Icon=claude-desktop
Categories=Utility;Development;
Keywords=AI;Claude;%s;
%s
`, n, comment, desktopExec(self), n, n, desktopManagedBy)); err != nil {
			return err
		}
		fmt.Println("Wrote", path)
	}
	entries, err := os.ReadDir(apps)
	if err != nil {
		return err
	}
	for _, e := range entries {
		path := filepath.Join(apps, e.Name())
		if strings.HasPrefix(e.Name(), launcherPrefix) && !keep[e.Name()] && isManagedEntry(path) {
			if err := os.Remove(path); err != nil {
				return err
			}
			fmt.Println("Removed", path)
		}
	}
	refreshDesktopDatabase(apps)
	if xdgMime, err := exec.LookPath("xdg-mime"); err == nil {
		_ = exec.Command(xdgMime, "default", desktopEntryID, "x-scheme-handler/claude").Run()
	}
	return nil
}

func desktopUninstall() error {
	apps, err := applicationsDir()
	if err != nil {
		return err
	}
	entries, err := os.ReadDir(apps)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, e := range entries {
		path := filepath.Join(apps, e.Name())
		if (e.Name() == desktopEntryID || strings.HasPrefix(e.Name(), launcherPrefix)) && isManagedEntry(path) {
			if err := os.Remove(path); err != nil {
				return err
			}
			fmt.Println("Removed", path)
		}
	}
	refreshDesktopDatabase(apps)
	return nil
}

func writeEntry(path, content string) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(content), 0644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func refreshDesktopDatabase(apps string) {
	if bin, err := exec.LookPath("update-desktop-database"); err == nil {
		_ = exec.Command(bin, apps).Run()
	}
}

// notifyDesktopError surfaces a failed launch from a launcher or bar plugin,
// where nobody sees stderr.
func notifyDesktopError(err error) {
	if info, statErr := os.Stderr.Stat(); statErr == nil && info.Mode()&os.ModeCharDevice != 0 {
		return
	}
	if bin, lookErr := exec.LookPath("notify-send"); lookErr == nil {
		_ = exec.Command(bin, "--app-name=ccenv", "Claude Desktop did not launch", err.Error()).Run()
	}
}

type listedProfile struct {
	Name           string `json:"name"`
	Dir            string `json:"dir"`
	Email          string `json:"email"`
	OrgID          string `json:"org_id"`
	Default        bool   `json:"default"`
	DesktopDir     string `json:"desktop_dir"`
	DesktopRunning bool   `json:"desktop_running"`
}

// listJSON is the machine-readable registry view for launchers such as the
// Omarchy plugin. It reads no credentials and runs no Claude command.
func listJSON() error {
	c, err := loadConfig()
	if err != nil {
		return err
	}
	out := struct {
		Default  string          `json:"default"`
		Profiles []listedProfile `json:"profiles"`
	}{Default: c.Default, Profiles: []listedProfile{}}
	for _, n := range sortedProfileNames(c) {
		p := c.Profiles[n]
		lp := listedProfile{Name: n, Dir: p.Dir, Email: p.Email, OrgID: p.OrgID, Default: n == c.Default}
		if dir, err := desktopDir(n, p); err == nil {
			lp.DesktopDir = dir
			lp.DesktopRunning = desktopRunning(dir)
		}
		out.Profiles = append(out.Profiles, lp)
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}

// discover lists ~/.claude* directories that look like Claude Code configs but
// are not registered. It only checks file names; it never reads credentials.
func discover() error {
	c, err := loadConfig()
	if err != nil {
		return err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	registered := map[string]bool{}
	for _, p := range c.Profiles {
		if real, err := filepath.EvalSymlinks(p.Dir); err == nil {
			registered[real] = true
		}
	}
	entries, err := os.ReadDir(home)
	if err != nil {
		return err
	}
	found := 0
	for _, e := range entries {
		if !strings.HasPrefix(e.Name(), ".claude") {
			continue
		}
		dir := filepath.Join(home, e.Name())
		real, err := filepath.EvalSymlinks(dir)
		if err != nil || registered[real] {
			continue
		}
		if info, err := os.Stat(real); err != nil || !info.IsDir() || !looksLikeClaudeConfig(real) {
			continue
		}
		name := strings.TrimLeft(strings.TrimPrefix(e.Name(), ".claude"), "-_.")
		if name == "" || !validName.MatchString(name) {
			name = "NAME"
		}
		fmt.Printf("ccenv add %s %s\n", name, dir)
		found++
	}
	if found == 0 {
		fmt.Println("No unregistered Claude config directories found under", home)
	}
	return nil
}

func looksLikeClaudeConfig(dir string) bool {
	for _, marker := range []string{".credentials.json", ".claude.json", "settings.json", "projects"} {
		if _, err := os.Stat(filepath.Join(dir, marker)); err == nil {
			return true
		}
	}
	return false
}
