package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
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
	"strconv"
	"strings"
	"syscall"
	"time"
)

type profile struct {
	Dir        string `json:"dir"`
	Email      string `json:"email"`
	OrgID      string `json:"org_id"`
	DesktopDir string `json:"desktop_dir,omitempty"`
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
	Proxy  *proxyConfig
}

var validName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)
var claudeVersionRE = regexp.MustCompile(`(?m)^([0-9]+)\.([0-9]+)\.([0-9]+)`)

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
		if len(os.Args) > 1 && os.Args[1] == "desktop" {
			notifyDesktopError(err)
		}
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
		fmt.Println(`cc() {
  local -a ccenv_args=()
  while (($#)); do
    case "${1-}" in
      --profile|--account)
        if (($# < 2)); then
          printf 'usage: cc [--profile <name>] [--account <name>] [--ignore-pin] [--direct] [--] [CLAUDE_ARGS...]\n' >&2
          return 2
        fi
        ccenv_args+=("$1" "$2")
        shift 2
        ;;
      --ignore-pin|--direct)
        ccenv_args+=("$1")
        shift
        ;;
      --)
        shift
        break
        ;;
      *)
        break
        ;;
    esac
  done
  command ccenv run "${ccenv_args[@]}" -- "$@"
}`)
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
		return local(args[1:])
	case "current":
		if len(args) != 1 {
			return errors.New("usage: ccenv current")
		}
		return current()
	case "list":
		if len(args) == 2 && args[1] == "--json" {
			return listJSON()
		}
		if len(args) != 1 {
			return errors.New("usage: ccenv list [--json]")
		}
		return list()
	case "discover":
		if len(args) != 1 {
			return errors.New("usage: ccenv discover")
		}
		return discover()
	case "desktop":
		return desktop(args[1:])
	case "omarchy":
		return omarchy(args[1:])
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
  ccenv local <name> [--proxy-url URL --api-key-file FILE] [--model MODEL]
                                           Write folder settings in .ccenv
  ccenv current                           Show the selected profile and source
  ccenv list [--json]                     List registered profiles
  ccenv discover                          Find unregistered Claude config directories
  ccenv check                             Verify logins and flag duplicate accounts
  ccenv run [--profile <name>] [--account <name>] [--ignore-pin] [--direct] -- [args]
                                           Launch Claude Code
  ccenv desktop [--profile <name>] [-- args]
                                           Launch Claude Desktop with the profile's own app data
  ccenv desktop install | uninstall       Manage per-profile app launchers and claude:// routing
  ccenv omarchy install                   Install the Omarchy bar plugin (rarebit.ccenv)
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

// isDefaultConfigDir reports whether dir is Claude Code's default ~/.claude.
// Claude keeps that directory's account cache at ~/.claude.json, outside it,
// but reads <dir>/.claude.json once CLAUDE_CONFIG_DIR names any directory,
// so the default directory must be selected by leaving the variable unset.
func isDefaultConfigDir(dir string) bool {
	home, err := os.UserHomeDir()
	if err != nil {
		return false
	}
	def := filepath.Join(home, ".claude")
	if filepath.Clean(dir) == def {
		return true
	}
	a, errA := filepath.EvalSymlinks(dir)
	b, errB := filepath.EvalSymlinks(def)
	return errA == nil && errB == nil && a == b
}

func withClaudeDirs(env []string, configDir, credentialsDir string) []string {
	result := make([]string, 0, len(env)+2)
	for _, e := range env {
		if !strings.HasPrefix(e, "CLAUDE_CONFIG_DIR=") && !strings.HasPrefix(e, "CLAUDE_SECURESTORAGE_CONFIG_DIR=") {
			result = append(result, e)
		}
	}
	if isDefaultConfigDir(configDir) && isDefaultConfigDir(credentialsDir) {
		return result
	}
	return append(result, "CLAUDE_CONFIG_DIR="+configDir, "CLAUDE_SECURESTORAGE_CONFIG_DIR="+credentialsDir)
}

