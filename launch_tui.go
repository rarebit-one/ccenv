package main

import (
	"fmt"
	"io"
	"os"
	"strings"
)

func terminalMenuAvailable(tty *os.File) bool {
	return terminalIsTerminal(tty.Fd()) && os.Getenv("TERM") != "dumb"
}

// menuFrame keeps the list short and only shows details for the highlighted
// launch. Full home paths stay visible so the selection is reviewable.
func menuFrame(s selection, previews []launchPreview, selected int, color bool) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Claude launch  %s\r\n\r\n", s.Source)
	for i, preview := range previews {
		marker := "  "
		if i == selected {
			marker = "› "
			if color {
				b.WriteString("\x1b[1;36m")
			}
		}
		route := preview.ProxyRoute
		if route != "" {
			route += "/"
		} else {
			route = "direct"
		}
		fmt.Fprintf(&b, "%s%-16s %s", marker, preview.Choice.Name, route)
		if preview.MembersKnown {
			fmt.Fprintf(&b, "  %s", strings.Join(preview.Members, ", "))
		}
		if i == selected && color {
			b.WriteString("\x1b[0m")
		}
		b.WriteString("\r\n")
	}
	preview := previews[selected]
	b.WriteString("\r\n")
	fmt.Fprintf(&b, "Config home   %s\r\n", preview.ConfigHome)
	if preview.ClaudeConfig != preview.ConfigHome {
		fmt.Fprintf(&b, "Claude config %s\r\n", preview.ClaudeConfig)
	}
	fmt.Fprintf(&b, "Storage home  %s\r\n", preview.StorageHome)
	fmt.Fprintf(&b, "Auth          %s\r\n", preview.AuthSource)
	if preview.ProxyEndpoint != "" {
		fmt.Fprintf(&b, "Proxy         %s\r\n", preview.ProxyEndpoint)
	}
	if preview.ProxyModel != "" {
		fmt.Fprintf(&b, "Model         %s\r\n", preview.ProxyModel)
	}
	if strings.HasSuffix(preview.ProxyRoute, "-pool") || strings.HasSuffix(preview.ProxyRoute, "-fallback") {
		if preview.MembersKnown {
			fmt.Fprintf(&b, "Accounts      %s\r\n", strings.Join(preview.Members, ", "))
		} else {
			b.WriteString("Accounts      unavailable\r\n")
		}
	}
	b.WriteString("\r\n↑/↓ choose  ·  Enter launch  ·  q cancel")
	return b.String()
}

func promptLaunchTUI(tty *os.File, s selection, previews []launchPreview) (launchChoice, error) {
	oldState, err := terminalMakeRaw(tty.Fd())
	if err != nil {
		return launchChoice{}, fmt.Errorf("prepare launch menu: %w", err)
	}
	defer terminalRestore(tty.Fd(), oldState)
	fmt.Fprint(tty, "\x1b[?1049h\x1b[?25l")
	defer fmt.Fprint(tty, "\x1b[?25h\x1b[?1049l")

	selected := 0
	color := os.Getenv("NO_COLOR") == ""
	escape := 0
	dirty := true
	for {
		if dirty {
			fmt.Fprint(tty, "\x1b[H\x1b[2J", menuFrame(s, previews, selected, color))
			dirty = false
		}
		var key [1]byte
		if _, err := io.ReadFull(tty, key[:]); err != nil {
			return launchChoice{}, fmt.Errorf("read launch menu: %w", err)
		}
		switch escape {
		case 1:
			escape = 0
			if key[0] == '[' || key[0] == 'O' {
				escape = 2
				continue
			}
		case 2:
			escape = 0
			switch key[0] {
			case 'A':
				selected = (selected + len(previews) - 1) % len(previews)
				dirty = true
			case 'B':
				selected = (selected + 1) % len(previews)
				dirty = true
			}
			continue
		}
		switch key[0] {
		case 27:
			escape = 1
		case 'k':
			selected = (selected + len(previews) - 1) % len(previews)
			dirty = true
		case 'j':
			selected = (selected + 1) % len(previews)
			dirty = true
		case '\r', '\n':
			return previews[selected].Choice, nil
		case 'q', 'Q', 3:
			return launchChoice{}, fmt.Errorf("launch canceled")
		default:
			if key[0] >= '1' && int(key[0]-'1') < len(previews) {
				selected = int(key[0] - '1')
				dirty = true
			}
		}
	}
}
