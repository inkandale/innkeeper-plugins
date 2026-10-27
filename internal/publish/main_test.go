package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/BurntSushi/toml"
	"github.com/inkandale/innkeeper/protocol"
)

func TestPublish(t *testing.T) {
	now = func() time.Time { return time.Date(2026, 10, 6, 12, 0, 0, 0, time.UTC) }
	out := t.TempDir()
	tag, err := publish("../..", out, "https://example.com/dl", "password-login")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(tag, "password-login-v") {
		t.Fatalf("tag = %s", tag)
	}
	var index struct {
		Plugin []entry `toml:"plugin"`
	}
	if _, err := toml.DecodeFile(filepath.Join(out, tag, "entry.toml"), &index); err != nil {
		t.Fatal(err)
	}
	if len(index.Plugin) != 1 || len(index.Plugin[0].Artifact) != len(platforms) || index.Plugin[0].Path != "" {
		t.Fatalf("entry = %+v", index.Plugin)
	}
	if e := index.Plugin[0]; e.Category != "Login" || e.Released != "2026-10-06" {
		t.Fatalf("category = %q, released = %q", e.Category, e.Released)
	}
	for _, a := range index.Plugin[0].Artifact {
		file := filepath.Join(out, tag, filepath.Base(a.URL))
		body, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(body)
		if hex.EncodeToString(sum[:]) != a.SHA256 || a.URL != "https://example.com/dl/"+tag+"/"+filepath.Base(a.URL) {
			t.Fatalf("artifact %+v doesn't match its file", a)
		}
		f, _ := os.Open(file)
		gz, err := gzip.NewReader(f)
		if err != nil {
			t.Fatal(err)
		}
		var names []string
		tr := tar.NewReader(gz)
		for h, err := tr.Next(); err == nil; h, err = tr.Next() {
			names = append(names, h.Name)
		}
		f.Close()
		slices.Sort(names)
		if !slices.Equal(names, []string{"password-login", "plugin.toml"}) {
			t.Fatalf("%s holds %v", a.URL, names)
		}
	}
}

func TestCategorize(t *testing.T) {
	path := filepath.Join(t.TempDir(), "index.toml")
	published := `[[plugin]]
name = "password-login"
version = "0.1.0"

[[plugin]]
name = "lumiverse"
version = "0.1.0"

[[plugin]]
name = "retired"
category = "Old"
version = "0.1.0"
`
	if err := os.WriteFile(path, []byte(published), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := categorize("../..", path); err != nil {
		t.Fatal(err)
	}
	var index struct {
		Categories []string `toml:"categories"`
		Plugin     []entry  `toml:"plugin"`
	}
	if _, err := toml.DecodeFile(path, &index); err != nil {
		t.Fatal(err)
	}
	if len(index.Plugin) != 3 || index.Plugin[0].Category != "Login" || index.Plugin[0].Title != "Password login" || index.Plugin[2].Category != "Old" {
		t.Fatalf("index = %+v", index.Plugin)
	}
	if index.Plugin[0].Recommended || !index.Plugin[1].Recommended {
		t.Fatalf("recommended: %+v", index.Plugin)
	}
	if !slices.Equal(index.Categories, []string{"Apps", "Import", "Login"}) {
		t.Fatalf("categories = %v", index.Categories)
	}
}

func TestTarballCarriesIcon(t *testing.T) {
	m, err := protocol.LoadManifest("../../sillytavern")
	if err != nil {
		t.Fatal(err)
	}
	body, err := tarball("../..", "sillytavern", m, "linux", "amd64")
	if err != nil {
		t.Fatal(err)
	}
	gz, err := gzip.NewReader(bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	tr := tar.NewReader(gz)
	for h, err := tr.Next(); err == nil; h, err = tr.Next() {
		names = append(names, h.Name)
	}
	slices.Sort(names)
	if !slices.Equal(names, []string{"icon.svg", "plugin.toml", "sillytavern"}) {
		t.Fatalf("tarball holds %v", names)
	}
}
