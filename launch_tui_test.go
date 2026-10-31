package main

import (
	"strings"
	"testing"
)

func TestMenuFrameShowsOnlySelectedLaunchDetails(t *testing.T) {
	selection := selection{Source: "/work/.ccenv"}
	previews := []launchPreview{
		{Choice: launchChoice{Name: "jaryl"}, ConfigHome: "/home/jaryl/.claude-sidekick", StorageHome: "/home/jaryl/.claude-sidekick", AuthSource: "local claude.ai login: jaryl@example.test", ProxyRoute: "sidekick", ProxyModel: "sidekick/opus", ProxyEndpoint: "https://gateway.test"},
		{Choice: launchChoice{Name: "ryan"}, ConfigHome: "/home/jaryl/.claude-sidekick-ryan", StorageHome: "/home/jaryl/.claude-sidekick-ryan", AuthSource: "gateway client token", ProxyRoute: "sidekick-ryan", ProxyModel: "sidekick-ryan/opus", ProxyEndpoint: "https://gateway.test"},
	}
	frame := menuFrame(selection, previews, 1, false)
	for _, want := range []string{"jaryl", "› ryan", "Config home   /home/jaryl/.claude-sidekick-ryan", "Storage home  /home/jaryl/.claude-sidekick-ryan", "sidekick-ryan/opus", "↑/↓ choose"} {
		if !strings.Contains(frame, want) {
			t.Fatalf("menu missing %q: %q", want, frame)
		}
	}
	for _, unwanted := range []string{"jaryl@example.test", "sidekick/opus", "\x1b["} {
		if strings.Contains(frame, unwanted) {
			t.Fatalf("menu shows unselected detail or color %q: %q", unwanted, frame)
		}
	}
}

func TestMenuFrameShowsPoolAndFallbackMembers(t *testing.T) {
	previews := []launchPreview{
		{Choice: launchChoice{Name: "pool"}, ProxyRoute: "family-pool", Members: []string{"family-kate", "other.water"}, MembersKnown: true},
		{Choice: launchChoice{Name: "fallback"}, ProxyRoute: "family-fallback", Members: []string{"tiny.bulb"}, MembersKnown: true},
	}
	frame := menuFrame(selection{Source: "/work/.ccenv"}, previews, 0, false)
	for _, want := range []string{"› pool", "family-kate, other.water", "fallback", "tiny.bulb", "Accounts      family-kate, other.water"} {
		if !strings.Contains(frame, want) {
			t.Fatalf("menu missing %q: %q", want, frame)
		}
	}
}
