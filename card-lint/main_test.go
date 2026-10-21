package main

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/inkandale/innkeeper/protocol"
)

// englishText is ordinary card prose, long enough to clear every size threshold.
const englishText = "She is a quiet librarian who has kept the old tower archive for most of her life. " +
	"When a stranger comes in from the rain, she will watch them from behind the shelves before she says anything at all. " +
	"She keeps a cat named Ink and a ledger of every book that was never returned."

// cleanCard trips no check.
func cleanCard() map[string]string {
	return map[string]string{
		"name":        "Mira",
		"description": "{{char}} is Mira. " + englishText,
		"personality": "Patient, dry, and fond of the people who are lost in her stacks.",
		"scenario":    "{{user}} has come into the archive to wait out a storm.",
		"first_mes":   "Mira looks up from her ledger. \"You are dripping on the floor, and that is a first edition.\"",
	}
}

func lintCard(t *testing.T, config map[string]any, card map[string]string) map[string]json.RawMessage {
	t.Helper()
	s, err := parseSettings(config)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(card)
	res, err := enrich(s, mustJSON(t, protocol.EnrichRunParams{Run: "R1",
		Revisions: []protocol.EnrichRevision{{CardID: 1, RevisionID: 10, Data: data}}}))
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Results) != 1 || res.Results[0].Error != "" {
		t.Fatalf("results = %+v", res.Results)
	}
	return res.Results[0].Values
}

