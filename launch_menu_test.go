package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFolderLaunchValidation(t *testing.T) {
	cases := []struct {
		name string
		json string
		want string
	}{
		{"valid", `{"profile":"work","proxy":{"url":"https://proxy.test","api_key_file":"/private/key"},"launches":[{"name":"work","account":"work"},{"name":"ryan","proxy_route":"ryan","gateway_auth":true}]}`, ""},
		{"duplicate", `{"profile":"work","launches":[{"name":"same","account":"work"},{"name":"same","account":"work"}]}`, "unique"},
		{"numeric name", `{"profile":"work","launches":[{"name":"2","account":"work"}]}`, "nonnumeric"},
		{"ambiguous", `{"profile":"work","account":"work","launches":[{"name":"work","account":"work"}]}`, "cannot be combined"},
		{"missing target", `{"profile":"work","launches":[{"name":"empty"}]}`, "exactly one"},
		{"two targets", `{"profile":"work","proxy":{"url":"https://proxy.test","api_key_file":"/private/key"},"launches":[{"name":"both","account":"work","proxy_route":"ryan"}]}`, "exactly one"},
		{"route without proxy", `{"profile":"work","launches":[{"name":"ryan","proxy_route":"ryan"}]}`, "folder proxy"},
		{"gateway without route", `{"profile":"work","launches":[{"name":"work","account":"work","gateway_auth":true}]}`, "needs proxy_route"},
		{"profile without route", `{"profile":"work","launches":[{"name":"other","account":"work","profile":"other"}]}`, "needs a proxy_route"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), ".ccenv")
			if err := os.WriteFile(path, []byte(tc.json), 0600); err != nil {
				t.Fatal(err)
			}
			folder, err := parseDotfile(path)
			if tc.want == "" {
				if err != nil || len(folder.Launches) != 2 {
					t.Fatalf("valid launches rejected: %#v %v", folder, err)
				}
			} else if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("wanted %q, got %v", tc.want, err)
			}
		})
	}
}

