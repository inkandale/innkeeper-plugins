package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"

	"github.com/inkandale/innkeeper/protocol"
)

const (
	risuRealm  = "https://realm.risuai.net"
	risuImages = "https://sv.risuai.xyz/resource/"
)

var uuidPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// risu resolves RisuRealm pages. The card it fetches already says
// risurealm:{uuid} in data.source.
func risu(ctx context.Context, f *fetcher, u *url.URL) (found, error) {
	id, ok := risuPage(u.Path)
	if !ok {
		return found{}, protocol.InvalidParams(errors.New("a RisuRealm URL looks like https://realm.risuai.net/character/{uuid}"))
	}
	item := risuURL(id)
	var page struct {
		Nodes []*struct {
			Data []json.RawMessage `json:"data"`
		} `json:"nodes"`
	}
	if err := f.getJSON(ctx, "RisuRealm", item+"/__data.json", &page); err != nil {
		return found{}, err
	}
	for _, n := range page.Nodes {
		if n == nil {
			continue
		}
		if typ, img, ok := risuCard(n.Data); ok {
			return risuDownload(ctx, f, item, id, typ, img)
		}
	}
	return found{}, permanent(errors.New("RisuRealm sent no card data"))
}

// risuPage reads /character/{uuid}. id is lowercase.
func risuPage(p string) (id string, ok bool) {
	id, ok = strings.CutPrefix(strings.TrimSuffix(p, "/"), "/character/")
	id = strings.ToLower(id)
	return id, ok && uuidPattern.MatchString(id)
}

func risuURL(id string) string { return risuRealm + "/character/" + id }

// risuSource is the source for a card's UUID. RisuRealm has no lorebook pages.
func risuSource(ref string, lorebook bool) (protocol.Source, bool) {
	ref = strings.ToLower(ref)
	if lorebook || !uuidPattern.MatchString(ref) {
		return protocol.Source{}, false
	}
	return protocol.Source{Kind: "risu", Ref: ref, URL: risuURL(ref)}, true
}

func risuPageSource(p string) (protocol.Source, bool) {
	id, ok := risuPage(p)
	if !ok {
		return protocol.Source{}, false
	}
	return risuSource(id, false)
}

// risuCard reads card.type and card.img from one node's devalue data.
func risuCard(data []json.RawMessage) (typ, img string, ok bool) {
	at := func(i int) json.RawMessage {
		if i < 0 || i >= len(data) {
			return nil
		}
		return data[i]
	}
	var root, c map[string]int
	if len(data) == 0 || json.Unmarshal(data[0], &root) != nil {
		return "", "", false
	}
	i, ok := root["card"]
	if !ok || json.Unmarshal(at(i), &c) != nil {
		return "", "", false
	}
	_ = json.Unmarshal(at(c["type"]), &typ)
	if j, ok := c["img"]; ok {
		_ = json.Unmarshal(at(j), &img)
	}
	return typ, img, true
}

// risuDownload fetches the card in the one format its type allows; RisuRealm
// refuses the other.
func risuDownload(ctx context.Context, f *fetcher, item, id, typ, img string) (found, error) {
	switch typ {
	case "normal":
		b, err := f.get(ctx, "RisuRealm", risuRealm+"/api/v1/download/json-v3/"+id)
		if err != nil {
			return found{}, err
		}
		got := found{item: item, card: b}
		if img != "" {
			if got.avatar, err = f.getAvatar(ctx, "RisuRealm", risuImages+url.PathEscape(img)); err != nil {
				return found{}, err
			}
		}
		return got, nil
	case "charx":
		// A JPEG with the CharX appended; card.Decode returns its icon as the avatar.
		b, err := f.get(ctx, "RisuRealm", risuRealm+"/api/v1/download/charx-v3/"+id)
		return found{item: item, card: b}, err
	}
	return found{}, permanent(fmt.Errorf("RisuRealm card type %q isn't supported", typ))
}