// splitConfigDir creates a per-config/per-account view of the config directory.
// Claude writes the active account identity cache to .claude.json even when its
// credentials come from a separate store. Keep that mutable cache private to
// the split, seed it from the credential account, and share the user's config.
func splitConfigDir(configDir, credentialsDir string) (string, error) {
	configDir, err := filepath.Abs(configDir)
	if err != nil {
		return "", err
	}
	credentialsDir, err = filepath.Abs(credentialsDir)
	if err != nil {
		return "", err
	}
	cacheDir, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("find ccenv cache directory: %w", err)
	}
	hash := sha256.Sum256([]byte(configDir + "\x00" + credentialsDir))
	overlay := filepath.Join(cacheDir, "ccenv", "config-overlays", hex.EncodeToString(hash[:16]))
	if err := os.MkdirAll(overlay, 0700); err != nil {
		return "", fmt.Errorf("create split config overlay: %w", err)
	}
	if err := os.Chmod(overlay, 0700); err != nil {
		return "", fmt.Errorf("secure split config overlay: %w", err)
	}
	entries, err := os.ReadDir(configDir)
	if err != nil {
		return "", fmt.Errorf("read config directory %s: %w", configDir, err)
	}
	for _, entry := range entries {
		name := entry.Name()
		if strings.HasPrefix(name, ".claude.json") || strings.HasPrefix(name, ".credentials.json") {
			continue
		}
		source := filepath.Join(configDir, name)
		dest := filepath.Join(overlay, name)
		if info, err := os.Lstat(dest); err == nil {
			if info.Mode()&os.ModeSymlink == 0 {
				return "", fmt.Errorf("split config overlay contains unexpected file %s", dest)
			}
			if err := os.Remove(dest); err != nil {
				return "", fmt.Errorf("replace split config link %s: %w", dest, err)
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("inspect split config path %s: %w", dest, err)
		}
		if err := os.Symlink(source, dest); err != nil {
			return "", fmt.Errorf("share config entry %s: %w", name, err)
		}
	}
	credentialPath := filepath.Join(overlay, ".credentials.json")
	if _, err := os.Lstat(credentialPath); err == nil {
		return "", fmt.Errorf("split config overlay contains an unexpected credential file: %s", credentialPath)
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("inspect split credential path: %w", err)
	}
	cache, err := readClaudeState(configDir)
	if err != nil {
		return "", err
	}
	accountCache, err := readClaudeState(credentialsDir)
	if err != nil {
		return "", err
	}
	delete(cache, "oauthAccount")
	if account, exists := accountCache["oauthAccount"]; exists {
		cache["oauthAccount"] = account
	}
	data, err := json.Marshal(cache)
	if err != nil {
		return "", fmt.Errorf("encode split account cache: %w", err)
	}
	cacheDest := filepath.Join(overlay, ".claude.json")
	tmpFile, err := os.CreateTemp(overlay, ".claude.json.tmp-")
	if err != nil {
		return "", fmt.Errorf("create split account cache: %w", err)
	}
	tmp := tmpFile.Name()
	if _, err := tmpFile.Write(data); err != nil {
		_ = tmpFile.Close()
		_ = os.Remove(tmp)
		return "", fmt.Errorf("write split account cache: %w", err)
	}
	if err := tmpFile.Close(); err != nil {
		_ = os.Remove(tmp)
		return "", fmt.Errorf("close split account cache: %w", err)
	}
	if err := os.Rename(tmp, cacheDest); err != nil {
		_ = os.Remove(tmp)
		return "", fmt.Errorf("install split account cache: %w", err)
	}
	if err := os.Chmod(overlay, 0700); err != nil {
		return "", fmt.Errorf("secure split config overlay: %w", err)
	}
	return overlay, nil
}

func getAuth(configDir, credentialsDir string) (authStatus, error) {
	var s authStatus
	bin, err := claudeBinary()
	if err != nil {
		return s, fmt.Errorf("find Claude Code: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "auth", "status", "--json")
	cmd.Env = withClaudeDirs(os.Environ(), configDir, credentialsDir)
	b, err := cmd.Output()
	if ctx.Err() != nil {
		return s, fmt.Errorf("claude auth status timed out for %s", configDir)
	}
	if err != nil {
		return s, fmt.Errorf("claude auth status for %s: %w", configDir, err)
	}
	if err := json.Unmarshal(b, &s); err != nil {
		return s, fmt.Errorf("parse Claude auth status: %w", err)
	}
	if !s.LoggedIn || s.Email == "" || s.OrgID == "" || s.AuthMethod != "claude.ai" {
		return s, fmt.Errorf("%s is not logged in with a claude.ai account", configDir)
	}
	return s, nil
}

func readClaudeState(dir string) (map[string]json.RawMessage, error) {
	path := filepath.Join(dir, ".claude.json")
	if isDefaultConfigDir(dir) {
		path = filepath.Join(filepath.Dir(dir), ".claude.json")
	}
	state := map[string]json.RawMessage{}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return state, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read Claude state %s: %w", path, err)
	}
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, fmt.Errorf("parse Claude state %s: %w", path, err)
	}
	if state == nil {
		return nil, fmt.Errorf("Claude state %s must be a JSON object", path)
	}
	return state, nil
}

