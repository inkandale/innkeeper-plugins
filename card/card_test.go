package card

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

var update = flag.Bool("update", false, "rewrite testdata/*.want.json from the current output")

// TestGolden decodes each card in testdata, compares it with
// <file>.want.json, and checks it survives PNG and V2 JSON round trips.
func TestGolden(t *testing.T) {
	files, err := filepath.Glob("testdata/*")
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, f := range files {
		if strings.HasSuffix(f, ".want.json") || strings.HasSuffix(f, ".md") || strings.HasSuffix(f, ".lorebook.json") {
			continue
		}
		n++
		t.Run(filepath.Base(f), func(t *testing.T) {
			in, err := os.ReadFile(f)
			if err != nil {
				t.Fatal(err)
			}
			card, img, err := Decode(in)
			if err != nil {
				t.Fatal(err)
			}
			if *update {
				var buf bytes.Buffer
				if err := json.Indent(&buf, card, "", "  "); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(f+".want.json", append(buf.Bytes(), '\n'), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			want, err := os.ReadFile(f + ".want.json")
			if err != nil {
				t.Fatalf("%v; run go test ./card -update and review the file", err)
			}
			if !sameJSON(t, card, want) {
				t.Fatalf("decoded card differs from %s.want.json:\n%s", f, card)
			}

			out, ext, err := Encode(card, FormatPNG, img)
			if err != nil || ext != "png" {
				t.Fatalf("encode png: %v %q", err, ext)
			}
			card2, img2, err := Decode(out)
			if err != nil {
				t.Fatal(err)
			}
			if !sameJSON(t, card2, card) {
				t.Fatalf("PNG round trip changed the card:\n%s", card2)
			}
			if img != nil && !bytes.Equal(img2, img) {
				t.Fatal("PNG round trip changed the image")
			}

			out, ext, err = Encode(card, FormatJSONV2, nil)
			if err != nil || ext != "json" {
				t.Fatalf("encode json-v2: %v %q", err, ext)
			}
			// V2 JSON keeps only data. Writers like SillyTavern put extra fields at
			// the top of a V3 card, and Decode adds group_only_greetings when missing.
			if card2, _, err = Decode(out); err != nil || !sameJSON(t, dataOf(t, card2), dataOf(t, card)) {
				t.Fatalf("V2 JSON round trip changed the card data: %v\n%s", err, card2)
			}
		})
	}
	if n < 4 {
		t.Fatalf("found %d golden cards, want at least 4: V2 and V3, each as PNG and JSON", n)
	}
}

func TestDecodeV1AndV2(t *testing.T) {
	card, img, err := Decode([]byte(`{"name": "Old", "description": "A V1 card."}`))
	if err != nil || img != nil {
		t.Fatal(err, img)
	}
	want := `{"spec": "chara_card_v3", "spec_version": "3.0", "data": {"name": "Old", "description": "A V1 card.", "group_only_greetings": []}}`
	if !sameJSON(t, card, []byte(want)) {
		t.Fatalf("V1: %s", card)
	}
	card, _, err = Decode([]byte("\xef\xbb\xbf" + `{"spec": "chara_card_v2", "spec_version": "2.0", "data": {"name": "Two", "group_only_greetings": ["Hi."]}}`))
	want = `{"spec": "chara_card_v3", "spec_version": "3.0", "data": {"name": "Two", "group_only_greetings": ["Hi."]}}`
	if err != nil || !sameJSON(t, card, []byte(want)) {
		t.Fatalf("V2: %v %s", err, card)
	}
	card, _, err = Decode([]byte(`{"spec": "chara_card_v2", "spec_version": "2.0", "data": {"name": "Two", "group_only_greetings": null}}`))
	want = `{"spec": "chara_card_v3", "spec_version": "3.0", "data": {"name": "Two", "group_only_greetings": []}}`
	if err != nil || !sameJSON(t, card, []byte(want)) {
		t.Fatalf("V2 null greetings: %v %s", err, card)
	}
}

func TestDecodePygmalionCard(t *testing.T) {
	card, _, err := Decode([]byte(`{"char_name": "Junko", "char_persona": "Clumsy.", "world_scenario": "A cafe.",
		"char_greeting": "Hi!", "example_dialogue": "<START>"}`))
	want := `{"spec": "chara_card_v3", "spec_version": "3.0", "data": {"name": "Junko", "description": "Clumsy.",
		"scenario": "A cafe.", "first_mes": "Hi!", "mes_example": "<START>", "group_only_greetings": []}}`
	if err != nil || !sameJSON(t, card, []byte(want)) {
		t.Fatalf("%v %s", err, card)
	}
}

func TestDecodeRokosBasiliskCard(t *testing.T) {
	card, _, err := Decode([]byte(`{"dbId": "a0cf", "author": {"username": "0394", "displayName": "1602"},
		"interactions": 463, "name": "Koro", "publicDesc": "Just some guy", "internalDesc": "Smug.",
		"exampleDialog": "", "greeting": "*Leans on a wall.*", "privacy": "Public", "defsPrivacy": "Open",
		"categories": [], "avatar": "c9b4.png"}`))
	want := `{"spec": "chara_card_v3", "spec_version": "3.0", "data": {"name": "Koro", "description": "Smug.",
		"first_mes": "*Leans on a wall.*", "mes_example": "", "creator": "0394", "creator_notes": "Just some guy",
		"group_only_greetings": []}}`
	if err != nil || !sameJSON(t, card, []byte(want)) {
		t.Fatalf("%v %s", err, card)
	}
}

func TestDecodePrefersCCv3(t *testing.T) {
	// Not the placeholder, which decodes as no avatar.
	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewGray(image.Rect(0, 0, 2, 3))); err != nil {
		t.Fatal(err)
	}
	img := buf.Bytes()
	chunks, _ := readChunks(img)
	v2 := b64([]byte(`{"spec": "chara_card_v2", "spec_version": "2.0", "data": {"name": "Old"}}`))
	v3 := b64([]byte(`{"spec": "chara_card_v3", "spec_version": "3.0", "data": {"name": "New"}}`))
	end := chunks[len(chunks)-1]
	chunks = append(chunks[:len(chunks)-1], textChunk("ccv3", v3), textChunk("chara", v2), end)
	card, got, err := Decode(writeChunks(chunks))
	if err != nil || !strings.Contains(string(card), `"New"`) {
		t.Fatalf("%v %s", err, card)
	}
	if !bytes.Equal(got, img) {
		t.Fatal("card chunks left in the image")
	}
}

func TestDecodeV3InCharaChunk(t *testing.T) {
	img, err := placeholder()
	if err != nil {
		t.Fatal(err)
	}
	chunks, _ := readChunks(img)
	v3 := `{"spec": "chara_card_v3", "spec_version": "3.0", "data": {"name": "Only"}}`
	end := chunks[len(chunks)-1]
	chunks = append(chunks[:len(chunks)-1], textChunk("chara", b64([]byte(v3))), end)
	card, _, err := Decode(writeChunks(chunks))
	if err != nil || string(card) != v3 {
		t.Fatalf("%v %s", err, card)
	}
}

func TestDecodeRejects(t *testing.T) {
	img, err := placeholder()
	if err != nil {
		t.Fatal(err)
	}
	for name, in := range map[string][]byte{
		"text":           []byte("hello"),
		"unknown spec":   []byte(`{"spec": "chara_card_v9", "data": {}}`),
		"no name":        []byte(`{"foo": 1}`),
		"name only":      []byte(`{"name": "x"}`),
		"name, other":    []byte(`{"name": "x", "model": "gpt"}`),
		"V2 no data":     []byte(`{"spec": "chara_card_v2"}`),
		"char_name only": []byte(`{"char_name": "x"}`),
		"Roko list":      []byte(`[{"name": "x", "internalDesc": "y"}]`),
		"plain PNG":      img,
	} {
		if _, _, err := Decode(in); !errors.Is(err, ErrNotCard) {
			t.Errorf("%s: err = %v, want ErrNotCard", name, err)
		}
	}
}

func TestEncodeConvertsAvatar(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 4, 6))
	src.Set(1, 1, color.RGBA{R: 255, A: 255})
	var jpg bytes.Buffer
	if err := jpeg.Encode(&jpg, src, nil); err != nil {
		t.Fatal(err)
	}
	card := []byte(`{"spec": "chara_card_v3", "spec_version": "3.0", "data": {"name": "Brakka"}}`)
	out, _, err := Encode(card, FormatPNG, jpg.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := png.DecodeConfig(bytes.NewReader(out))
	if err != nil || cfg.Width != 4 || cfg.Height != 6 {
		t.Fatalf("output %v %+v, want a 4x6 PNG", err, cfg)
	}
	if _, _, err := Encode(card, "gif", nil); !errors.Is(err, ErrUnknownFormat) {
		t.Fatalf("unknown format: %v", err)
	}
}

