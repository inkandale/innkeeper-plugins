package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/inkandale/innkeeper/protocol"
)

// recorder keeps the notifications an import sends.
type recorder struct{ sent []any }

func (r *recorder) notify(_ string, params any) error {
	r.sent = append(r.sent, params)
	return nil
}

// items lists what was emitted, as "card <item>" or "lorebook <item>".
func (r *recorder) items() []string {
	var out []string
	for _, p := range r.sent {
		switch p := p.(type) {
		case protocol.EmitCard:
			out = append(out, "card "+p.Item)
		case protocol.EmitLorebook:
			out = append(out, "lorebook "+p.Item)
		}
	}
	return out
}

// startImport runs import.run with params and checkpoint, both JSON.
func startImport(t *testing.T, e env, params, checkpoint string) (*recorder, error) {
	t.Helper()
	raw, err := json.Marshal(protocol.ImportRunParams{Run: "1.1", Params: json.RawMessage(params),
		Checkpoint: json.RawMessage(checkpoint)})
	if err != nil {
		t.Fatal(err)
	}
	rec := &recorder{}
	_, err = run(context.Background(), e, rec.notify, raw)
	return rec, err
}

func testdata(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "card", "testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func isInvalid(err error) bool {
	var rpcErr *protocol.Error
	return errors.As(err, &rpcErr) && rpcErr.Code == protocol.CodeInvalidParams
}

func TestImportUpload(t *testing.T) {
	lib, tmp := t.TempDir(), t.TempDir()
	put := func(b []byte) string {
		sum := sha256.Sum256(b)
		sha := hex.EncodeToString(sum[:])
		path := filepath.Join(lib, sha[:2], sha[2:4], sha)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, b, 0o644); err != nil {
			t.Fatal(err)
		}
		return sha
	}
	e := env{library: lib, blobTmp: tmp}

	sha := put(testdata(t, "v3-seraphina.png"))
	rec, err := startImport(t, e, `{"blob": "`+sha+`", "name": "seraphina.png"}`, `null`)
	if err != nil {
		t.Fatal(err)
	}
	if got := rec.items(); !slices.Equal(got, []string{"card " + sha}) {
		t.Fatalf("emitted %q", got)
	}
	if avatar := rec.sent[0].(protocol.EmitCard).Avatar; filepath.Dir(avatar) != tmp {
		t.Fatalf("avatar %q is not in blob_tmp", avatar)
	}

	sha = put(testdata(t, "eldoria.lorebook.json"))
	rec, err = startImport(t, e, `{"blob": "`+sha+`", "name": "Eldoria.json"}`, `null`)
	if err != nil {
		t.Fatal(err)
	}
	if book := rec.sent[0].(protocol.EmitLorebook).Lorebook; !strings.Contains(string(book), `"name":"Eldoria"`) {
		t.Fatalf("lorebook not named from the upload: %.200s", book)
	}

	sha = put([]byte("not a card"))
	if _, err := startImport(t, e, `{"blob": "`+sha+`"}`, `null`); !isInvalid(err) {
		t.Fatalf("non-card upload: err = %v, want -32602", err)
	}
}

func TestImportRejects(t *testing.T) {
	dir := t.TempDir()
	missing := strings.Repeat("a", 64)
	for name, c := range map[string]struct {
		e      env
		params string
	}{
		"no folder configured":   {env{}, `{}`},
		"path leaves the folder": {env{folder: dir}, `{"path": "../etc"}`},
		"absolute path":          {env{folder: dir}, `{"path": "/etc"}`},
		"missing subfolder":      {env{folder: dir}, `{"path": "nope"}`},
		"path and blob":          {env{folder: dir, library: dir}, `{"path": "a", "blob": "` + missing + `"}`},
		"unknown param":          {env{folder: dir}, `{"folder": "/etc"}`},
		"blob not a sha256":      {env{library: dir}, `{"blob": "../../etc/passwd"}`},
		"missing upload":         {env{library: dir}, `{"blob": "` + missing + `"}`},
	} {
		if _, err := startImport(t, c.e, c.params, `null`); !isInvalid(err) {
			t.Errorf("%s: err = %v, want -32602", name, err)
		}
	}
}

