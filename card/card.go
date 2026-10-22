// Package card reads and writes character card files: CCv3, V2, and V1
// cards as JSON, and as PNG images that carry the card in a text chunk. It
// also reads CharX, a zip holding a CCv3 card.json and its assets, and the
// older Pygmalion and Roko's Basilisk JSON cards.
package card

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	"image/png"
	"strings"
	"unicode"

	_ "golang.org/x/image/webp"
)

var (
	// ErrNotCard means the input isn't a card this package can read.
	ErrNotCard = errors.New("not a character card")
	// ErrUnknownFormat means Encode doesn't write the format.
	ErrUnknownFormat = errors.New("unknown format")
)

// Formats Encode writes.
const (
	FormatPNG    = "png"
	FormatJSONV2 = "json-v2"
)

// maxAvatarPixels bounds the memory a hostile avatar header can make Encode allocate.
const maxAvatarPixels = 4096 * 4096

// PNG text chunk keywords. ccv3 holds a CCv3 card. chara holds a V2 card for
// older apps, or in some writers' files a V3 one.
const (
	keywordV3 = "ccv3"
	keywordV2 = "chara"
)

// Decode reads a card file. It returns the card as CCv3 JSON and, for a PNG,
// the image without its card chunks, or for CharX, the main icon as a PNG.
// A CCv3 card comes back unchanged. Every error wraps ErrNotCard. img is nil
// for JSON input and for Encode's placeholder, which stands for no avatar.
func Decode(b []byte) (card json.RawMessage, img []byte, err error) {
	if isZip(b) {
		return decodeCharX(b)
	}
	if !isPNG(b) {
		card, err = toV3(b)
		return card, nil, err
	}
	chunks, err := readChunks(b)
	if err != nil {
		return nil, nil, fmt.Errorf("%w: %w", ErrNotCard, err)
	}
	found := map[string][]byte{}
	var kept []chunk
	for _, c := range chunks {
		if k, text, ok := c.text(); ok && (k == keywordV3 || k == keywordV2) {
			found[k] = text
			continue
		}
		kept = append(kept, c)
	}
	text, ok := found[keywordV3]
	if !ok {
		if text, ok = found[keywordV2]; !ok {
			return nil, nil, fmt.Errorf("%w: the PNG has no ccv3 or chara chunk", ErrNotCard)
		}
	}
	raw, err := unbase64(text)
	if err != nil {
		return nil, nil, fmt.Errorf("%w: %w", ErrNotCard, err)
	}
	if card, err = toV3(raw); err != nil {
		return nil, nil, err
	}
	if img = writeChunks(kept); IsPlaceholder(img) {
		img = nil
	}
	return card, img, nil
}

// Encode writes a card in format and returns the file and its extension.
// avatar is the card's image in any format image.Decode reads, or nil, which
// gives a PNG a plain placeholder. An animated avatar keeps only its first frame.
func Encode(card json.RawMessage, format string, avatar []byte) ([]byte, string, error) {
	card, err := toV3(card)
	if err != nil {
		return nil, "", err
	}
	v2, err := toV2(card)
	if err != nil {
		return nil, "", err
	}
	switch format {
	case FormatJSONV2:
		return v2, "json", nil
	case FormatPNG:
		img, err := AsPNG(avatar)
		if err != nil {
			return nil, "", err
		}
		chunks, err := readChunks(img)
		if err != nil {
			return nil, "", fmt.Errorf("avatar: %w", err)
		}
		out := make([]chunk, 0, len(chunks)+2)
		for _, c := range chunks {
			if k, _, ok := c.text(); ok && (k == keywordV3 || k == keywordV2) {
				continue
			}
			if c.typ == "IEND" {
				out = append(out, textChunk(keywordV2, b64(v2)), textChunk(keywordV3, b64(card)))
			}
			out = append(out, c)
		}
		return writeChunks(out), "png", nil
	}
	return nil, "", fmt.Errorf("%w %q", ErrUnknownFormat, format)
}

