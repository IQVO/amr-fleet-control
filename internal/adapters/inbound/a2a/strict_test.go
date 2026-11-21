package a2a

import (
	"testing"

	"github.com/a2aproject/a2a-go/v2/a2a"
)

// carry (plan 04 Task 21 review lesson): one JSON document means the decoder's NEXT token is io.EOF (dec.More is
// false on a stray `}` or `]`, so the brief's Decode let them through), and keys match the json names EXACTLY
// (encoding/json matches case-insensitively, so `FleetID` filled fleetId and `Skill` filled skill).
func TestStrictDecodingRefusesTrailingDelimitersAndCaseFoldedKeys(t *testing.T) {
	client := agent(t, fakeReads{})
	for name, c := range map[string]struct {
		part *a2a.Part
		want string
	}{
		"a trailing }":              {a2a.NewTextPart(`{"skill":"get_fleet","arguments":{"fleetId":"AMR1"}}}`), "malformed-skill-request"},
		"a trailing ]":              {a2a.NewTextPart(`{"skill":"get_fleet","arguments":{"fleetId":"AMR1"}}]`), "malformed-skill-request"},
		"a second document":         {a2a.NewTextPart(`{"skill":"get_fleet","arguments":{"fleetId":"AMR1"}} {}`), "malformed-skill-request"},
		"the exact request":         {a2a.NewTextPart(`{"skill":"get_fleet","arguments":{"fleetId":"AMR1"}}`), ""},
		"a case-folded argument":    {skill("get_fleet", map[string]any{"FleetID": "AMR1"}), "invalid-argument"},
		"a case-folded leg id":      {skill("get_leg", map[string]any{"fleetId": "AMR1", "LegId": "leg-1"}), "invalid-argument"},
		"a case-folded request key": {a2a.NewDataPart(map[string]any{"Skill": "get_fleet", "arguments": map[string]any{"fleetId": "AMR1"}}), "malformed-skill-request"},
		"case-folded arguments key": {a2a.NewDataPart(map[string]any{"skill": "get_fleet", "Arguments": map[string]any{"fleetId": "AMR1"}}), "malformed-skill-request"},
	} {
		env, _ := send(t, client, c.part)
		if c.want == "" {
			if env["problem"] != nil || env["data"] == nil {
				t.Errorf("%s: envelope %v, want the data", name, env)
			}
			continue
		}
		if problemType(env) != c.want || env["data"] != nil {
			t.Errorf("%s: envelope %v, want only the %s problem", name, env, c.want)
		}
	}
}

func TestStrictDecodingStillAcceptsTheExactKeys(t *testing.T) {
	for name, raw := range map[string]string{
		"exact keys":         `{"fleetId":"AMR1","legId":"leg-1"}`,
		"trailing blanks":    "{\"fleetId\":\"AMR1\"} \n\t",
		"empty arguments":    ``,
		"null arguments":     `null`,
		"an empty object":    `{}`,
		"insignificant keys": `{ "fleetId" : "AMR1" }`,
	} {
		var a args
		if err := decodeStrict([]byte(raw), &a); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
}
