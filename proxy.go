package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

type folderConfig struct {
	Profile string       `json:"profile"`
	Proxy   *proxyConfig `json:"proxy,omitempty"`
}

// proxyModelAliases are the Claude Code model aliases a proxy can route.
// fable is optional; the others are required for an --account override.
var proxyModelAliases = []string{"opus", "sonnet", "haiku", "fable"}

type proxyConfig struct {
	Provider       string            `json:"provider,omitempty"`
	AuthMode       string            `json:"auth_mode,omitempty"`
	AuthProfile    string            `json:"auth_profile,omitempty"`
	URL            string            `json:"url"`
	APIKeyFile     string            `json:"api_key_file"`
	Model          string            `json:"model,omitempty"`
	DefaultModels  map[string]string `json:"default_models,omitempty"`
	TimeoutSeconds int               `json:"timeout_seconds,omitempty"`
}

func parseDotfile(path string) (folderConfig, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return folderConfig{}, err
	}
	if !strings.HasPrefix(strings.TrimSpace(string(b)), "{") {
		name, err := parseLegacyDotfile(path)
		return folderConfig{Profile: name}, err
	}
	var folder folderConfig
	decoder := json.NewDecoder(bytes.NewReader(b))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&folder); err != nil {
		return folderConfig{}, fmt.Errorf("invalid .ccenv at %s: %w", path, err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return folderConfig{}, fmt.Errorf("invalid .ccenv at %s: expected one JSON object", path)
	}
	if !validName.MatchString(folder.Profile) {
		return folderConfig{}, fmt.Errorf("invalid .ccenv at %s: expected a profile name", path)
	}
	if folder.Proxy != nil {
		if err := validateProxy(folder.Proxy, filepath.Dir(path)); err != nil {
			return folderConfig{}, fmt.Errorf("invalid .ccenv at %s: %w", path, err)
		}
	}
	return folder, nil
}

func validateProxy(p *proxyConfig, directory string) error {
	if p.Provider != "" && p.Provider != "cliproxyapi" {
		return errors.New("proxy provider must be cliproxyapi or omitted")
	}
	if p.AuthMode != "" && p.AuthMode != "gateway" && p.AuthMode != "claudeai" {
		return errors.New("proxy auth_mode must be gateway or claudeai")
	}
	if p.AuthProfile != "" && (p.AuthMode != "claudeai" || !validName.MatchString(p.AuthProfile)) {
		return errors.New("proxy auth_profile requires claudeai mode and a valid profile name")
	}
	u, err := url.Parse(p.URL)
	if err != nil || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return errors.New("proxy URL must have a host and no embedded credentials, query, or fragment")
	}
	if u.Scheme != "https" {
		ip := net.ParseIP(u.Hostname())
		if u.Scheme != "http" || (u.Hostname() != "localhost" && (ip == nil || !ip.IsLoopback())) {
			return errors.New("proxy URL requires HTTPS or loopback HTTP")
		}
	}
	if p.APIKeyFile == "" {
		return errors.New("proxy requires api_key_file; keep the key outside the selector")
	}
	if !filepath.IsAbs(p.APIKeyFile) && !strings.HasPrefix(p.APIKeyFile, "~/") {
		p.APIKeyFile = filepath.Join(directory, p.APIKeyFile)
	}
	p.APIKeyFile, err = expandPath(p.APIKeyFile)
	if err != nil {
		return err
	}
	if p.TimeoutSeconds == 0 {
		p.TimeoutSeconds = 3
	}
	if p.TimeoutSeconds < 1 || p.TimeoutSeconds > 30 {
		return errors.New("proxy timeout_seconds must be between 1 and 30")
	}
	if strings.ContainsAny(p.Model, "\r\n\x00") {
		return errors.New("proxy model must be a single line")
	}
	for alias, model := range p.DefaultModels {
		if !slices.Contains(proxyModelAliases, alias) {
			return errors.New("proxy default_models accepts only sonnet, opus, haiku and fable")
		}
		if strings.TrimSpace(model) == "" || strings.ContainsAny(model, "\r\n\x00") {
			return errors.New("proxy default_models values must be nonempty model names")
		}
	}
	p.URL = strings.TrimRight(p.URL, "/")
	if strings.HasSuffix(p.URL, "/v1") {
		return errors.New("proxy URL must be the base URL without /v1")
	}
	return nil
}

func local(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: ccenv local NAME [--proxy-url URL --api-key-file FILE] [--model MODEL]")
	}
	fs := flag.NewFlagSet("local", flag.ContinueOnError)
	proxyURL := fs.String("proxy-url", "", "proxy base URL")
	provider := fs.String("proxy-provider", "", "proxy provider (cliproxyapi)")
	keyFile := fs.String("api-key-file", "", "private file containing the gateway client key")
	model := fs.String("model", "", "model for proxy launches")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if len(fs.Args()) != 0 {
		return errors.New("unexpected arguments after folder settings")
	}
	if *proxyURL == "" {
		if *keyFile != "" || *model != "" || *provider != "" {
			return errors.New("--api-key-file, --model and --proxy-provider require --proxy-url")
		}
		return setLocal(args[0])
	}
	c, err := loadConfig()
	if err != nil {
		return err
	}
	if _, err := requireProfile(c, args[0]); err != nil {
		return err
	}
	p := &proxyConfig{Provider: *provider, URL: *proxyURL, APIKeyFile: *keyFile, Model: *model}
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	if err := validateProxy(p, cwd); err != nil {
		return err
	}
	p.APIKeyFile = *keyFile
	b, err := json.MarshalIndent(folderConfig{Profile: args[0], Proxy: p}, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(".ccenv", append(b, '\n'), 0644); err != nil {
		return err
	}
	fmt.Println("Wrote .ccenv:", args[0], "with proxy", p.URL)
	return nil
}

