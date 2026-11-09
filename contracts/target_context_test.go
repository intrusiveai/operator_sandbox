package contracts

import (
	"bytes"
	"encoding/json"
	"github.com/intrusiveai/operator_sandbox/schemas"
	"os"
	"testing"
)

func TestSharedCompactContext(t *testing.T) {
	p, err := LoadProtocol(schemas.Files)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile("../schemas/fixtures/compact-target-context.json")
	var cases []struct {
		Name         string
		Target, View json.RawMessage
		References   map[string]json.RawMessage
	}
	if err = json.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}
	for _, c := range cases {
		t.Run(c.Name, func(t *testing.T) {
			view, refs, err := p.CompactTargetContext(c.Target)
			if err != nil {
				t.Fatal(err)
			}
			want, _ := Canonicalize(c.View, OrdinaryLimit)
			if !bytes.Equal(view, want) || len(refs) != len(c.References) {
				t.Fatal("shared view differs")
			}
			for id, raw := range refs {
				want, _ := Canonicalize(c.References[id], OrdinaryLimit)
				if !bytes.Equal(raw, want) || id != "target-"+RawDigest(raw)[7:] {
					t.Fatal("reference differs", id)
				}
			}
		})
	}
}
func TestSharedSelectedTools(t *testing.T) {
	p, _ := LoadProtocol(schemas.Files)
	raw, _ := os.ReadFile("../schemas/fixtures/selected-model-tools.json")
	var cases []struct {
		Codec, Digest        string
		Operations, Surfaces []string
		Size                 int `json:"size_bytes"`
	}
	if err := json.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}
	for _, c := range cases {
		b, err := p.ModelToolsSelected(c.Codec, c.Operations, c.Surfaces)
		if err != nil || len(b) != c.Size || RawDigest(b) != c.Digest || len(b) > 31000 {
			t.Fatal(c.Codec, c.Surfaces, err)
		}
	}
	for _, surfaces := range [][]string{{"future"}, {"mcp_tool_result", "database_response"}, {"database_response", "database_response"}} {
		if _, err := p.ModelToolsSelected(cases[0].Codec, cases[0].Operations, surfaces); err == nil {
			t.Fatal("invalid surfaces")
		}
	}
	for _, tool := range []string{"reference_read", "observation_read"} {
		args := []byte(`{"reference":"reference-1","offset":0}`)
		if tool == "observation_read" {
			args = []byte(`{"observation":"obs-1","offset":0}`)
		}
		v, err := p.ValidateToolArguments(tool, args)
		if err != nil || number(v["max_bytes"]) != 4096 {
			t.Fatal(tool, v, err)
		}

		for _, value := range []string{"0", "-1", "1048577", "null", "1.5", "\"4096\""} {
			bad := append([]byte(nil), args[:len(args)-1]...)
			bad = append(bad, []byte(",\"max_bytes\":"+value+"}")...)
			if _, err := p.ValidateToolArguments(tool, bad); err == nil {
				t.Fatal("invalid supplied read bound accepted", tool, value)
			}
		}
	}
}