func mustJSON(t *testing.T, v any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func number(t *testing.T, values map[string]json.RawMessage, key string) float64 {
	t.Helper()
	var n float64
	if err := json.Unmarshal(values[key], &n); err != nil {
		t.Fatalf("%s = %s: %v", key, values[key], err)
	}
	return n
}

func problems(t *testing.T, values map[string]json.RawMessage) []string {
	t.Helper()
	var p []string
	if err := json.Unmarshal(values["problems"], &p); err != nil || p == nil {
		t.Fatalf("problems = %s: %v", values["problems"], err)
	}
	return p
}

func TestCleanCardScoresFullWithNoProblems(t *testing.T) {
	v := lintCard(t, nil, cleanCard())
	if got := number(t, v, "lint"); got != 100 {
		t.Errorf("lint = %v, want 100", got)
	}
	if p := problems(t, v); len(p) != 0 {
		t.Errorf("problems = %v, want none", p)
	}
	if number(t, v, "tokens") <= 0 {
		t.Errorf("tokens = %s, want an estimate", v["tokens"])
	}
}

func TestEachCheckListsItsCodeAndTakesItsPoints(t *testing.T) {
	cases := map[string]struct {
		edit   func(c map[string]string)
		points float64
	}{
		"no_name":          {func(c map[string]string) { c["name"] = " " }, 100},
		"no_description":   {func(c map[string]string) { c["description"] = ""; c["personality"] += " " + englishText }, 100},
		"no_first_message": {func(c map[string]string) { c["first_mes"] = "" }, 100},
		"oversized":        {func(c map[string]string) { c["system_prompt"] = strings.Repeat("The rain does not stop. ", 700) }, 20},
		"repeated_text":    {func(c map[string]string) { c["scenario"] += "\n\n" + englishText }, 15},
		"first_message_copies_description": {func(c map[string]string) {
			c["description"] = englishText + "\n\n{{char}} is Mira."
			c["first_mes"] = englishText + "\n\n\"Hello.\""
		}, 15},
		"placeholder": {func(c map[string]string) { c["scenario"] = "<bot> has let {{user}} in from the storm." }, 15},
		"no_char_macro": {func(c map[string]string) {
			c["description"] = englishText
			c["first_mes"] = "\"You are dripping on the floor.\""
		}, 10},
		"markup":   {func(c map[string]string) { c["personality"] = "<div style=\"color:red\">Patient and dry.</div>" }, 10},
		"mojibake": {func(c map[string]string) { c["personality"] = "Patient, dry, and fond of cafÃ© regulars." }, 10},
	}
	for code, tc := range cases {
		t.Run(code, func(t *testing.T) {
			card := cleanCard()
			tc.edit(card)
			v := lintCard(t, nil, card)
			if p := problems(t, v); !slices.Equal(p, []string{code}) {
				t.Fatalf("problems = %v, want [%s]", p, code)
			}
			if got := number(t, v, "lint"); got != 100-tc.points {
				t.Errorf("lint = %v, want %v", got, 100-tc.points)
			}
		})
	}
}

func TestShortCardIsTooShort(t *testing.T) {
	v := lintCard(t, nil, map[string]string{"name": "Mira", "description": "{{char}} is a librarian, and she is kind to all of you.",
		"first_mes": "Mira looks up from her ledger and smiles at you."})
	if p := problems(t, v); !slices.Contains(p, "too_short") {
		t.Errorf("problems = %v, want too_short", p)
	}
}

func TestHundredPointProblemZeroesTheScore(t *testing.T) {
	card := cleanCard()
	card["first_mes"] = ""
	card["personality"] = "<b>Patient</b>, dry, and fond of cafÃ© regulars."
	if got := number(t, lintCard(t, nil, card), "lint"); got != 0 {
		t.Errorf("lint = %v, want 0", got)
	}
}

func TestLanguageNamesTheCardsLanguage(t *testing.T) {
	texts := map[string]string{
		"Spanish": "Ella es una bibliotecaria tranquila que ha cuidado el archivo de la vieja torre durante casi toda su vida. " +
			"Cuando un extraño entra huyendo de la lluvia, lo observa desde detrás de los estantes antes de decir nada.",
		"Mandarin": "她是一位安静的图书管理员，几乎一生都在看守古塔里的档案馆。当一个陌生人从雨中走进来时，她会先在书架后面观察对方，然后才开口说话。",
		"Russian": "Она тихая библиотекарша, которая почти всю жизнь хранит архив старой башни. " +
			"Когда незнакомец заходит с дождя, она сначала наблюдает за ним из-за полок и только потом говорит. " +
			"У неё есть кошка по имени Чернила и толстая книга, в которую она записывает каждую книгу, которую так и не вернули. " +
			"Она редко улыбается, но если вы принесёте ей чай, она расскажет вам историю о каждом томе на верхней полке.",
	}
	if got := text(t, lintCard(t, nil, cleanCard()), "language"); got != "English" {
		t.Errorf("English card: language = %q", got)
	}
	for want, body := range texts {
		card := cleanCard()
		card["description"], card["first_mes"] = "{{char}} "+body, body
		if got := text(t, lintCard(t, nil, card), "language"); got != want {
			t.Errorf("language = %q, want %s", got, want)
		}
	}
	short := cleanCard()
	short["description"], short["first_mes"] = "{{char}}", "Hi."
	if got := text(t, lintCard(t, nil, short), "language"); got != unknownLanguage {
		t.Errorf("too short to tell: language = %q, want %s", got, unknownLanguage)
	}
}

// longEnglishText is card prose long and varied enough to fill whatlanggo's
// trigram table, past which it reported English as unreliable.
const longEnglishText = `Captain Odessa Vane commands the Meridian, a battered cargo hauler that runs medicine and machine parts between the outer moons. She inherited the ship from her grandmother, along with a mountain of debt, a temperamental reactor, and a crew who trust her more than they trust each other. Odessa is blunt, quick to laugh, and slow to forgive. She keeps a brass compass on her desk that has not pointed north in thirty years, because it reminds her that every heading is a choice.

Her quartermaster, Bellamy Quist, is a former customs inspector who knows exactly how smugglers hide things because he spent a decade finding them. He cooks elaborate breakfasts whenever the crew is anxious, and the galley smells of cardamom, burnt toast, and engine grease. The pilot, a wiry teenager called Juniper, talks to the navigation computer as though it were an elderly aunt and claims it answers back. The ship's doctor, Hollis Okonkwo, quietly collects jazz records from planets that no longer exist.

The Meridian is cramped and loud. Pipes knock behind the bulkheads, the gravity plating hiccups near the cargo bay, and the observation lounge has a cracked window patched with polymer foam and optimism. Everyone aboard has a reason for leaving somewhere, and nobody asks too many questions. Odessa enforces three rules: pay your share, protect the cargo, and never lie to the captain about fuel.

Lately the jobs have dried up. A corporate consortium has bought most of the shipping lanes and squeezed independent haulers with inspection fees, docking surcharges, and paperwork designed to exhaust anyone without a legal department. Odessa refuses to sell. She took a risky contract to deliver a sealed crate to a mining colony on the frozen moon Calder, no questions asked, for triple the usual rate. The crate hums faintly at night. Juniper swears it moved.

Odessa speaks plainly and swears creatively, mixing dockside slang with surprisingly formal phrases she picked up from her grandmother. When she is worried she becomes very polite. When she is angry she goes silent and starts cleaning her pistol, a heavy antique revolver that has been rebuilt so many times that none of the original parts remain. She is fiercely loyal, terrible at accepting help, and secretly writes letters to her estranged brother that she never sends.

The story begins as the Meridian drops out of transit near Calder Station. Snow storms scour the surface, the docking beacon flickers, and the colony has not answered hails for two days. Odessa must decide whether to land, turn back and forfeit the payment, or open the crate and learn what she has really been carrying across the dark.

Calder Station was built forty years ago by a cooperative of ice miners who wanted nothing from the inner worlds except to be left alone. They dug tunnels through glaciers, farmed algae under violet lamps, and elected a new mayor every winter by throwing snowballs at a painted target. The colony's archive is a single dusty server kept in the chapel basement, guarded by a retired schoolteacher named Augustin Pell who believes the machine is haunted by the ghost of a poet.

Odessa remembers visiting Calder as a child. Her grandmother traded spices for fish oil and let her ride the funicular up to the observatory, where an astronomer showed her the rings of the gas giant through a telescope held together with wire and prayer. She remembers the cold most of all: a dry, biting cold that froze eyelashes and made every breath feel like swallowing glass. She also remembers a boy who gave her a carved wooden whale and asked her to come back someday.

The crew has opinions about the job. Bellamy thinks the client is a front for weapons dealers and has been quietly calculating how much fuel they would need to vanish into the asteroid belt. Hollis is worried about the medical supplies the colony ordered months ago, which never arrived. Juniper mostly wants to know whether there are penguins on Calder, and has been disappointed to learn that there are not, although there are enormous shaggy goats that eat lichen and headbutt anyone wearing yellow.

Weather reports from the surface are grim. Katabatic winds pour off the northern ridge at two hundred kilometres per hour, burying antennae and cracking solar panels. The only safe landing window lasts about forty minutes, and the approach requires threading a narrow canyon lined with jagged blue ice. Juniper has practised the manoeuvre in simulation nineteen times and crashed in eleven of them, which she describes as a promising trend.

Somewhere below, lights blink in a pattern that might be a distress code or might be a malfunctioning greenhouse. The crate in the hold has grown warm to the touch. Odessa pours herself a small glass of her grandmother's plum brandy, takes a single sip, and gives the order to begin descent.`

func TestLanguageNamesALongCardEnglish(t *testing.T) {
	card := cleanCard()
	card["description"], card["first_mes"] = "{{char}} "+longEnglishText, longEnglishText
	if got := text(t, lintCard(t, nil, card), "language"); got != "English" {
		t.Errorf("language = %q, want English", got)
	}
}

func text(t *testing.T, values map[string]json.RawMessage, key string) string {
	t.Helper()
	var s string
	if err := json.Unmarshal(values[key], &s); err != nil {
		t.Fatalf("%s = %s: %v", key, values[key], err)
	}
	return s
}

func TestSettingsChangeThresholds(t *testing.T) {
	v := lintCard(t, map[string]any{"max_tokens": 10}, cleanCard())
	if p := problems(t, v); !slices.Equal(p, []string{"oversized"}) {
		t.Fatalf("problems = %v, want [oversized]", p)
	}
	card := cleanCard()
	card["scenario"] = "[[NAME]] has let {{user}} in from the storm."
	if p := problems(t, lintCard(t, map[string]any{"placeholders": []string{"[[name]]"}}, card)); !slices.Equal(p, []string{"placeholder"}) {
		t.Errorf("problems = %v, want [placeholder]", p)
	}
}

func TestSettingsDefaultFromPluginToml(t *testing.T) {
	s, err := parseSettings(nil)
	if err != nil {
		t.Fatal(err)
	}
	if s.MinTokens != 50 || s.MaxTokens != 4000 || s.RepeatWords != 20 || len(s.Placeholders) == 0 {
		t.Errorf("defaults = %+v", s)
	}
}

func TestUndecodableRevisionGetsAnError(t *testing.T) {
	s, _ := parseSettings(nil)
	res, err := enrich(s, mustJSON(t, protocol.EnrichRunParams{Run: "R1", Revisions: []protocol.EnrichRevision{
		{CardID: 1, RevisionID: 10, Data: json.RawMessage(`[1]`)},
	}}))
	if err != nil {
		t.Fatal(err)
	}
	if res.Results[0].Error == "" || res.Results[0].Values != nil {
		t.Errorf("result = %+v, want an error and no values", res.Results[0])
	}
}
