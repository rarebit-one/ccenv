package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime/debug"
	"sort"
	"strings"
	"syscall"
	"time"
)

type profile struct {
	Dir   string `json:"dir"`
	Email string `json:"email"`
	OrgID string `json:"org_id"`
}

type config struct {
	Default  string             `json:"default"`
	Profiles map[string]profile `json:"profiles"`
}

type authStatus struct {
	LoggedIn   bool   `json:"loggedIn"`
	Email      string `json:"email"`
	OrgID      string `json:"orgId"`
	AuthMethod string `json:"authMethod"`
}

type selection struct {
	Name   string
	Source string
}

var validName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

// GoReleaser sets version for release binaries. Go-installed binaries get their
// module version from Go build information instead.
var version = "dev"

func buildVersion() string {
	if version != "dev" {
		return version
	}
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
		return strings.TrimPrefix(info.Main.Version, "v")
	}
	return version
}

func main() {
	if err := dispatch(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "ccenv:", err)
		os.Exit(1)
	}
}

func dispatch(args []string) error {
	if len(args) == 0 {
		usage()
		return nil
	}
	switch args[0] {
	case "version", "--version":
		if len(args) != 1 {
			return errors.New("usage: ccenv version")
		}
		fmt.Println("ccenv", buildVersion())
		return nil
	case "init":
		if len(args) != 2 || args[1] != "bash" {
			return errors.New("usage: ccenv init bash")
		}
		fmt.Println(`cc() { command ccenv run -- "$@"; }`)
		return nil
	case "add":
		if len(args) != 3 || !validName.MatchString(args[1]) {
			return errors.New("usage: ccenv add <name> <existing-config-dir>")
		}
		return add(args[1], args[2])
	case "refresh":
		if len(args) != 2 {
			return errors.New("usage: ccenv refresh <name>")
		}
		return refresh(args[1])
	case "default":
		if len(args) != 2 {
			return errors.New("usage: ccenv default <name>")
		}
		return setDefault(args[1])
	case "local":
		if len(args) != 2 {
			return errors.New("usage: ccenv local <name>")
		}
		return setLocal(args[1])
	case "current":
		if len(args) != 1 {
			return errors.New("usage: ccenv current")
		}
		return current()
	case "list":
		if len(args) != 1 {
			return errors.New("usage: ccenv list")
		}
		return list()
	case "check":
		if len(args) != 1 {
			return errors.New("usage: ccenv check")
		}
		return check()
	case "run":
		return run(args[1:])
	case "help", "-h", "--help":
		usage()
		return nil
	default:
		return fmt.Errorf("unknown command %q (try ccenv help)", args[0])
	}
}

func usage() {
	fmt.Print(`ccenv selects a Claude Code login from the nearest .ccenv or a global default.

  ccenv add <name> <existing-config-dir>  Register and pin a logged-in profile
  ccenv refresh <name>                    Pin the profile's current login after re-authentication
  ccenv default <name>                    Set the global fallback
  ccenv local <name>                      Write .ccenv in the current directory
  ccenv current                           Show the selected profile and source
  ccenv list                              List registered profiles
  ccenv check                             Verify logins and flag duplicate accounts
  ccenv run [--profile <name>] [--ignore-pin] -- [args]
                                           Launch Claude Code
  ccenv init bash                         Print the interactive cc shell function
  ccenv version                           Print the installed version
`)
}

func configPath() (string, error) {
	if p := os.Getenv("CCENV_CONFIG"); p != "" {
		return filepath.Abs(p)
	}
	d, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(d, "ccenv", "config.json"), nil
}

func loadConfig() (config, error) {
	c := config{Profiles: map[string]profile{}}
	p, err := configPath()
	if err != nil {
		return c, err
	}
	b, err := os.ReadFile(p)
	if errors.Is(err, os.ErrNotExist) {
		return c, nil
	}
	if err != nil {
		return c, err
	}
	if err := json.Unmarshal(b, &c); err != nil {
		return c, fmt.Errorf("read %s: %w", p, err)
	}
	if c.Profiles == nil {
		c.Profiles = map[string]profile{}
	}
	return c, nil
}

func saveConfig(c config) error {
	p, err := configPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	f, err := os.CreateTemp(filepath.Dir(p), ".config-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if err := f.Chmod(0600); err != nil {
		f.Close()
		return err
	}
	if _, err := f.Write(b); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), p)
}

func expandPath(p string) (string, error) {
	if p == "~" || strings.HasPrefix(p, "~/") {
		h, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		p = filepath.Join(h, strings.TrimPrefix(p, "~/"))
	}
	return filepath.Abs(p)
}

