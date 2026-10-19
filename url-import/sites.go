package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strings"

	"github.com/inkandale/innkeeper/protocol"
)

// sourceKinds checks a ref of each kind url-import names and gives its source for the entity.
var sourceKinds = map[string]func(ref string, lorebook bool) (protocol.Source, bool){
	"chub":             chubSource,
	"risu":             risuSource,
	"character-tavern": tavernSource,
}

func isLorebook(entity string) (bool, error) {
	switch entity {
	case protocol.KindCard:
		return false, nil
	case protocol.KindLorebook:
		return true, nil
	}
	return false, protocol.InvalidParams(fmt.Errorf("entity %q is not %s or %s", entity, protocol.KindCard, protocol.KindLorebook))
}

// pageSource reads a page URL as its source, with the URL of the page as given.
func pageSource(raw string) (protocol.Source, bool) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return protocol.Source{}, false
	}
	// A RisuRealm card's own data.source says risurealm:{uuid}.
	if u.Scheme == "risurealm" {
		return risuSource(u.Opaque, false)
	}
	site, ok := hosts[strings.ToLower(u.Hostname())]
	if !ok || site.source == nil || (u.Scheme != "http" && u.Scheme != "https") {
		return protocol.Source{}, false
	}
	return site.source(u.Path)
}

// detectSources answers sites.detect.
func detectSources(raw json.RawMessage) (any, error) {
	var p protocol.SitesDetectParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, protocol.InvalidParams(err)
	}
	lorebook, err := isLorebook(p.Entity)
	if err != nil {
		return nil, err
	}
	var data map[string]any
	dec := json.NewDecoder(bytes.NewReader(p.Data))
	dec.UseNumber()
	if err := dec.Decode(&data); err != nil || data == nil {
		return nil, protocol.InvalidParams(errors.New("data must be an object"))
	}
	out := []protocol.Source{}
	add := func(s protocol.Source) {
		// The first wins, so an ID read from the data isn't lost to the same page's URL.
		if len(out) < protocol.MaxSources && !slices.ContainsFunc(out, func(o protocol.Source) bool { return o.Kind == s.Kind && o.Ref == s.Ref }) {
			out = append(out, s)
		}
	}
	if s, ok := chubData(data, lorebook); ok {
		add(s)
	}
	list, _ := data["source"].([]any)
	for _, v := range list {
		page, _ := v.(string)
		s, ok := pageSource(page)
		if !ok {
			continue
		}
		// A page fits the entity when it is the page the entity's source names.
		if fit, ok := sourceKinds[s.Kind](s.Ref, lorebook); ok && fit.URL == s.URL {
			add(s)
		}
	}
	return protocol.SitesDetectResult{Sources: out}, nil
}

// parseSource answers sites.parse.
func parseSource(raw json.RawMessage) (any, error) {
	var p protocol.SitesParseParams
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, protocol.InvalidParams(err)
	}
	lorebook, err := isLorebook(p.Entity)
	if err != nil {
		return nil, err
	}
	if (p.URL != "") == (p.Kind != "" || p.Ref != "") {
		return nil, protocol.InvalidParams(errors.New("send a url, or a kind and ref, not both"))
	}
	kind, ref := p.Kind, p.Ref
	if p.URL != "" {
		s, ok := pageSource(p.URL)
		if !ok {
			return protocol.SitesParseResult{}, nil
		}
		kind, ref = s.Kind, s.Ref
	}
	toSource, ok := sourceKinds[kind]
	if !ok {
		return protocol.SitesParseResult{}, nil
	}
	s, ok := toSource(ref, lorebook)
	if !ok {
		return nil, protocol.InvalidParams(fmt.Errorf("%q is not a valid %s ref for a %s", ref, kind, p.Entity))
	}
	return protocol.SitesParseResult{Source: &s}, nil
}