// CLAUDE_SECURESTORAGE_CONFIG_DIR is an undocumented Claude Code feature.
// Version 2.1.215 is the earliest version with a public live report of the
// config/credential split working; older or unparseable versions fail closed.
func requireSeparateCredentialsSupport() error {
	bin, err := claudeBinary()
	if err != nil {
		return fmt.Errorf("find Claude Code: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, bin, "--version").Output()
	if err != nil {
		return fmt.Errorf("check Claude Code version for separate credential storage: %w", err)
	}
	parts := claudeVersionRE.FindStringSubmatch(string(out))
	if len(parts) != 4 {
		return fmt.Errorf("cannot verify Claude Code support for separate credential storage from version output %q", strings.TrimSpace(string(out)))
	}
	major, _ := strconv.Atoi(parts[1])
	minor, _ := strconv.Atoi(parts[2])
	patch, _ := strconv.Atoi(parts[3])
	if major < 2 || (major == 2 && (minor < 1 || (minor == 1 && patch < 215))) {
		return fmt.Errorf("separate config and account selection requires Claude Code 2.1.215 or newer; found %s", strings.Join(parts[1:], "."))
	}
	return nil
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
	s, err := getAuth(dir, dir)
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
	s, err := getAuth(p.Dir, p.Dir)
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

func parseLegacyDotfile(path string) (string, error) {
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
		return selection{Name: override, Source: "--profile"}, nil
	}
	dir, err := filepath.Abs(cwd)
	if err != nil {
		return selection{}, err
	}
	for {
		path := filepath.Join(dir, ".ccenv")
		if _, err := os.Stat(path); err == nil {
			folder, err := parseDotfile(path)
			if err != nil {
				return selection{}, err
			}
			if _, err := requireProfile(c, folder.Profile); err != nil {
				return selection{}, fmt.Errorf("%s: %w", path, err)
			}
			return selection{Name: folder.Profile, Source: path, Proxy: folder.Proxy}, nil
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
	return selection{Name: c.Default, Source: "global default"}, nil
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
	if s.Proxy != nil {
		fmt.Printf("Proxy: %s (direct fallback account: %s)\n", s.Proxy.URL, s.Name)
	}
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
	s, err := getAuth(p.Dir, p.Dir)
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

// refuseOverrideEnv stops a launch when the caller's environment could replace
// the subscription the profile selects.
func refuseOverrideEnv() error {
	for _, key := range []string{"ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN", "ANTHROPIC_BASE_URL", "CLAUDE_CODE_OAUTH_TOKEN", "CLAUDE_CODE_OAUTH_REFRESH_TOKEN", "CLAUDE_CODE_USE_BEDROCK", "CLAUDE_CODE_USE_VERTEX", "CLAUDE_CODE_USE_FOUNDRY", "CLAUDE_CODE_USE_MANTLE"} {
		if os.Getenv(key) != "" {
			return fmt.Errorf("%s is set and may override the subscription; unset it before launching", key)
		}
	}
	return nil
}

func run(args []string) error {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	name := fs.String("profile", "", "profile override")
	account := fs.String("account", "", "registered account profile whose credentials to use")
	ignorePin := fs.Bool("ignore-pin", false, "bypass the saved account identity for this launch")
	direct := fs.Bool("direct", false, "skip the folder proxy and use the pinned direct login")
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
	accountName := *account
	if accountName == "" {
		accountName = s.Name
	}
	accountProfile, err := requireProfile(c, accountName)
	if err != nil {
		return err
	}
	if err := refuseOverrideEnv(); err != nil {
		return err
	}
	if s.Proxy != nil && !*direct {
		if *ignorePin {
			return errors.New("--ignore-pin requires --direct when a folder proxy is configured")
		}
		proxy, err := proxyForAccount(*s.Proxy, s.Name, accountName)
		if err != nil {
			return err
		}
		key, err := readProxyKey(proxy.APIKeyFile)
		if err != nil {
			return err
		}
		if err := checkProxy(proxy, key); err == nil {
			credentialsProfile := accountProfile
			if proxy.AuthProfile != "" && accountName == s.Name {
				credentialsProfile, err = requireProfile(c, proxy.AuthProfile)
				if err != nil {
					return err
				}
				if credentialsProfile.Email != accountProfile.Email || credentialsProfile.OrgID != accountProfile.OrgID {
					return fmt.Errorf("proxy auth_profile %s must have the same pinned identity as %s", proxy.AuthProfile, accountName)
				}
			}
			if credentialsProfile.Dir != p.Dir {
				if err := requireSeparateCredentialsSupport(); err != nil {
					return err
				}
			}
			if proxy.AuthMode == "claudeai" {
				if os.Getenv("ANTHROPIC_CUSTOM_HEADERS") != "" {
					return errors.New("ANTHROPIC_CUSTOM_HEADERS is set; unset it before using claudeai proxy authentication")
				}
				auth, err := getAuth(credentialsProfile.Dir, credentialsProfile.Dir)
				if err != nil {
					return err
				}
				if auth.AuthMethod != "claude.ai" || auth.Email != accountProfile.Email || auth.OrgID != accountProfile.OrgID {
					return fmt.Errorf("%s proxy requires the pinned claude.ai login: expected %s / %s, found %s / %s (%s)", accountName, accountProfile.Email, accountProfile.OrgID, auth.Email, auth.OrgID, auth.AuthMethod)
				}
			}
			launchConfigDir := p.Dir
			if credentialsProfile.Dir != p.Dir {
				launchConfigDir, err = splitConfigDir(p.Dir, credentialsProfile.Dir)
				if err != nil {
					return err
				}
			}
			bin, err := claudeBinary()
			if err != nil {
				return err
			}
			fmt.Fprintf(os.Stderr, "ccenv: using %s config through proxy %s with %s account routing\n", s.Name, proxy.URL, accountName)
			if proxy.AuthMode == "claudeai" {
				fmt.Fprintf(os.Stderr, "ccenv: native claude.ai subscription login verified for %s\n", accountProfile.Email)
			}
			return syscall.Exec(bin, append([]string{bin}, fs.Args()...), proxyEnvironment(os.Environ(), launchConfigDir, credentialsProfile.Dir, proxy, key))
		} else if blocked := new(proxyAccountLimit); errors.As(err, &blocked) {
			return fmt.Errorf("%s account (%s): %w; direct login uses the same account limit. Wait for retry or explicitly choose another account with cc --account NAME --model opus", accountName, accountProfile.Email, err)
		} else if err := offerDirectFallback(err, accountName, accountProfile); err != nil {
			return err
		}
	}
	if accountProfile.Dir != p.Dir {
		if err := requireSeparateCredentialsSupport(); err != nil {
			return err
		}
	}
	auth, err := getAuth(accountProfile.Dir, accountProfile.Dir)
	if err != nil {
		return err
	}
	if *ignorePin {
		fmt.Fprintf(os.Stderr, "ccenv: pin bypassed for account %s: Claude reports %s / %s for this launch\n", accountName, auth.Email, auth.OrgID)
	} else if auth.Email != accountProfile.Email || auth.OrgID != accountProfile.OrgID {
		return fmt.Errorf("%s identity changed: expected %s / %s, found %s / %s", accountName, accountProfile.Email, accountProfile.OrgID, auth.Email, auth.OrgID)
	}
	launchConfigDir := p.Dir
	if accountProfile.Dir != p.Dir {
		launchConfigDir, err = splitConfigDir(p.Dir, accountProfile.Dir)
		if err != nil {
			return err
		}
		fmt.Fprintf(os.Stderr, "ccenv: using %s config with %s credentials; account cache is isolated for this split\n", s.Name, accountName)
	}
	bin, err := claudeBinary()
	if err != nil {
		return err
	}
	return syscall.Exec(bin, append([]string{bin}, fs.Args()...), withClaudeDirs(os.Environ(), launchConfigDir, accountProfile.Dir))
}
