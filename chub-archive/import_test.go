package main

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/inkandale/innkeeper/protocol"
)

func testEnv(t *testing.T) *env {
	t.Helper()
	return &env{images: []string{"testdata/images"}, blobTmp: t.TempDir()}
}

// sent is what one run sent: cards and lorebooks in order, and checkpoints.
type sent struct {
	cards     []protocol.EmitCard
	lorebooks []protocol.EmitLorebook
	order     []string // item of each card and lorebook, in the order sent
	cps       []string
	logs      []string // level and message of each log notice
}

func importParams(dump, checkpoint string) json.RawMessage {
	p := protocol.ImportRunParams{Run: "r", Params: json.RawMessage(`{"dump": "` + dump + `"}`)}
	if checkpoint != "" {
		p.Checkpoint = json.RawMessage(checkpoint)
	}
	b, _ := json.Marshal(p)
	return b
}

// runImport imports testdata/dump.sql from checkpoint. stop, if set, ends
// the run with an error when it returns true for a checkpoint.
func runImport(t *testing.T, e *env, checkpoint string, stop func(cp string) bool) (sent, error) {
	t.Helper()
	var s sent
	errStop := errors.New("stopped")
	notify := func(method string, params any) error {
		switch p := params.(type) {
		case protocol.EmitCard:
			s.cards = append(s.cards, p)
			s.order = append(s.order, p.Item)
		case protocol.EmitLorebook:
			s.lorebooks = append(s.lorebooks, p)
			s.order = append(s.order, p.Item)
		case protocol.Log:
			s.logs = append(s.logs, p.Level+" "+p.Message)
		case protocol.Checkpoint:
			s.cps = append(s.cps, string(p.Checkpoint))
			if stop != nil && stop(string(p.Checkpoint)) {
				return errStop
			}
		}
		return nil
	}
	_, err := run(context.Background(), e, notify, importParams("testdata/dump.sql", checkpoint))
	if err != nil && !errors.Is(err, errStop) {
		t.Fatal(err)
	}
	return s, err
}

func sentCard(t *testing.T, s sent, item, version string) protocol.EmitCard {
	t.Helper()
	for _, c := range s.cards {
		if c.Item == item && c.Version == version {
			return c
		}
	}
	t.Fatalf("no card %s %q sent", item, version)
	return protocol.EmitCard{}
}

func values(c protocol.EmitCard) map[string]string {
	out := map[string]string{}
	for k, v := range c.Values {
		out[k] = string(v)
	}
	return out
}

func TestImportSendsEachVersionInDumpOrder(t *testing.T) {
	e := testEnv(t)
	s, _ := runImport(t, e, "", nil)

	want := []string{"booru_character_def:+pygmalion1", "chub_character_def:100", "chub_character_def:101",
		"chub_character_def:100", "chub_lorebook_def:200", "generic_character_def:3cfc7793db330003fc1ff39895edc8cc",
		"nyaime_character_def:43", "risuai_character_def:0c95bece-1d02-416f-8834-7a87895af295",
		"webring_character_def:cdef8582d2d146742a302972a605b19b"}
	if !slices.Equal(s.order, want) {
		t.Fatalf("sent %v, want %v", s.order, want)
	}

	aria := sentCard(t, s, "chub_character_def:100", "Version 9")
	if !aria.Created.Equal(time.Date(2024, 10, 2, 9, 50, 16, 236022000, time.UTC)) {
		t.Errorf("Aria's created = %v, want the version row's added", aria.Created)
	}
	if !strings.Contains(string(aria.Card), `who says \"hi\".\nShe guards\tthe woods.`) || !strings.Contains(string(aria.Card), `"chara_card_v3"`) {
		t.Errorf("Aria's card = %s", aria.Card)
	}
	// chub's per-item row gives the rating and topics.
	if got, want := values(aria), map[string]string{"rating": "4.5", "topics": `["Fantasy","Elf"]`, "site": `"chub"`}; !maps.Equal(got, want) {
		t.Errorf("Aria's values = %v, want %v", got, want)
	}
	if got := values(sentCard(t, s, "chub_character_def:101", "Version 7")); !maps.Equal(got, map[string]string{"site": `"chub"`, "hidden": `"yes"`}) {
		t.Errorf("Brin's values = %v", got)
	}
	if got := values(sentCard(t, s, "booru_character_def:+pygmalion1", "Version 1")); !maps.Equal(got, map[string]string{"site": `"booru"`}) {
		t.Errorf("Cora's values = %v", got)
	}
	if len(s.lorebooks) != 1 || !strings.Contains(string(s.lorebooks[0].Lorebook), "A kingdom of elves.") {
		t.Errorf("lorebooks = %+v", s.lorebooks)
	}

	var last checkpoint
	_ = json.Unmarshal([]byte(s.cps[len(s.cps)-1]), &last)
	info, err := os.Stat("testdata/dump.sql")
	if err != nil {
		t.Fatal(err)
	}
	if last.Table != "" || last.Offset != info.Size() || last.Size != info.Size() {
		t.Fatalf("last checkpoint = %+v, want the end of the dump", last)
	}
}

