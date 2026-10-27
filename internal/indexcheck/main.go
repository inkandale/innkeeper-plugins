// Command indexcheck checks that index.toml lists every plugin folder and
// matches each folder's plugin.toml. Run it from the repo root.
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/BurntSushi/toml"

	"github.com/inkandale/innkeeper/protocol"
)

type entry struct {
	Name         string   `toml:"name"`
	Title        string   `toml:"title"`
	Path         string   `toml:"path"`
	Category     string   `toml:"category"`
	Recommended  bool     `toml:"recommended"`
	Version      string   `toml:"version"`
	Protocol     int      `toml:"protocol"`
	Capabilities []string `toml:"capabilities"`
	Description  string   `toml:"description"`
	Uses         []string `toml:"uses"`
}

func main() {
	errs := check(".")
	for _, err := range errs {
		fmt.Fprintln(os.Stderr, err)
	}
	if len(errs) > 0 {
		os.Exit(1)
	}
}

// check returns every problem with the index in the repo at root.
func check(root string) []error {
	var index struct {
		Categories []string `toml:"categories"`
		Plugin     []entry  `toml:"plugin"`
	}
	md, err := toml.DecodeFile(filepath.Join(root, "index.toml"), &index)
	if err != nil {
		return []error{err}
	}
	var errs []error
	if u := md.Undecoded(); len(u) > 0 {
		errs = append(errs, fmt.Errorf("index.toml: unknown keys: %v", u))
	}
	listed := map[string]bool{}
	for _, e := range index.Plugin {
		if e.Path == "." || !filepath.IsLocal(e.Path) || strings.ContainsRune(e.Path, '/') {
			errs = append(errs, fmt.Errorf("%s: path %q must be a folder at the repo root", e.Name, e.Path))
			continue
		}
		if listed[e.Path] {
			errs = append(errs, fmt.Errorf("%s: listed twice", e.Path))
		}
		listed[e.Path] = true
		m, err := protocol.LoadManifest(filepath.Join(root, e.Path))
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", e.Path, err))
			continue
		}
		if e.Name != m.Name || e.Version != m.Version || e.Protocol != m.Protocol || !slices.Equal(e.Capabilities, m.Capabilities) {
			errs = append(errs, fmt.Errorf("%s: index entry (name %q, version %q, protocol %d, capabilities %v) doesn't match plugin.toml (%q, %q, %d, %v)",
				e.Path, e.Name, e.Version, e.Protocol, e.Capabilities, m.Name, m.Version, m.Protocol, m.Capabilities))
		}
		if e.Title != m.Title || e.Description != m.Description || e.Category != m.Category {
			errs = append(errs, fmt.Errorf("%s: index title %q, description %q, category %q; plugin.toml has %q, %q, %q",
				e.Path, e.Title, e.Description, e.Category, m.Title, m.Description, m.Category))
		}
		if !slices.Equal(e.Uses, m.Uses) {
			errs = append(errs, fmt.Errorf("%s: index uses %v; plugin.toml has %v", e.Path, e.Uses, m.Uses))
		}
		if !slices.Equal(m.Command,[]string{"./" + e.Path}) {
			errs = append(errs, fmt.Errorf("%s: command must be [\"./%s\"], where make build writes the binary", e.Path, e.Path))
		}
		if e.Description == "" {
			errs = append(errs, fmt.Errorf("%s: description is required", e.Path))
		}
	}
	for _, c := range index.Categories {
		if !slices.ContainsFunc(index.Plugin, func(e entry) bool { return e.Category == c }) {
			errs = append(errs, fmt.Errorf("categories: no plugin is in %q", c))
		}
	}
	dirs, err := os.ReadDir(root)
	if err != nil {
		return append(errs, err)
	}
	for _, d := range dirs {
		if !d.IsDir() || listed[d.Name()] {
			continue
		}
		if _, err := os.Stat(filepath.Join(root, d.Name(), "plugin.toml")); err == nil {
			errs = append(errs, fmt.Errorf("%s has a plugin.toml but no index.toml entry", d.Name()))
		}
	}
	return errs
}