// toV3 converts a CCv3, V2, or V1 card in JSON to CCv3.
func toV3(raw []byte) (json.RawMessage, error) {
	raw = bytes.TrimPrefix(raw, []byte("\xef\xbb\xbf"))
	var top map[string]json.RawMessage
	if err := json.Unmarshal(raw, &top); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrNotCard, err)
	}
	var spec string
	if s, ok := top["spec"]; ok {
		if err := json.Unmarshal(s, &spec); err != nil {
			return nil, fmt.Errorf("%w: spec must be a string", ErrNotCard)
		}
	}
	var data map[string]json.RawMessage
	switch spec {
	case "chara_card_v3", "chara_card_v2":
		if err := json.Unmarshal(top["data"], &data); err != nil || data == nil {
			return nil, fmt.Errorf("%w: data must be an object", ErrNotCard)
		}
		if spec == "chara_card_v3" {
			return json.RawMessage(raw), nil
		}
	case "":
		// V1 cards keep their fields at the top level. Requiring a content field
		// keeps app presets and other JSON with a name from passing as cards.
		if data = v1Data(top); data == nil {
			return nil, fmt.Errorf("%w: no spec, and not a V1 card", ErrNotCard)
		}
	default:
		return nil, fmt.Errorf("%w: unknown spec %q", ErrNotCard, spec)
	}
	if g := data["group_only_greetings"]; len(g) == 0 || string(g) == "null" {
		data["group_only_greetings"] = json.RawMessage("[]")
	}
	return json.Marshal(map[string]any{"spec": "chara_card_v3", "spec_version": "3.0", "data": data})
}

// Pygmalion and Roko's Basilisk cards predate V1 and name its fields differently.
var (
	pygmalionFields = map[string]string{"char_name": "name", "char_persona": "description",
		"world_scenario": "scenario", "char_greeting": "first_mes", "example_dialogue": "mes_example"}
	rokoFields = map[string]string{"name": "name", "internalDesc": "description",
		"greeting": "first_mes", "exampleDialog": "mes_example", "publicDesc": "creator_notes"}
)

// v1Data returns the V1 fields of a V1, Pygmalion, or Roko's Basilisk card,
// or nil for anything else.
func v1Data(top map[string]json.RawMessage) map[string]json.RawMessage {
	if isV1(top) {
		return top
	}
	if data := renamed(top, pygmalionFields); isV1(data) {
		return data
	}
	if _, ok := top["internalDesc"]; !ok {
		return nil
	}
	data := renamed(top, rokoFields)
	if !isV1(data) {
		return nil
	}
	var author struct {
		Username string `json:"username"`
	}
	if json.Unmarshal(top["author"], &author) == nil && author.Username != "" {
		data["creator"], _ = json.Marshal(author.Username)
	}
	return data
}

// renamed returns top's fields named in names, under their new names.
func renamed(top map[string]json.RawMessage, names map[string]string) map[string]json.RawMessage {
	out := map[string]json.RawMessage{}
	for from, to := range names {
		if v, ok := top[from]; ok {
			out[to] = v
		}
	}
	return out
}

// isV1 reports whether top has a name and at least one content field.
func isV1(top map[string]json.RawMessage) bool {
	if _, ok := top["name"]; !ok {
		return false
	}
	for _, k := range []string{"description", "personality", "first_mes", "scenario"} {
		if _, ok := top[k]; ok {
			return true
		}
	}
	return false
}

// toV2 returns a CCv3 card's data under the V2 spec. V2 apps ignore the
// V3-only fields.
func toV2(card json.RawMessage) ([]byte, error) {
	var c struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(card, &c); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrNotCard, err)
	}
	return json.Marshal(map[string]any{"spec": "chara_card_v2", "spec_version": "2.0", "data": c.Data})
}

// AsPNG returns avatar as a PNG, converting other image formats, or the
// placeholder when avatar is empty.
func AsPNG(avatar []byte) ([]byte, error) {
	if len(avatar) == 0 {
		return placeholder()
	}
	// DecodeConfig reads only the header, so the cap applies before any pixels are allocated.
	cfg, _, err := image.DecodeConfig(bytes.NewReader(avatar))
	if err != nil {
		return nil, fmt.Errorf("read avatar: %w", err)
	}
	if cfg.Width <= 0 || cfg.Height <= 0 {
		return nil, errors.New("avatar has no pixels")
	}
	if cfg.Width*cfg.Height > maxAvatarPixels {
		return nil, fmt.Errorf("avatar is %dx%d, over the %d pixel limit", cfg.Width, cfg.Height, maxAvatarPixels)
	}
	if isPNG(avatar) {
		return avatar, nil
	}
	img, _, err := image.Decode(bytes.NewReader(avatar))
	if err != nil {
		return nil, fmt.Errorf("read avatar: %w", err)
	}
	var buf bytes.Buffer
	err = png.Encode(&buf, img)
	return buf.Bytes(), err
}

func b64(b []byte) []byte { return []byte(base64.StdEncoding.EncodeToString(b)) }

// unbase64 decodes a chunk's text, which some writers wrap or leave unpadded.
func unbase64(text []byte) ([]byte, error) {
	s := strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) {
			return -1
		}
		return r
	}, string(text))
	if b, err := base64.StdEncoding.DecodeString(s); err == nil {
		return b, nil
	}
	return base64.RawStdEncoding.DecodeString(s)
}
