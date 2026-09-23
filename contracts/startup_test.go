package contracts

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"testing"

	"github.com/intrusive-ai/operator-sandbox/schemas"
)

type startupFixture struct {
	Name      string            `json:"name"`
	Mode      string            `json:"mode"`
	Direction string            `json:"direction"`
	Document  json.RawMessage   `json:"document"`
	Messages  []json.RawMessage `json:"messages"`
	Tree      string            `json:"tree_base64"`
	Set       string            `json:"skill_set_base64"`
	Skills    []string          `json:"skills_base64"`
	Valid     bool              `json:"valid"`
}

func startupFixtures(t *testing.T) []startupFixture {
	t.Helper()
	raw, err := os.ReadFile("../schemas/fixtures/startup-manifests.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []startupFixture
	if err = json.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}
	return cases
}

func TestSharedStartupFixtures(t *testing.T) {
	p, err := LoadProtocol(schemas.Files)
	if err != nil {
		t.Fatal(err)
	}
	cases := startupFixtures(t)
	for _, c := range cases {
		t.Run(c.Name, func(t *testing.T) {
			decode := func(s string) []byte {
				b, err := base64.StdEncoding.DecodeString(s)
				if err != nil {
					t.Fatal(err)
				}
				return b
			}
			var messages, skills [][]byte
			for _, m := range c.Messages {
				messages = append(messages, m)
			}
			for _, s := range c.Skills {
				skills = append(skills, decode(s))
			}
			var err error
			switch c.Mode {
			case "control":
				_, err = p.ValidateControl(c.Direction, c.Document)
			case "startup":
				err = p.ValidateStartup(messages)
			case "input_tree":
				_, err = p.ValidateInputTree(c.Document)
			case "skill_manifest":
				_, err = p.ValidateSkillManifest(c.Document)
			case "skill_set":
				_, err = p.ValidateSkillSet(c.Document)
			case "manifest_set":
				err = p.ValidateManifestSet(decode(c.Tree), decode(c.Set), skills)
			case "startup_inputs":
				err = p.ValidateStartupInputs(messages, decode(c.Tree), decode(c.Set), skills)
			default:
				t.Fatal("unknown fixture mode")
			}
			if (err == nil) != c.Valid {
				t.Fatalf("valid=%v want=%v error=%v", err == nil, c.Valid, err)
			}
			if err != nil && !errors.Is(err, ErrProtocol) && !errors.Is(err, ErrSchema) && !errors.Is(err, ErrLimit) && !errors.Is(err, ErrJSON) {
				t.Fatal("unexpected error", err)
			}
		})
	}
	t.Logf("%d shared startup/manifest cases", len(cases))
}

func TestStartupEncodedLimits(t *testing.T) {
	p, err := LoadProtocol(schemas.Files)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range startupFixtures(t) {
		var maximum int
		var validate func([]byte) (map[string]any, error)
		switch c.Name {
		case "control bootstrap":
			maximum = ControlLimit
			validate = func(b []byte) (map[string]any, error) { return p.ValidateControl("host", b) }
		case "complete input inventory":
			maximum = InputTreeManifestLimit
			validate = p.ValidateInputTree
		case "complete skill inventory":
			maximum = SkillManifestLimit
			validate = p.ValidateSkillManifest
		case "selected skill set":
			maximum = ControlLimit
			validate = p.ValidateSkillSet
		default:
			continue
		}
		t.Run(c.Name, func(t *testing.T) {
			raw := append([]byte(nil), c.Document...)
			raw = append(raw, bytes.Repeat([]byte(" "), maximum-len(raw))...)
			if _, err := validate(raw); err != nil {
				t.Fatal(err)
			}
			if _, err := validate(append(raw, ' ')); !errors.Is(err, ErrLimit) {
				t.Fatal("byte ceiling not enforced", err)
			}
		})
	}
}
