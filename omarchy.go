package main

import (
	"embed"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// The Omarchy bar plugin ships inside the binary so it always matches the
// `ccenv list --json` contract it reads.
//
//go:embed omarchy/rarebit.ccenv
var omarchyPlugin embed.FS

const omarchyPluginID = "rarebit.ccenv"

func omarchy(args []string) error {
	if len(args) == 0 || args[0] != "install" {
		return errors.New("usage: ccenv omarchy install [--dir <plugins-dir>]")
	}
	fs := flag.NewFlagSet("omarchy install", flag.ContinueOnError)
	pluginsDir := fs.String("dir", "", "Omarchy plugins directory")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("usage: ccenv omarchy install [--dir <plugins-dir>]")
	}
	if *pluginsDir == "" {
		config, err := xdgDir("XDG_CONFIG_HOME", ".config")
		if err != nil {
			return err
		}
		*pluginsDir = filepath.Join(config, "omarchy", "plugins")
	}
	self, err := os.Executable()
	if err != nil {
		return err
	}
	if self, err = filepath.EvalSymlinks(self); err != nil {
		return err
	}
	dest := filepath.Join(*pluginsDir, omarchyPluginID)
	_, statErr := os.Stat(dest)
	existed := statErr == nil
	if err := writeOmarchyPlugin(dest, self); err != nil {
		return err
	}
	fmt.Println("Installed", dest)
	// A third-party plugin is enabled when its id appears in shell.json.
	shell, _ := os.ReadFile(filepath.Join(filepath.Dir(*pluginsDir), "shell.json"))
	if !strings.Contains(string(shell), strconv.Quote(omarchyPluginID)) {
		fmt.Println("Enable it with: omarchy plugin enable", omarchyPluginID)
	} else if existed {
		// The shell caches Ccenv.js (a .pragma library) across hot reloads.
		fmt.Println("Restart the shell to load the update: omarchy-restart-shell")
	}
	return nil
}

func writeOmarchyPlugin(dest, bin string) error {
	if err := os.MkdirAll(dest, 0755); err != nil {
		return err
	}
	root := "omarchy/" + omarchyPluginID
	err := fs.WalkDir(omarchyPlugin, root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := omarchyPlugin.ReadFile(path)
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		return writeEntry(filepath.Join(dest, rel), string(b))
	})
	if err != nil {
		return err
	}
	// The shell's PATH often lacks ~/.local/bin, so pin the absolute binary.
	return writeEntry(filepath.Join(dest, "Ccenv.js"), ".pragma library\nvar BIN = "+strconv.Quote(bin)+"\n")
}