func TestConfiguredPoolFallbackRequiresMatchingLaunch(t *testing.T) {
	pool := launchChoice{ProxyRoute: "fundbright-pool", GatewayAuth: true, Profile: "fundbright"}
	fallback := launchChoice{ProxyRoute: "fundbright-fallback", GatewayAuth: true, Profile: "fundbright"}
	for _, tc := range []struct {
		name   string
		choice launchChoice
		want   string
	}{
		{"matching", fallback, "fundbright-fallback"},
		{"different authentication", launchChoice{ProxyRoute: fallback.ProxyRoute, Profile: fallback.Profile}, ""},
		{"different profile", launchChoice{ProxyRoute: fallback.ProxyRoute, GatewayAuth: true}, ""},
		{"different route", launchChoice{ProxyRoute: "rarebit-fallback", GatewayAuth: true, Profile: fallback.Profile}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := configuredPoolFallback(selection{Launches: []launchChoice{pool, tc.choice}}, pool)
			if got != tc.want {
				t.Fatalf("fallback route = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestPoolLaunchCanSelectAnotherConfigHome(t *testing.T) {
	root := t.TempDir()
	familyHome := filepath.Join(root, "family-home")
	sidekickHome := filepath.Join(root, "sidekick-home")
	c := config{ProxyOnly: true, Profiles: map[string]profile{
		"family": {Dir: familyHome}, "sidekick": {Dir: sidekickHome},
	}}
	selector := `{"profile":"family","proxy":{"url":"https://proxy.test","api_key_file":"/private/key","model":"family/opus","default_models":{"opus":"family/opus","sonnet":"family/sonnet","haiku":"family/haiku","fable":"family/fable"}},"launches":[{"name":"sidekick","profile":"sidekick","proxy_route":"sidekick-pool","gateway_auth":true}]}`
	if err := os.WriteFile(filepath.Join(root, ".ccenv"), []byte(selector), 0600); err != nil {
		t.Fatal(err)
	}
	s, err := selectProfile(c, root, "")
	if err != nil {
		t.Fatal(err)
	}
	previews, err := folderLaunchPreviews(c, s, c.Profiles[s.Name])
	if err != nil {
		t.Fatal(err)
	}
	if len(previews) != 1 || previews[0].ConfigHome != sidekickHome || previews[0].StorageHome != sidekickHome || previews[0].ProxyRoute != "sidekick-pool" || previews[0].ProxyModel != "sidekick-pool/opus" {
		t.Fatalf("wrong global pool preview: %#v", previews)
	}
	c.Profiles = map[string]profile{"family": {Dir: familyHome}}
	if _, err := selectProfile(c, root, ""); err == nil || !strings.Contains(err.Error(), `unknown profile "sidekick"`) {
		t.Fatalf("unknown chosen config home did not stop selection: %v", err)
	}
}

func TestHomeSelectorAppliesOutsideHomeWhenNoCloserSelector(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	outside := filepath.Join(root, "outside")
	for _, dir := range []string{home, outside} {
		if err := os.Mkdir(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("HOME", home)
	if err := os.WriteFile(filepath.Join(home, ".ccenv"), []byte(`{"profile":"family"}`), 0600); err != nil {
		t.Fatal(err)
	}
	c := config{HomeSelector: true, Profiles: map[string]profile{"family": {Dir: home}}}
	s, err := selectProfile(c, outside, "")
	if err != nil || s.Source != filepath.Join(home, ".ccenv") {
		t.Fatalf("outside folder did not use home selector: %#v %v", s, err)
	}
	if err := os.WriteFile(filepath.Join(outside, ".ccenv"), []byte(`{"profile":"family"}`), 0600); err != nil {
		t.Fatal(err)
	}
	s, err = selectProfile(c, outside, "")
	if err != nil || s.Source != filepath.Join(outside, ".ccenv") {
		t.Fatalf("closer selector did not win: %#v %v", s, err)
	}
}

func TestLaunchMenuShowsHomesAndAcceptsChoice(t *testing.T) {
	s := selection{Source: "/workspace/.ccenv"}
	previews := []launchPreview{
		{Choice: launchChoice{Name: "jaryl"}, ConfigHome: "/home/jaryl/.claude-sidekick", ClaudeConfig: "/home/jaryl/.claude-sidekick", StorageHome: "/home/jaryl/.claude-sidekick", AuthSource: "local claude.ai login: jaryl@sidekicklabs.xyz", ProxyRoute: "sidekick", ProxyEndpoint: "https://proxy.test"},
		{Choice: launchChoice{Name: "ryan", ProxyRoute: "sidekick-ryan", GatewayAuth: true}, ConfigHome: "/home/jaryl/.claude-sidekick", ClaudeConfig: "/home/jaryl/.claude-sidekick", StorageHome: "/home/jaryl/.claude-sidekick", AuthSource: "gateway client token; no local account selected", ProxyRoute: "sidekick-ryan", ProxyEndpoint: "https://proxy.test"},
	}
	var output bytes.Buffer
	choice, err := promptLaunchChoice(strings.NewReader("invalid\n2\n"), &output, s, previews)
	if err != nil || choice.Name != "ryan" {
		t.Fatalf("menu selection: %#v %v", choice, err)
	}
	for _, want := range []string{"Config home: /home/jaryl/.claude-sidekick", "Secure storage home: /home/jaryl/.claude-sidekick", "local claude.ai login: jaryl@sidekicklabs.xyz", "gateway client token; no local account selected", "Proxy route: sidekick-ryan/", "Choose a listed number or name."} {
		if !strings.Contains(output.String(), want) {
			t.Fatalf("menu omitted %q: %s", want, output.String())
		}
	}
	choice, err = promptLaunchChoice(strings.NewReader("jaryl\n"), &bytes.Buffer{}, s, previews)
	if err != nil || choice.Name != "jaryl" {
		t.Fatalf("named selection: %#v %v", choice, err)
	}
	if _, err := promptLaunchChoice(strings.NewReader("\n"), &bytes.Buffer{}, s, previews); err == nil || !strings.Contains(err.Error(), "canceled") {
		t.Fatalf("blank choice should cancel, got %v", err)
	}
}

func TestLaunchPreviewShowsActualSplitConfigDir(t *testing.T) {
	configHome := filepath.Join(t.TempDir(), "config")
	credentialHome := filepath.Join(t.TempDir(), "credentials")
	c := config{Profiles: map[string]profile{
		"work":     {Dir: configHome, Email: "work@example.test"},
		"personal": {Dir: credentialHome, Email: "personal@example.test"},
	}}
	s := selection{Name: "work", Proxy: &proxyConfig{URL: "https://proxy.test", AuthMode: "claudeai", Model: "work/opus", DefaultModels: map[string]string{"opus": "work/opus", "sonnet": "work/sonnet", "haiku": "work/haiku"}}}
	preview, err := previewLaunch(c, s, c.Profiles["work"], launchChoice{Name: "personal", Account: "personal"})
	if err != nil {
		t.Fatal(err)
	}
	want, err := splitOverlayPath(configHome, credentialHome)
	if err != nil {
		t.Fatal(err)
	}
	if preview.ConfigHome != configHome || preview.ClaudeConfig != want || preview.StorageHome != credentialHome || !strings.Contains(preview.AuthSource, "personal@example.test") {
		t.Fatalf("preview did not show launch directories: %#v", preview)
	}
}

func TestUnknownFolderLaunchAccountStopsSelection(t *testing.T) {
	root := t.TempDir()
	selector := `{"profile":"work","launches":[{"name":"missing","account":"missing"}]}`
	if err := os.WriteFile(filepath.Join(root, ".ccenv"), []byte(selector), 0600); err != nil {
		t.Fatal(err)
	}
	c := config{Profiles: map[string]profile{"work": {Dir: root, Email: "work@example.test"}}}
	if _, err := selectProfile(c, root, ""); err == nil || !strings.Contains(err.Error(), `launch missing: unknown profile "missing"`) {
		t.Fatalf("unknown launch account did not stop selection: %v", err)
	}
}
