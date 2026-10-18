package main

import (
	"context"
	"encoding/json"
	"io"
	"slices"
	"strings"
	"testing"

	"github.com/inkandale/innkeeper/protocol"
)

var good = map[string]string{
	"name":        "Brakka",
	"description": strings.Repeat("A towering orc courier who never misses a delivery. ", 10),
	"first_mes":   "*The door bangs open and rain blows in behind her.* \"Parcel for you. Sign here, and quickly: the river's rising and I have six more stops.\"",
}

func with(changes map[string]string) map[string]string {
	card := map[string]string{}
	for k, v := range good {
		card[k] = v
	}
	for k, v := range changes {
		card[k] = v
	}
	return card
}

func TestChecks(t *testing.T) {
	cases := []struct {
		name    string
		card    map[string]string
		quality float64
		issues  []string
	}{
		{"clean", good, 100, []string{}},
		{"empty description", with(map[string]string{"description": "An orc."}), 60, []string{"empty description"}},
		{"thin description", with(map[string]string{"description": strings.Repeat("x", 200)}), 85, []string{"thin description"}},
		{"no greeting", with(map[string]string{"first_mes": ""}), 65, []string{"no greeting"}},
		{"short greeting", with(map[string]string{"first_mes": strings.Repeat("x", 60)}), 90, []string{"short greeting"}},
		{"template text", with(map[string]string{"scenario": "{{char}} meets [CHARACTER NAME] at the docks."}), 75, []string{"template text"}},
		{"case-sensitive TODO", with(map[string]string{"scenario": "TODO: write the scenario"}), 75, []string{"template text"}},
		{"Spanish todo is fine", with(map[string]string{"first_mes": good["first_mes"] + " Todo bien, amigo."}), 100, []string{}},
		{"very long", with(map[string]string{"mes_example": strings.Repeat("x", 40000)}), 85, []string{"very long"}},
		{"several", map[string]string{"name": "X", "description": "", "first_mes": ""}, 25, []string{"empty description", "no greeting"}},
	}
	for _, c := range cases {
		data, _ := json.Marshal(c.card)
		got := check(data)
		if got.quality != c.quality || !slices.Equal(got.issues, c.issues) {
			t.Errorf("%s: quality %v issues %q, want %v %q", c.name, got.quality, got.issues, c.quality, c.issues)
		}
	}
}

func TestEnrichRunReturnsDeclaredValues(t *testing.T) {
	m, err := protocol.LoadManifest(".")
	if err != nil {
		t.Fatal(err)
	}
	toPlugin, fromHost := io.Pipe()
	toHost, fromPlugin := io.Pipe()
	plugin := protocol.NewConn(toPlugin, fromPlugin, handle)
	host := protocol.NewConn(toHost, fromHost, func(context.Context, string, json.RawMessage) (any, error) { return nil, nil })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go plugin.Run(ctx)
	go host.Run(ctx)
	data, _ := json.Marshal(with(map[string]string{"first_mes": ""}))
	var res protocol.EnrichRunResult
	err = host.Call(ctx, protocol.MethodEnrichRun, protocol.EnrichRunParams{Run: "R", Revisions: []protocol.EnrichRevision{
		{CardID: 1, RevisionID: 10, Data: data}, {CardID: 2, RevisionID: 11, Data: json.RawMessage(`"not an object"`)}}}, &res)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Results) != 2 || res.Results[0].RevisionID != 10 || res.Results[1].Error == "" {
		t.Fatalf("results %+v", res.Results)
	}
	for k, v := range res.Results[0].Values {
		d, ok := m.Enrichments[k]
		if !ok {
			t.Errorf("%s isn't declared", k)
		} else if err := d.Check(v); err != nil {
			t.Errorf("%s: %v", k, err)
		}
	}
	if string(res.Results[0].Values["issues"]) != `["no greeting"]` || !strings.Contains(string(res.Results[0].Meta), rulesVersion) {
		t.Fatalf("values %s meta %s", res.Results[0].Values, res.Results[0].Meta)
	}
}