func readProxyKey(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("cannot open proxy api_key_file: %w", err)
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
		return "", errors.New("proxy api_key_file must be a regular file accessible only to its owner")
	}
	b, err := io.ReadAll(io.LimitReader(f, 4097))
	if err != nil {
		return "", err
	}
	key := strings.TrimSpace(string(b))
	if len(b) > 4096 || key == "" || strings.ContainsAny(key, "\r\n\t ") {
		return "", errors.New("proxy api_key_file must contain one nonempty key, at most 4096 bytes")
	}
	return key, nil
}

func checkProxy(p proxyConfig, key string) error {
	client := &http.Client{Timeout: time.Duration(p.TimeoutSeconds) * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	req, err := http.NewRequest(http.MethodGet, p.URL+"/v1/models", nil)
	if err != nil {
		return errors.New("invalid proxy request URL")
	}
	req.Header.Set("Authorization", "Bearer "+key)
	response, err := client.Do(req)
	if err != nil {
		return errors.New("proxy connection failed or timed out")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("proxy returned HTTP %d", response.StatusCode)
	}
	var catalog struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&catalog); err != nil || catalog.Data == nil {
		return errors.New("proxy returned an invalid or empty model catalog")
	}
	available := map[string]bool{}
	for _, model := range catalog.Data {
		available[model.ID] = true
	}
	if p.Model != "" && !available[p.Model] {
		return missingProxyModel(client, p, key, p.Model)
	}
	for _, alias := range proxyModelAliases {
		if model := p.DefaultModels[alias]; model != "" && !available[model] {
			return missingProxyModel(client, p, key, model)
		}
	}
	if len(catalog.Data) == 0 {
		return errors.New("proxy returned an empty model catalog")
	}
	return nil
}

func offerDirectFallback(cause error, name string, p profile) error {
	tty, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		return fmt.Errorf("%v; no interactive terminal available: use ccenv run --direct -- ... for the pinned direct login", cause)
	}
	defer tty.Close()
	if !confirmDirect(tty, tty, cause, name, p) {
		return errors.New("direct launch declined; Claude was not started")
	}
	fmt.Fprintf(os.Stderr, "ccenv: using pinned direct account %s for this launch\n", name)
	return nil
}

func confirmDirect(input io.Reader, output io.Writer, cause error, name string, p profile) bool {
	fmt.Fprintf(output, "ccenv: %v. Launch directly as %s (%s / %s)? [y/N] ", cause, name, p.Email, p.OrgID)
	line, err := bufio.NewReader(input).ReadString('\n')
	answer := strings.TrimSpace(strings.ToLower(line))
	return err == nil && (answer == "y" || answer == "yes")
}

func proxyForAccount(p proxyConfig, configName, accountName string) (proxyConfig, error) {
	if configName == accountName {
		return p, nil
	}
	rewrite := func(model string) (string, error) {
		prefix := configName + "/"
		if !strings.HasPrefix(model, prefix) || len(model) == len(prefix) {
			return "", fmt.Errorf("proxy --account requires model and all default_models to use the %s route prefix", prefix)
		}
		return accountName + "/" + strings.TrimPrefix(model, prefix), nil
	}
	var err error
	p.Model, err = rewrite(p.Model)
	if err != nil {
		return proxyConfig{}, err
	}
	defaults := make(map[string]string, len(proxyModelAliases))
	for _, alias := range proxyModelAliases {
		model, set := p.DefaultModels[alias]
		if !set && alias == "fable" {
			// An empty route clears any inherited Fable default, which would
			// still point at the folder's account.
			defaults[alias] = ""
			continue
		}
		defaults[alias], err = rewrite(model)
		if err != nil {
			return proxyConfig{}, err
		}
	}
	p.DefaultModels = defaults
	return p, nil
}

func proxyEnvironment(env []string, configDir, credentialsDir string, p proxyConfig, key string) []string {
	env = withClaudeDirs(env, configDir, credentialsDir)
	overrides := map[string]string{}
	for alias, model := range p.DefaultModels {
		overrides["ANTHROPIC_DEFAULT_"+strings.ToUpper(alias)+"_MODEL"] = model
	}
	if model := p.DefaultModels["sonnet"]; model != "" {
		overrides["CLAUDE_CODE_SUBAGENT_MODEL"] = model
	}
	result := make([]string, 0, len(env)+3)
	for _, value := range env {
		name, _, _ := strings.Cut(value, "=")
		if p.AuthMode == "claudeai" && name == "ANTHROPIC_CUSTOM_HEADERS" {
			continue
		}
		if _, exists := overrides[name]; exists {
			continue
		}
		if p.Model == "" || !strings.HasPrefix(value, "ANTHROPIC_MODEL=") {
			result = append(result, value)
		}
	}
	result = append(result, "ANTHROPIC_BASE_URL="+p.URL)
	if p.AuthMode == "claudeai" {
		result = append(result, "ANTHROPIC_CUSTOM_HEADERS=x-api-key: "+key)
	} else {
		result = append(result, "ANTHROPIC_AUTH_TOKEN="+key)
	}
	if p.Model != "" {
		result = append(result, "ANTHROPIC_MODEL="+p.Model)
	}
	for _, name := range []string{"ANTHROPIC_DEFAULT_SONNET_MODEL", "ANTHROPIC_DEFAULT_OPUS_MODEL", "ANTHROPIC_DEFAULT_HAIKU_MODEL", "ANTHROPIC_DEFAULT_FABLE_MODEL", "CLAUDE_CODE_SUBAGENT_MODEL"} {
		if value, exists := overrides[name]; exists && value != "" {
			result = append(result, name+"="+value)
		}
	}
	return result
}
