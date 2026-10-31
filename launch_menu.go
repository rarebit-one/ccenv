package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
)

type launchPreview struct {
	Choice        launchChoice
	ConfigHome    string
	ClaudeConfig  string
	StorageHome   string
	AuthSource    string
	ProxyRoute    string
	ProxyModel    string
	ProxyEndpoint string
	Members       []string
	MembersKnown  bool
}

func previewLaunch(c config, s selection, p profile, choice launchChoice) (launchPreview, error) {
	if choice.Profile != "" {
		var err error
		p, err = requireProfile(c, choice.Profile)
		if err != nil {
			return launchPreview{}, err
		}
	}
	accountName := choice.Account
	if accountName == "" {
		if choice.Profile != "" {
			accountName = choice.Profile
		} else {
			accountName = s.Name
		}
	}
	accountProfile, err := requireProfile(c, accountName)
	if err != nil {
		return launchPreview{}, err
	}
	credentialsProfile := accountProfile
	if s.Proxy != nil && s.Proxy.AuthProfile != "" && accountName == s.Name && !choice.GatewayAuth && !c.ProxyOnly {
		credentialsProfile, err = requireProfile(c, s.Proxy.AuthProfile)
		if err != nil {
			return launchPreview{}, err
		}
		if credentialsProfile.Email != accountProfile.Email || credentialsProfile.OrgID != accountProfile.OrgID {
			return launchPreview{}, fmt.Errorf("proxy auth_profile %s must have the same pinned identity as %s", s.Proxy.AuthProfile, accountName)
		}
	}
	if c.ProxyOnly {
		credentialsProfile = p
	}
	preview := launchPreview{
		Choice:       choice,
		ConfigHome:   p.Dir,
		ClaudeConfig: p.Dir,
		StorageHome:  credentialsProfile.Dir,
		AuthSource:   "local claude.ai login: " + accountProfile.Email,
	}
	if credentialsProfile.Dir != p.Dir {
		preview.ClaudeConfig, err = splitOverlayPath(p.Dir, credentialsProfile.Dir)
		if err != nil {
			return launchPreview{}, err
		}
	}
	if s.Proxy != nil {
		preview.ProxyEndpoint = s.Proxy.URL
		routeName := accountName
		if choice.ProxyRoute != "" {
			routeName = choice.ProxyRoute
		}
		proxy, err := proxyForAccount(*s.Proxy, s.Name, routeName)
		if err != nil {
			return launchPreview{}, err
		}
		preview.ProxyModel = proxy.Model
		if strings.HasPrefix(proxy.Model, routeName+"/") {
			preview.ProxyRoute = routeName
		}
		if s.Proxy.AuthMode != "claudeai" || choice.GatewayAuth || c.ProxyOnly {
			preview.AuthSource = "gateway client token; no local account selected"
		}
	}
	return preview, nil
}

func folderLaunchPreviews(c config, s selection, p profile) ([]launchPreview, error) {
	previews := make([]launchPreview, 0, len(s.Launches))
	var members map[string][]string
	if s.Proxy != nil {
		members = poolMembers(*s.Proxy)
	}
	for _, choice := range s.Launches {
		preview, err := previewLaunch(c, s, p, choice)
		if err != nil {
			return nil, fmt.Errorf("launch %s: %w", choice.Name, err)
		}
		preview.Members, preview.MembersKnown = members[preview.ProxyRoute]
		previews = append(previews, preview)
	}
	return previews, nil
}

// A matching fallback launch is an explicit opt-in to changing the serving
// gateway account when the selected pool has no usable models.
func configuredPoolFallback(s selection, selected launchChoice) string {
	if !strings.HasSuffix(selected.ProxyRoute, "-pool") {
		return ""
	}
	want := strings.TrimSuffix(selected.ProxyRoute, "-pool") + "-fallback"
	for _, choice := range s.Launches {
		if choice.ProxyRoute == want && choice.Profile == selected.Profile && choice.GatewayAuth == selected.GatewayAuth {
			return want
		}
	}
	return ""
}

