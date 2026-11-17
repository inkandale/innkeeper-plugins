package main

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"maps"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"testing"

	"github.com/inkandale/innkeeper/protocol"
)

const collegeMD = "---\nname: college\ndescription: >\n  Campus life and classes.\n  Use for students.\n---\n\n# College\n\nSee references/campus.md.\n"

func wantInvalid(t *testing.T, err error, contains string) {
	t.Helper()
	var rpc *protocol.Error
	if !errors.As(err, &rpc) || rpc.Code != -32602 || !strings.Contains(rpc.Message, contains) {
		t.Fatalf("err = %v, want -32602 containing %q", err, contains)
	}
}

func TestParseSkillMarkdown(t *testing.T) {
	s, err := parseSkillMarkdown([]byte(strings.ReplaceAll(collegeMD, "\n", "\r\n")))
	if err != nil {
		t.Fatal(err)
	}
	if s.Name != "college" || s.Description != "Campus life and classes. Use for students." ||
		s.Body != "# College\n\nSee references/campus.md." {
		t.Fatalf("skill %+v", s)
	}
	s, err = parseSkillMarkdown([]byte("---\nname: \"a-b\"\ndescription: 'Quoted: yes'\nlicense: MIT\n---\nBody"))
	if err != nil || s.Name != "a-b" || s.Description != "Quoted: yes" || s.Body != "Body" {
		t.Fatalf("skill %+v, %v", s, err)
	}
}

func TestParseSkillRejects(t *testing.T) {
	for md, want := range map[string]string{
		"# No frontmatter":                                                     "starts with ---",
		"<!doctype html><html>":                                                "web page",
		"---\nname: a\ndescription: b\n":                                       "no closing ---",
		"---\nname: College\ndescription: b\n---\nx":                           "skill name",
		"---\nname: a--b\ndescription: b\n---\nx":                              "skill name",
		"---\nname: -a\ndescription: b\n---\nx":                                "skill name",
		"---\nname: ..\ndescription: b\n---\nx":                                "skill name",
		"---\nname: a\n---\nx":                                                 "description",
		"---\nname: a\ndescription: b\n---\n  \n":                              "instructions are empty",
		"---\nname: a\ndescription: " + strings.Repeat("d", 1025) + "\n---\nx": "description",
		"---\nname: [a\n---\nx":                                                "frontmatter",
	} {
		_, err := parseSkillMarkdown([]byte(md))
		wantInvalid(t, err, want)
	}
}

