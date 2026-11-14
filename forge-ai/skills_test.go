package main

import (
	"archive/zip"
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

// zipFolder zips dir as an owner would: the folder itself is the zip's top
// entry, so SKILL.md sits at <name>/SKILL.md.
func zipFolder(t *testing.T, dir string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	parent := filepath.Dir(dir)
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, err := filepath.Rel(parent, path)
		if err != nil {
			return err
		}
		w, err := zw.Create(filepath.ToSlash(rel))
		if err != nil {
			return err
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		_, err = w.Write(b)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestBundledSkillsImport(t *testing.T) {
	entries, err := os.ReadDir("skills")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) == 0 {
		t.Fatal("no skills in skills/")
	}
	for _, e := range entries {
		if !e.IsDir() {
			t.Errorf("skills/%s: only skill folders belong in skills/", e.Name())
			continue
		}
		t.Run(e.Name(), func(t *testing.T) {
			sk, err := parseZip(zipFolder(t, filepath.Join("skills", e.Name())))
			if err != nil {
				t.Fatal(err)
			}
			if sk.Name != e.Name() {
				t.Fatalf("frontmatter name %q, folder %q", sk.Name, e.Name())
			}
		})
	}
}