func printLaunchPreview(w io.Writer, number int, preview launchPreview) {
	if number > 0 {
		fmt.Fprintf(w, "  %d) %s\n", number, preview.Choice.Name)
	} else {
		fmt.Fprintf(w, "  %s\n", preview.Choice.Name)
	}
	fmt.Fprintf(w, "     Config home: %s\n", preview.ConfigHome)
	if preview.ClaudeConfig != preview.ConfigHome {
		fmt.Fprintf(w, "     Claude config dir: %s (split overlay)\n", preview.ClaudeConfig)
	}
	fmt.Fprintf(w, "     Secure storage home: %s\n", preview.StorageHome)
	fmt.Fprintf(w, "     Authentication: %s\n", preview.AuthSource)
	if preview.ProxyRoute != "" {
		fmt.Fprintf(w, "     Proxy route: %s/ through %s\n", preview.ProxyRoute, preview.ProxyEndpoint)
	} else if preview.ProxyEndpoint != "" {
		fmt.Fprintf(w, "     Proxy: %s\n", preview.ProxyEndpoint)
	}
	if strings.HasSuffix(preview.ProxyRoute, "-pool") || strings.HasSuffix(preview.ProxyRoute, "-fallback") {
		if preview.MembersKnown {
			fmt.Fprintf(w, "     Accounts: %s\n", strings.Join(preview.Members, ", "))
		} else {
			fmt.Fprintln(w, "     Accounts: unavailable")
		}
	}
	if preview.ProxyModel != "" {
		fmt.Fprintf(w, "     Startup model: %s\n", preview.ProxyModel)
	}
}

func printLaunchPreviews(w io.Writer, s selection, previews []launchPreview) {
	fmt.Fprintf(w, "Choose a Claude launch for %s:\n", s.Source)
	for i, preview := range previews {
		printLaunchPreview(w, i+1, preview)
	}
}

func launchByName(previews []launchPreview, name string) (launchChoice, error) {
	for _, preview := range previews {
		if preview.Choice.Name == name {
			return preview.Choice, nil
		}
	}
	names := make([]string, 0, len(previews))
	for _, preview := range previews {
		names = append(names, preview.Choice.Name)
	}
	return launchChoice{}, fmt.Errorf("unknown launch choice %q; choose one of %s", name, strings.Join(names, ", "))
}

func promptLaunchChoice(r io.Reader, w io.Writer, s selection, previews []launchPreview) (launchChoice, error) {
	printLaunchPreviews(w, s, previews)
	reader := bufio.NewReader(r)
	for {
		fmt.Fprint(w, "Choice (number or name; blank cancels): ")
		line, err := reader.ReadString('\n')
		if err != nil && err != io.EOF {
			return launchChoice{}, err
		}
		answer := strings.TrimSpace(line)
		if answer == "" {
			return launchChoice{}, fmt.Errorf("launch canceled")
		}
		if number, parseErr := strconv.Atoi(answer); parseErr == nil && number >= 1 && number <= len(previews) {
			return previews[number-1].Choice, nil
		}
		if choice, lookupErr := launchByName(previews, answer); lookupErr == nil {
			return choice, nil
		}
		if err == io.EOF {
			return launchChoice{}, fmt.Errorf("launch canceled")
		}
		fmt.Fprintln(w, "Choose a listed number or name.")
	}
}

func selectFolderLaunch(c config, s selection, p profile, name string) (launchChoice, error) {
	previews, err := folderLaunchPreviews(c, s, p)
	if err != nil {
		return launchChoice{}, err
	}
	if len(previews) == 0 {
		return launchChoice{}, fmt.Errorf("%s has no launch choices", s.Source)
	}
	if name != "" {
		choice, err := launchByName(previews, name)
		if err != nil {
			return launchChoice{}, err
		}
		fmt.Fprintln(os.Stderr, "Selected Claude launch:")
		for _, preview := range previews {
			if preview.Choice.Name == choice.Name {
				printLaunchPreview(os.Stderr, 0, preview)
				break
			}
		}
		return choice, nil
	}
	if len(previews) == 1 {
		fmt.Fprintln(os.Stderr, "Selected Claude launch:")
		printLaunchPreview(os.Stderr, 0, previews[0])
		return previews[0].Choice, nil
	}
	tty, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		names := make([]string, 0, len(previews))
		for _, preview := range previews {
			names = append(names, preview.Choice.Name)
		}
		return launchChoice{}, fmt.Errorf("multiple launches in %s; use --choice NAME (%s) without an interactive terminal", s.Source, strings.Join(names, ", "))
	}
	defer tty.Close()
	if !terminalMenuAvailable(tty) {
		return promptLaunchChoice(tty, tty, s, previews)
	}
	choice, err := promptLaunchTUI(tty, s, previews)
	if err != nil {
		return launchChoice{}, err
	}
	for _, preview := range previews {
		if preview.Choice.Name == choice.Name {
			fmt.Fprintf(os.Stderr, "ccenv: selected %s; config home %s; secure storage home %s\n", choice.Name, preview.ConfigHome, preview.StorageHome)
			break
		}
	}
	return choice, nil
}