func claudeBinary() (string, error) {
	p := os.Getenv("CCENV_CLAUDE_BIN")
	if p == "" {
		p = "claude"
	}
	if !strings.ContainsRune(p, os.PathSeparator) {
		var err error
		p, err = exec.LookPath(p)
		if err != nil {
			return "", err
		}
	}
	resolved, err := filepath.EvalSymlinks(p)
	if err != nil {
		return "", err
	}
	if filepath.Base(resolved) != "mise" {
		return p, nil
	}
	// A mise shim reapplies the project's environment when invoked, which can
	// replace our selected CLAUDE_CONFIG_DIR. Resolve the installed tool first.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, resolved, "which", "claude").Output()
	if err != nil {
		return "", fmt.Errorf("resolve Claude Code through mise: %w", err)
	}
	real := strings.TrimSpace(string(out))
	if !filepath.IsAbs(real) {
		return "", fmt.Errorf("mise returned a non-absolute Claude Code path %q", real)
	}
	if _, err := exec.LookPath(real); err != nil {
		return "", fmt.Errorf("mise returned unusable Claude Code path %q: %w", real, err)
	}
	realTarget, err := filepath.EvalSymlinks(real)
	if err != nil {
		return "", err
	}
	if realTarget == resolved {
		return "", fmt.Errorf("mise returned its own Claude Code shim %q", real)
	}
	return real, nil
}

func withConfigDir(env []string, dir string) []string {
	result := make([]string, 0, len(env)+1)
	for _, e := range env {
		if !strings.HasPrefix(e, "CLAUDE_CONFIG_DIR=") {
			result = append(result, e)
		}
	}
	return append(result, "CLAUDE_CONFIG_DIR="+dir)
}

func getAuth(dir string) (authStatus, error) {
	var s authStatus
	bin, err := claudeBinary()
	if err != nil {
		return s, fmt.Errorf("find Claude Code: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "auth", "status", "--json")
	cmd.Env = withConfigDir(os.Environ(), dir)
	b, err := cmd.Output()
	if ctx.Err() != nil {
		return s, fmt.Errorf("claude auth status timed out for %s", dir)
	}
	if err != nil {
		return s, fmt.Errorf("claude auth status for %s: %w", dir, err)
	}
	if err := json.Unmarshal(b, &s); err != nil {
		return s, fmt.Errorf("parse Claude auth status: %w", err)
	}
	if !s.LoggedIn || s.Email == "" || s.OrgID == "" || s.AuthMethod != "claude.ai" {
		return s, fmt.Errorf("%s is not logged in with a claude.ai account", dir)
	}
	return s, nil
}

func add(name, dir string) error {
	c, err := loadConfig()
	if err != nil {
		return err
	}
	if _, exists := c.Profiles[name]; exists {
		return fmt.Errorf("profile %q already exists; edit the config file to update it", name)
	}
	dir, err = expandPath(dir)
	if err != nil {
		return err
	}
	info, err := os.Stat(dir)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("%s is not a directory", dir)
	}
	s, err := getAuth(dir)
	if err != nil {
		return err
	}
	c.Profiles[name] = profile{Dir: dir, Email: s.Email, OrgID: s.OrgID}
	if err := saveConfig(c); err != nil {
		return err
	}
	fmt.Printf("Added %s → %s (%s, org %s)\n", name, dir, s.Email, s.OrgID)
	return nil
}

func refresh(name string) error {
	c, err := loadConfig()
	if err != nil {
		return err
	}
	p, err := requireProfile(c, name)
	if err != nil {
		return err
	}
	s, err := getAuth(p.Dir)
	if err != nil {
		return err
	}
	p.Email, p.OrgID = s.Email, s.OrgID
	c.Profiles[name] = p
	if err := saveConfig(c); err != nil {
		return err
	}
	fmt.Printf("Refreshed %s: %s, org %s\n", name, p.Email, p.OrgID)
	return nil
}

func requireProfile(c config, name string) (profile, error) {
	p, ok := c.Profiles[name]
	if !ok || !validName.MatchString(name) || p.Dir == "" {
		return p, fmt.Errorf("unknown profile %q; register it with ccenv add", name)
	}
	return p, nil
}

func setDefault(name string) error {
	c, err := loadConfig()
	if err != nil {
		return err
	}
	if _, err := requireProfile(c, name); err != nil {
		return err
	}
	c.Default = name
	if err := saveConfig(c); err != nil {
		return err
	}
	fmt.Println("Default:", name)
	return nil
}

func setLocal(name string) error {
	c, err := loadConfig()
	if err != nil {
		return err
	}
	if _, err := requireProfile(c, name); err != nil {
		return err
	}
	if err := os.WriteFile(".ccenv", []byte(name+"\n"), 0644); err != nil {
		return err
	}
	fmt.Println("Wrote .ccenv:", name)
	return nil
}

func parseDotfile(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	name := ""
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(strings.SplitN(line, "#", 2)[0])
		if line == "" {
			continue
		}
		if name != "" || !validName.MatchString(line) {
			return "", fmt.Errorf("invalid .ccenv at %s: expected one profile name", path)
		}
		name = line
	}
	if name == "" {
		return "", fmt.Errorf("empty .ccenv at %s", path)
	}
	return name, nil
}

