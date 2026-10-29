package capabilities

import (
	"encoding/json"
	"github.com/intrusiveai/operator_sandbox/contracts"
	"os"
	"strings"
	"testing"
)

func TestPostgreSQLNativeExportAndAdmission(t *testing.T) {
	raw, err := os.ReadFile("testdata/postgresql-native.json")
	if err != nil {
		t.Fatal(err)
	}
	c := catalogForTest(t)
	e, err := FromNative(c, raw, "delivery-example")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = Import(c, e.PublicJSON(), raw, e.TargetID()); err != nil {
		t.Fatal(err)
	}
	facts := e.ExecutionFacts()
	if facts.Services[0].DatabaseTables[0].ID != "documents" || facts.Operations[0].Delivery == nil {
		t.Fatal("lost database or delivery facts")
	}
	b := object(t, fixture(t, "submitted-bundle.json"))
	b["target_requirements"].(map[string]any)["capability_source_digest"] = e.SourceDigest()
	b["target_requirements"].(map[string]any)["capability_projection_digest"] = e.ProjectionDigest()
	// A scenario can invoke the code adapter without requesting any injection.
	b["target_requirements"].(map[string]any)["required_capability_refs"] = []any{"operation:ask"}
	b["scenarios"] = []any{}
	bundle, err := ParseBundle(c, marshal(t, b))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = Check(bundle, e, bind(t, c, e, "black-box"), Policy{AllowedRefs: []string{"operation:ask", "evidence:target_output"}}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(e.PublicJSON()), "synthetic-password") || strings.Contains(string(e.PublicJSON()), "SELECT") {
		t.Fatal("private database configuration leaked")
	}
	for name, change := range map[string]func(*nativeManifest){
		"duplicate keys":  func(n *nativeManifest) { n.Services[0].DatabaseTables[0].Keys = []string{"id", "id"} },
		"overlap fields":  func(n *nativeManifest) { n.Services[0].DatabaseTables[0].Fields = []string{"id"} },
		"invalid field":   func(n *nativeManifest) { n.Services[0].DatabaseTables[0].Fields = []string{"x;drop"} },
		"creation bound":  func(n *nativeManifest) { n.Services[0].DatabaseTables[1].MaxRecords = 0 },
		"wrong transport": func(n *nativeManifest) { n.Services[0].Transports = []string{"http"} },
		"non database":    func(n *nativeManifest) { n.Services[0].Kind = "fixture_http" },
	} {
		t.Run(name, func(t *testing.T) {
			var n nativeManifest
			_ = json.Unmarshal(raw, &n)
			change(&n)
			n.Digest = ""
			encoded, _ := json.Marshal(n)
			n.Digest = contracts.RawDigest(encoded)
			encoded, _ = json.Marshal(n)
			if _, err := FromNative(c, encoded, e.TargetID()); err == nil {
				t.Fatal("accepted malformed database capabilities")
			}
		})
	}
	corrupt := append([]byte{}, raw...)
	corrupt = []byte(strings.Replace(string(corrupt), "documents", "tampered", 1))
	if _, err = FromNative(c, corrupt, e.TargetID()); err == nil {
		t.Fatal("digest tampering accepted")
	}
}
