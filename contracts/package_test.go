package contracts

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"io/fs"
	"os"
	"testing"

	"github.com/intrusive-ai/operator-sandbox/schemas"
)

func TestSharedPackageIntegrity(t *testing.T) {
	p, err := LoadProtocol(schemas.Files)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile("../schemas/fixtures/package-integrity.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		Name      string            `json:"name"`
		Mode      string            `json:"mode"`
		Valid     bool              `json:"valid"`
		Manifest  string            `json:"manifest_base64"`
		Expected  PackageIdentity   `json:"expected"`
		Files     map[string]string `json:"files_base64"`
		Profiles  map[string]string `json:"profiles"`
		Canonical string            `json:"canonical_base64"`
	}
	if err := json.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}
	for _, c := range cases {
		t.Run(c.Name, func(t *testing.T) {
			decode := func(raw string) []byte {
				v, e := base64.StdEncoding.DecodeString(raw)
				if e != nil {
					t.Fatal(e)
				}
				return v
			}
			manifest := decode(c.Manifest)
			files := map[string][]byte{}
			for name, raw := range c.Files {
				files[name] = decode(raw)
			}
			var err error
			switch c.Mode {
			case "manifest":
				_, err = p.ValidatePackageManifest(manifest)
			case "verify":
				err = p.VerifyPackage(manifest, files, c.Expected)
			case "build":
				var built []byte
				var identity PackageIdentity
				built, identity, err = p.BuildPackageManifest(c.Expected.Version, files, c.Profiles)
				if err == nil && (!bytes.Equal(built, decode(c.Canonical)) || identity != c.Expected) {
					t.Fatal("builder differs from golden bytes/identity")
				}
			default:
				t.Fatal("unknown fixture mode")
			}
			if (err == nil) != c.Valid {
				t.Fatalf("valid=%v want=%v error=%v", err == nil, c.Valid, err)
			}
		})
	}
	t.Logf("%d shared package integrity cases", len(cases))
}

func packageTestFiles(t *testing.T) (map[string][]byte, map[string]string) {
	t.Helper()
	entries, err := fs.ReadDir(schemas.Files, ".")
	if err != nil {
		t.Fatal(err)
	}
	files := map[string][]byte{}
	for _, entry := range entries {
		raw, err := fs.ReadFile(schemas.Files, entry.Name())
		if err != nil {
			t.Fatal(err)
		}
		files[entry.Name()] = raw
	}
	profiles := map[string]string{"jcs-v1": "semantics/digests.md", "manifest-paths-v1": "semantics/paths.md", "harness-loop-v1": "semantics/limits.md"}
	for _, name := range profiles {
		files[name] = []byte("Fixture semantic profile, not a published release.\n")
	}
	return files, profiles
}

func TestVerifiedPackageLoader(t *testing.T) {
	p, err := LoadProtocol(schemas.Files)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := p.PackageIdentity(); ok {
		t.Fatal("development protocol claims verification")
	}
	files, profiles := packageTestFiles(t)
	manifest, pin, err := p.BuildPackageManifest("0.0.0", files, profiles)
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := p.LoadVerifiedProtocol(manifest, files, pin)
	if err != nil {
		t.Fatal(err)
	}
	actual, ok := loaded.PackageIdentity()
	if !ok || actual != pin {
		t.Fatal("missing loaded package pin")
	}
	if !wireEqual(loaded.RegistryDigests()["catalog_digest"], p.RegistryDigests()["catalog_digest"]) {
		t.Fatal("loaded different catalog")
	}
	// Verify the pinned protocol still validates a real typed control message.
	raw, err := os.ReadFile("../schemas/fixtures/startup-example.json")
	if err != nil {
		t.Fatal(err)
	}
	var messages []json.RawMessage
	if err := json.Unmarshal(raw, &messages); err != nil {
		t.Fatal(err)
	}
	if _, err := loaded.ValidateControl("host", messages[0]); err != nil {
		t.Fatal(err)
	}
	// Package pins are enforced in addition to all existing launch identities.
	data, err := os.ReadFile("../schemas/fixtures/identity-validation.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixtures []inputFixture
	if err := json.Unmarshal(data, &fixtures); err != nil {
		t.Fatal(err)
	}
	c := fixtures[0]
	decode := func(s string) []byte {
		v, e := base64.StdEncoding.DecodeString(s)
		if e != nil {
			t.Fatal(e)
		}
		return v
	}
	launch, skills := [][]byte{}, [][]byte{}
	for _, m := range c.Messages {
		launch = append(launch, m)
	}
	for _, s := range c.Skills {
		skills = append(skills, decode(s))
	}
	if err := p.ValidateLaunchIdentities(launch, decode(c.Tree), decode(c.Set), skills, decode(c.Context), decode(c.Bundle), decode(c.Prompt)); err != nil {
		t.Fatal(err)
	}
	if err := loaded.ValidateLaunchIdentities(launch, decode(c.Tree), decode(c.Set), skills, decode(c.Context), decode(c.Bundle), decode(c.Prompt)); err == nil {
		t.Fatal("different package admitted")
	}
	actual.Version = "changed"
	files["catalog.json"][0] = 'x'
	if current, _ := loaded.PackageIdentity(); current != pin {
		t.Fatal("caller changed loaded identity")
	}
	if _, err := loaded.ValidateControl("host", messages[0]); err != nil {
		t.Fatal("caller mutation changed compiled schemas", err)
	}
	if err := p.VerifyPackage(manifest, files, pin); err == nil {
		t.Fatal("tampered payload verified")
	}
}

func TestPackageVerificationPrecedesSchemaLoading(t *testing.T) {
	p, err := LoadProtocol(schemas.Files)
	if err != nil {
		t.Fatal(err)
	}
	files, profiles := packageTestFiles(t)
	// This exact payload is hash-consistent but references an unavailable schema.
	files["wire-common.schema.json"] = []byte(`{"$schema":"https://json-schema.org/draft/2020-12/schema","$id":"urn:operator:schema:wire-common:v1alpha1","$ref":"https://invalid.example/schema"}`)
	manifest, pin, err := p.BuildPackageManifest("0.0.0", files, profiles)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.LoadVerifiedProtocol(manifest, files, pin); err == nil {
		t.Fatal("external schema reference accepted")
	}
	files, profiles = packageTestFiles(t)
	files["../escape"] = []byte("x")
	if _, _, err := p.BuildPackageManifest("0.0.0", files, profiles); err == nil {
		t.Fatal("unsafe package path accepted")
	}
}