func selectProfile(c config, cwd, override string) (selection, error) {
	if override != "" {
		if _, err := requireProfile(c, override); err != nil {
			return selection{}, err
		}
		return selection{override, "--profile"}, nil
	}
	dir, err := filepath.Abs(cwd)
	if err != nil {
		return selection{}, err
	}
	for {
		path := filepath.Join(dir, ".ccenv")
		if _, err := os.Stat(path); err == nil {
			name, err := parseDotfile(path)
			if err != nil {
				return selection{}, err
			}
			if _, err := requireProfile(c, name); err != nil {
				return selection{}, fmt.Errorf("%s: %w", path, err)
			}
			return selection{name, path}, nil
		} else if !errors.Is(err, os.ErrNotExist) {
			return selection{}, err
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	if c.Default == "" {
		return selection{}, errors.New("no .ccenv found and no global default set")
	}
	if _, err := requireProfile(c, c.Default); err != nil {
		return selection{}, err
	}
	return selection{c.Default, "global default"}, nil
}

func selected(c config, override string) (selection, profile, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return selection{}, profile{}, err
	}
	s, err := selectProfile(c, cwd, override)
	if err != nil {
		return s, profile{}, err
	}
	p, err := requireProfile(c, s.Name)
	return s, p, err
}

func current() error {
	c, err := loadConfig()
	if err != nil {
		return err
	}
	s, p, err := selected(c, "")
	if err != nil {
		return err
	}
	fmt.Printf("%s → %s (from %s)\n", s.Name, p.Dir, s.Source)
	return nil
}

func list() error {
	c, err := loadConfig()
	if err != nil {
		return err
	}
	names := make([]string, 0, len(c.Profiles))
	for n := range c.Profiles {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		mark := " "
		if n == c.Default {
			mark = "*"
		}
		fmt.Printf("%s %-16s %s\n", mark, n, c.Profiles[n].Dir)
	}
	return nil
}

func verify(name string, p profile) error {
	s, err := getAuth(p.Dir)
	if err != nil {
		return err
	}
	if s.Email != p.Email || s.OrgID != p.OrgID {
		return fmt.Errorf("%s identity changed: expected %s / %s, found %s / %s", name, p.Email, p.OrgID, s.Email, s.OrgID)
	}
	return nil
}

func check() error {
	c, err := loadConfig()
	if err != nil {
		return err
	}
	names := make([]string, 0, len(c.Profiles))
	for n := range c.Profiles {
		names = append(names, n)
	}
	sort.Strings(names)
	identities := map[string][]string{}
	failed := false
	for _, n := range names {
		p := c.Profiles[n]
		if err := verify(n, p); err != nil {
			fmt.Printf("FAIL %-16s %v\n", n, err)
			failed = true
		} else {
			fmt.Printf("OK   %-16s %s\n", n, p.Email)
			key := p.Email + "\x00" + p.OrgID
			identities[key] = append(identities[key], n)
		}
	}
	keys := make([]string, 0, len(identities))
	for key := range identities {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		group := identities[key]
		if len(group) > 1 {
			fmt.Printf("SAME ACCOUNT: %s\n", strings.Join(group, ", "))
		}
	}
	if failed {
		return errors.New("one or more profiles failed verification")
	}
	return nil
}

func run(args []string) error {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	name := fs.String("profile", "", "profile override")
	ignorePin := fs.Bool("ignore-pin", false, "bypass the saved account identity for this launch")
	if err := fs.Parse(args); err != nil {
		return err
	}
	c, err := loadConfig()
	if err != nil {
		return err
	}
	s, p, err := selected(c, *name)
	if err != nil {
		return err
	}
	for _, key := range []string{"ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN", "ANTHROPIC_BASE_URL", "CLAUDE_CODE_OAUTH_TOKEN", "CLAUDE_CODE_OAUTH_REFRESH_TOKEN", "CLAUDE_CODE_USE_BEDROCK", "CLAUDE_CODE_USE_VERTEX", "CLAUDE_CODE_USE_FOUNDRY", "CLAUDE_CODE_USE_MANTLE"} {
		if os.Getenv(key) != "" {
			return fmt.Errorf("%s is set and may override the subscription; unset it before running cc", key)
		}
	}
	auth, err := getAuth(p.Dir)
	if err != nil {
		return err
	}
	if *ignorePin {
		fmt.Fprintf(os.Stderr, "ccenv: pin bypassed for %s: Claude reports %s / %s for this launch\n", s.Name, auth.Email, auth.OrgID)
	} else if auth.Email != p.Email || auth.OrgID != p.OrgID {
		return fmt.Errorf("%s identity changed: expected %s / %s, found %s / %s", s.Name, p.Email, p.OrgID, auth.Email, auth.OrgID)
	}
	bin, err := claudeBinary()
	if err != nil {
		return err
	}
	return syscall.Exec(bin, append([]string{bin}, fs.Args()...), withConfigDir(os.Environ(), p.Dir))
}