// writeFolder makes a folder from name to content. Content starting with
// "testdata/" is that card/testdata file instead.
func writeFolder(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, content := range files {
		b := []byte(content)
		if rest, ok := strings.CutPrefix(content, "testdata/"); ok {
			b = testdata(t, rest)
		}
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestImportFolder(t *testing.T) {
	dir := writeFolder(t, map[string]string{
		"sub/seraphina.png": "testdata/v3-seraphina.png",
		"seraphina.json":    "testdata/v2-seraphina.json",
		"eldoria.json":      "testdata/eldoria.lorebook.json",
		"mirelle.charx":     "testdata/v3-mirelle.charx",
		"junk.json":         `{"theme": "dark"}`,
		"notes.txt":         "not a card",
	})
	outside := writeFolder(t, map[string]string{"secret.json": "testdata/v2-seraphina.json"})
	if err := os.Symlink(filepath.Join(outside, "secret.json"), filepath.Join(dir, "link.json")); err != nil {
		t.Fatal(err)
	}
	tmp := t.TempDir()
	e := env{folder: dir, blobTmp: tmp}

	rec, err := startImport(t, e, `{}`, `null`)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"lorebook eldoria.json", "card mirelle.charx", "card seraphina.json", "card sub/seraphina.png"}
	if got := rec.items(); !slices.Equal(got, want) {
		t.Fatalf("emitted %q, want %q", got, want)
	}
	if book := rec.sent[0].(protocol.EmitLorebook).Lorebook; !strings.Contains(string(book), `"name":"eldoria"`) {
		t.Errorf("lorebook not named from its file: %.200s", book)
	}
	var progress protocol.Progress
	var cp protocol.Checkpoint
	for _, p := range rec.sent {
		switch p := p.(type) {
		case protocol.Progress:
			progress = p
		case protocol.Checkpoint:
			cp = p
		case protocol.EmitCard:
			if !strings.HasSuffix(p.Item, ".json") && filepath.Dir(p.Avatar) != tmp {
				t.Errorf("avatar %q is not in blob_tmp", p.Avatar)
			}
		}
	}
	if progress.Done != 5 || progress.Total != 5 || !strings.Contains(progress.Message, "1 file") {
		t.Errorf("last progress = %+v; want 5 of 5, one file skipped", progress)
	}
	if string(cp.Checkpoint) != `{"last":"sub/seraphina.png"}` {
		t.Errorf("last checkpoint = %s", cp.Checkpoint)
	}

	rec, err = startImport(t, e, `{}`, `{"last": "mirelle.charx"}`)
	if want := []string{"card seraphina.json", "card sub/seraphina.png"}; err != nil || !slices.Equal(rec.items(), want) {
		t.Errorf("resumed import emitted %q, %v; want %q", rec.items(), err, want)
	}
	rec, err = startImport(t, e, `{"path": "sub"}`, `null`)
	if want := []string{"card sub/seraphina.png"}; err != nil || !slices.Equal(rec.items(), want) {
		t.Errorf("subfolder import emitted %q, %v; want %q", rec.items(), err, want)
	}
}

func TestImportFolderSkipsUnreadableSubfolder(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads every folder")
	}
	dir := writeFolder(t, map[string]string{
		"locked/a.json":  "testdata/v2-seraphina.json",
		"seraphina.json": "testdata/v2-seraphina.json",
	})
	locked := filepath.Join(dir, "locked")
	if err := os.Chmod(locked, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(locked, 0o755) })
	rec, err := startImport(t, env{folder: dir, blobTmp: t.TempDir()}, `{}`, `null`)
	if want := []string{"card seraphina.json"}; err != nil || !slices.Equal(rec.items(), want) {
		t.Fatalf("emitted %q, %v; want %q", rec.items(), err, want)
	}
}

// The form defaults the folder to /import and leaves a default out of the
// config; emptying the field means uploads only.
func TestConfigFolder(t *testing.T) {
	for _, tt := range []struct {
		config map[string]any
		want   string
	}{
		{map[string]any{}, "/import"},
		{map[string]any{"folder": ""}, ""},
		{map[string]any{"folder": "/cards"}, "/cards"},
	} {
		if got := configFolder(tt.config); got != tt.want {
			t.Errorf("configFolder(%v) = %q, want %q", tt.config, got, tt.want)
		}
	}
}