func zipOf(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, body := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		w.Write([]byte(body))
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestImportZip(t *testing.T) {
	s, err := parseUpload(zipOf(t, map[string]string{
		"college/SKILL.md":              collegeMD,
		"college/references/campus.md":  "Quad, library, dorms.",
		"college/assets/logo.png":       "\x89PNG\r\n\x1a\n\x00\x00",
		"__MACOSX/college/._SKILL.md.x": "ignored",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if s.Name != "college" || len(s.Files) != 1 || s.Files["references/campus.md"] != "Quad, library, dorms." {
		t.Fatalf("skill %+v", s)
	}
	// A zip of the folder's contents, with SKILL.md at the top.
	s, err = parseUpload(zipOf(t, map[string]string{"SKILL.md": collegeMD, "notes.md": "n"}))
	if err != nil || s.Files["notes.md"] != "n" {
		t.Fatalf("skill %+v, %v", s, err)
	}
}

func TestImportZipRefusesSlip(t *testing.T) {
	for _, name := range []string{"../evil.md", "/etc/evil.md", `college\..\..\evil.md`, "college/../../evil.md"} {
		_, err := parseUpload(zipOf(t, map[string]string{"college/SKILL.md": collegeMD, name: "x"}))
		wantInvalid(t, err, "leaves its folder")
	}
}

func TestImportZipRefusesBadArchives(t *testing.T) {
	_, err := parseUpload(zipOf(t, map[string]string{"a/SKILL.md": collegeMD, "b/SKILL.md": collegeMD}))
	wantInvalid(t, err, "more than one SKILL.md")
	_, err = parseUpload(zipOf(t, map[string]string{"readme.md": "x"}))
	wantInvalid(t, err, "no SKILL.md")
	_, err = parseUpload(zipOf(t, map[string]string{"SKILL.md": collegeMD, "big.md": strings.Repeat("a", maxFileSize+1)}))
	wantInvalid(t, err, "big.md is over 1 MiB")
	many := map[string]string{"SKILL.md": collegeMD}
	for i := range maxZipEntries {
		many[strings.Repeat("x", i+1)+".md"] = "x"
	}
	_, err = parseUpload(zipOf(t, many))
	wantInvalid(t, err, "entries")
}

func TestImportZipStopsReadingAtTheLimit(t *testing.T) {
	// 64 MiB of text deflates to well under the upload limit.
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, _ := zw.Create("SKILL.md")
	w.Write([]byte(collegeMD))
	w, _ = zw.Create("big.md")
	w.Write(bytes.Repeat([]byte("a"), 64<<20))
	zw.Close()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	_, err := parseUpload(buf.Bytes())
	runtime.ReadMemStats(&after)
	wantInvalid(t, err, "big.md is over 1 MiB")
	if read := after.TotalAlloc - before.TotalAlloc; read > 16<<20 {
		t.Fatalf("allocated %d MiB reading a zip with a 1 MiB file limit", read>>20)
	}
}

func TestImportZipSkipsLargeBinaries(t *testing.T) {
	png := append([]byte("\x89PNG\r\n\x1a\n"), bytes.Repeat([]byte{0}, 2<<20)...)
	s, err := parseUpload(zipOf(t, map[string]string{"college/SKILL.md": collegeMD, "college/assets/big.png": string(png)}))
	if err != nil || len(s.Files) != 0 {
		t.Fatalf("skill %+v, %v", s, err)
	}
}

func TestImportZipRefusesBinarySkillMD(t *testing.T) {
	_, err := parseUpload(zipOf(t, map[string]string{"SKILL.md": "---\nname: a\ndescription: b\n---\n\xff\xfe"}))
	wantInvalid(t, err, "UTF-8")
}

func TestSkillCheckCapsBody(t *testing.T) {
	err := skill{Name: "a", Description: "b", Body: strings.Repeat("a", maxFileSize+1)}.check()
	wantInvalid(t, err, "instructions are over 1 MiB")
}

func TestHeadIsTextAllowsACutRune(t *testing.T) {
	if !headIsText([]byte("caf\xc3")) || headIsText([]byte("caf\xff")) || headIsText([]byte("a\x00")) {
		t.Fatal("headIsText")
	}
}

func TestImportURL(t *testing.T) {
	zipped := zipOf(t, map[string]string{"college/SKILL.md": collegeMD, "college/a.md": "A"})
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/SKILL.md":
			w.Write([]byte(collegeMD))
		case "/college.zip":
			w.Write(zipped)
		case "/huge":
			w.Write(bytes.Repeat([]byte("a"), maxUpload+1))
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()
	ctx := context.Background()
	if s, err := fetchSkill(ctx, ts.URL+"/SKILL.md"); err != nil || s.Name != "college" {
		t.Fatalf("SKILL.md: %+v, %v", s, err)
	}
	if s, err := fetchSkill(ctx, ts.URL+"/college.zip"); err != nil || s.Files["a.md"] != "A" {
		t.Fatalf("zip: %+v, %v", s, err)
	}
	_, err := fetchSkill(ctx, ts.URL+"/missing")
	wantInvalid(t, err, "404")
	_, err = fetchSkill(ctx, ts.URL+"/huge")
	wantInvalid(t, err, "over 8 MiB")
	_, err = fetchSkill(ctx, "file:///etc/passwd")
	wantInvalid(t, err, "isn't an http or https URL")
}

func TestImportURLFetchesReferencedFiles(t *testing.T) {
	md := "---\nname: college\ndescription: d\n---\nLoad `references/a.md`, see [notes](notes.md#top) and `missing/x.md`, " +
		"never `../secret.md`, `logo.png`, or `{{user}}`."
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/college/SKILL.md":
			w.Write([]byte(md))
		case "/college/references/a.md":
			w.Write([]byte("A"))
		case "/college/notes.md":
			w.Write([]byte("N"))
		case "/college/logo.png":
			w.Write([]byte("\x89PNG\x00"))
		case "/secret.md":
			t.Error("fetched a file outside the skill folder")
		default:
			http.NotFound(w, r)
		}
	}))
	defer ts.Close()
	s, err := fetchSkill(context.Background(), ts.URL+"/college/SKILL.md")
	if err != nil {
		t.Fatal(err)
	}
	if want := map[string]string{"references/a.md": "A", "notes.md": "N"}; !maps.Equal(s.Files, want) {
		t.Fatalf("files = %v, want %v", s.Files, want)
	}
}

func TestSkillCheckRefusesBadFiles(t *testing.T) {
	s := skill{Name: "a", Description: "b", Body: "c"}
	for p, text := range map[string]string{"../x.md": "x", "/x.md": "x", "a/../x.md": "x", "SKILL.md": "x", "bin": "\x00"} {
		s.Files = map[string]string{p: text}
		if err := s.check(); err == nil {
			t.Errorf("%q accepted", p)
		}
	}
}