func sameJSON(t *testing.T, a, b []byte) bool {
	t.Helper()
	var x, y any
	if err := json.Unmarshal(a, &x); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, &y); err != nil {
		t.Fatal(err)
	}
	return reflect.DeepEqual(x, y)
}

// dataOf returns a card's data with group_only_greetings defaulted to [].
func dataOf(t *testing.T, card []byte) []byte {
	t.Helper()
	var c struct{ Data map[string]json.RawMessage }
	if err := json.Unmarshal(card, &c); err != nil {
		t.Fatal(err)
	}
	if _, ok := c.Data["group_only_greetings"]; !ok {
		c.Data["group_only_greetings"] = json.RawMessage("[]")
	}
	b, err := json.Marshal(c.Data)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestEncodeRejectsHugeAvatar(t *testing.T) {
	var g bytes.Buffer
	if err := gif.Encode(&g, image.NewPaletted(image.Rect(0, 0, 1, 1), color.Palette{color.Black, color.White}), nil); err != nil {
		t.Fatal(err)
	}
	b := g.Bytes()
	b[6], b[7], b[8], b[9] = 0x20, 0x4e, 0x20, 0x4e // 20000 x 20000
	card := []byte(`{"spec": "chara_card_v3", "spec_version": "3.0", "data": {"name": "Big"}}`)
	if _, _, err := Encode(card, FormatPNG, b); err == nil || !strings.Contains(err.Error(), "limit") {
		t.Fatalf("GIF err = %v, want the pixel limit", err)
	}
	// A PNG whose IHDR claims 20000x20000, with a valid CRC.
	img, err := placeholder()
	if err != nil {
		t.Fatal(err)
	}
	chunks, err := readChunks(img)
	if err != nil {
		t.Fatal(err)
	}
	chunks[0].data = append([]byte{0, 0, 0x4e, 0x20, 0, 0, 0x4e, 0x20}, chunks[0].data[8:]...)
	if _, _, err := Encode(card, FormatPNG, writeChunks(chunks)); err == nil || !strings.Contains(err.Error(), "limit") {
		t.Fatalf("PNG err = %v, want the pixel limit", err)
	}
}

func FuzzDecode(f *testing.F) {
	files, _ := filepath.Glob("testdata/*")
	for _, name := range files {
		if b, err := os.ReadFile(name); err == nil && !strings.HasSuffix(name, ".want.json") && !strings.HasSuffix(name, ".md") {
			f.Add(b)
		}
	}
	for _, s := range []string{"", "{}", `{"name": "x"}`, `{"spec": "chara_card_v2", "data": {}}`, "\x89PNG\r\n\x1a\n"} {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, in []byte) {
		card, img, err := Decode(in)
		if err != nil {
			return
		}
		for _, format := range []string{FormatPNG, FormatJSONV2} {
			Encode(card, format, img)
		}
	})
}

func TestAnAvatarlessCardRoundTripsWithoutAnAvatar(t *testing.T) {
	in := json.RawMessage(`{"spec":"chara_card_v3","spec_version":"3.0","data":{"name":"Brakka","first_mes":"Hail."}}`)
	file, _, err := Encode(in, FormatPNG, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, img, err := Decode(file); err != nil || img != nil {
		t.Fatalf("decode gave a %d-byte avatar, err %v", len(img), err)
	}
}