func TestImportSendsAvatarsThroughBlobTmp(t *testing.T) {
	e := testEnv(t)
	s, _ := runImport(t, e, "", nil)
	for _, version := range []string{"Version 5", "Version 9"} {
		a := sentCard(t, s, "chub_character_def:100", version).Avatar
		if filepath.Dir(a) != e.blobTmp || filepath.Ext(a) != ".png" {
			t.Fatalf("Aria %s: avatar = %q, want a .png in %s", version, a, e.blobTmp)
		}
		want, _ := os.ReadFile("testdata/images/a/b/c/0000000000000000000000000000d")
		if got, err := os.ReadFile(a); err != nil || string(got) != string(want) {
			t.Fatalf("Aria %s: avatar holds %d bytes, err %v; want the archive's image", version, len(got), err)
		}
	}
	// Brin's image isn't in the archive.
	if a := sentCard(t, s, "chub_character_def:101", "Version 7").Avatar; a != "" {
		t.Fatalf("Brin's avatar = %q, want none", a)
	}
}

// A run stopped inside chub_character_def resumes from its checkpoint and
// still knows chub's per-item rows, read before the stop.
func TestImportResumes(t *testing.T) {
	old := checkpointEvery
	checkpointEvery = 1
	t.Cleanup(func() { checkpointEvery = old })

	e := testEnv(t)
	first, err := runImport(t, e, "", func(cp string) bool { return strings.Contains(cp, `"table":"chub_character_def"`) })
	if err == nil {
		t.Fatal("the run wasn't stopped inside chub_character_def")
	}
	rest, _ := runImport(t, e, first.cps[len(first.cps)-1], nil)
	if got := append(first.order, rest.order...); len(got) != 9 || got[2] != "chub_character_def:101" {
		t.Fatalf("sent %v across both runs, want each version once", got)
	}
	if got := values(sentCard(t, rest, "chub_character_def:100", "Version 9")); got["rating"] != "4.5" {
		t.Fatalf("resumed Aria's values = %v, want chub's rating", got)
	}
}

// Resuming from a checkpoint inside chub's per-item block reads the rows
// before it again, and resuming past chub's versions sends the rest.
func TestImportResumesAroundChubsPerItemBlock(t *testing.T) {
	old := checkpointEvery
	checkpointEvery = 1
	t.Cleanup(func() { checkpointEvery = old })

	for _, tc := range []struct {
		table string
		sent  int // versions sent before the stop
	}{{"chub_character", 1}, {"generic_character_def", 6}} {
		e := testEnv(t)
		first, err := runImport(t, e, "", func(cp string) bool { return strings.Contains(cp, `"table":"`+tc.table+`"`) })
		if err == nil || len(first.order) != tc.sent {
			t.Fatalf("%s: stopped after %v, err %v; want %d versions sent", tc.table, first.order, err, tc.sent)
		}
		rest, _ := runImport(t, e, first.cps[len(first.cps)-1], nil)
		if got := append(first.order, rest.order...); len(got) != 9 {
			t.Fatalf("%s: sent %v across both runs, want each version once", tc.table, got)
		}
		if tc.table == "chub_character" {
			if got := values(sentCard(t, rest, "chub_character_def:100", "Version 5")); got["rating"] != "4.5" {
				t.Fatalf("resumed Aria's values = %v, want chub's rating", got)
			}
		}
	}
}

// A run that stops early leaves in blob_tmp only the avatars it sent.
func TestImportStoppedEarlyRemovesUnsentAvatars(t *testing.T) {
	e := testEnv(t)
	errStop := errors.New("stopped")
	var sentAvatars []string
	notify := func(method string, params any) error {
		c, ok := params.(protocol.EmitCard)
		if !ok {
			return nil
		}
		if c.Version == "Version 9" {
			return errStop
		}
		if c.Avatar != "" {
			sentAvatars = append(sentAvatars, c.Avatar)
		}
		return nil
	}
	if _, err := run(context.Background(), e, notify, importParams("testdata/dump.sql", "")); err == nil {
		t.Fatal("the run wasn't stopped")
	}
	left, _ := filepath.Glob(filepath.Join(e.blobTmp, "*"))
	if !slices.Equal(left, sentAvatars) || len(left) != 1 {
		t.Fatalf("blob_tmp holds %v, want only the avatar sent, %v", left, sentAvatars)
	}
}

func TestImportRefusesAnotherDumpsCheckpoint(t *testing.T) {
	e := testEnv(t)
	_, err := run(context.Background(), e, func(string, any) error { return nil }, importParams("testdata/dump.sql", `{"offset": 10, "size": 1}`))
	var rpc *protocol.Error
	if !errors.As(err, &rpc) || rpc.Code != protocol.CodeInvalidParams || !strings.Contains(err.Error(), "changed") {
		t.Fatalf("err = %v, want invalid params saying the dump changed", err)
	}
}

func TestImportNeedsTheDump(t *testing.T) {
	for _, dump := range []string{"", "testdata/missing.sql"} {
		_, err := run(context.Background(), testEnv(t), func(string, any) error { return nil }, importParams(dump, ""))
		var rpc *protocol.Error
		if !errors.As(err, &rpc) || rpc.Code != protocol.CodeInvalidParams || !strings.Contains(err.Error(), "dump") {
			t.Fatalf("dump %q: err = %v, want invalid params naming dump", dump, err)
		}
	}
}

// An unmounted image folder would import every version without its image.
func TestImportNeedsTheImageFolders(t *testing.T) {
	for _, dir := range []string{"testdata/missing", "testdata/dump.sql"} {
		e := testEnv(t)
		e.images = []string{"testdata/images", dir}
		_, err := run(context.Background(), e, func(string, any) error { return nil }, importParams("testdata/dump.sql", ""))
		var rpc *protocol.Error
		if !errors.As(err, &rpc) || rpc.Code != protocol.CodeInvalidParams {
			t.Fatalf("%s: err = %v, want invalid params", dir, err)
		}
	}
}

// An image that will never read, such as a directory at its path, leaves
// the version without one rather than stop every retry on it.
func TestImportSendsVersionWithUnreadableImage(t *testing.T) {
	e := testEnv(t)
	broken := t.TempDir()
	if err := os.MkdirAll(filepath.Join(broken, "a", "b", "c", "0000000000000000000000000000d"), 0o755); err != nil {
		t.Fatal(err)
	}
	e.images = []string{broken}
	s, _ := runImport(t, e, "", nil)
	if len(s.cards) != 8 || sentCard(t, s, "chub_character_def:100", "Version 5").Avatar != "" {
		t.Fatalf("sent %d cards, Aria's avatar %q; want 8, without an avatar", len(s.cards), sentCard(t, s, "chub_character_def:100", "Version 5").Avatar)
	}
	logged := strings.Join(s.logs, "\n")
	if !strings.Contains(logged, "warn image abc0000000000000000000000000000d: ") ||
		!strings.Contains(logged, "warn skipped chub_character_def 102 increment 8") {
		t.Fatalf("logged %q; want warnings for the unreadable image and the bad row", s.logs)
	}
}

// A blob_tmp the plugin can't write to is retried from the checkpoint.
func TestImportBlobTmpWriteErrorIsRetryable(t *testing.T) {
	e := testEnv(t)
	e.blobTmp = filepath.Join(e.blobTmp, "missing")
	_, err := run(context.Background(), e, func(string, any) error { return nil }, importParams("testdata/dump.sql", ""))
	var rpc *protocol.Error
	if !errors.As(err, &rpc) || !rpc.Retryable() {
		t.Fatalf("err = %v, want a retryable error", err)
	}
}
