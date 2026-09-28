package capabilities

import (
	"bytes"
	"encoding/json"
	"github.com/intrusiveai/operator_sandbox/contracts"
	"github.com/intrusiveai/operator_sandbox/internal/httpstarget"
	"github.com/intrusiveai/operator_sandbox/schemas"
	"os"
	"testing"
)

func TestHTTPSProjectionFixture(t *testing.T) {
	raw, e := os.ReadFile("../../schemas/fixtures/https-capability-chain.json")
	if e != nil {
		t.Fatal(e)
	}
	var f struct{ Mapping, Source, Public json.RawMessage }
	if json.Unmarshal(raw, &f) != nil {
		t.Fatal("fixture")
	}
	p, e := contracts.LoadProtocol(schemas.Files)
	if e != nil {
		t.Fatal(e)
	}
	m, e := httpstarget.Parse(f.Mapping)
	if e != nil {
		t.Fatal(e)
	}
	export, e := FromHTTPS(p.Catalog(), m, "https-agent")
	if e != nil {
		t.Fatal(e)
	}
	for _, pair := range [][2][]byte{{f.Source, export.NativeJSON()}, {f.Public, export.PublicJSON()}} {
		a, _ := contracts.Canonicalize(pair[0], contracts.OrdinaryLimit)
		b, _ := contracts.Canonicalize(pair[1], contracts.OrdinaryLimit)
		if !bytes.Equal(a, b) {
			t.Fatalf("fixture drift: %s\n%s", a, b)
		}
	}
	if _, e = Import(p.Catalog(), export.PublicJSON(), export.NativeJSON(), "https-agent"); e != nil {
		t.Fatal(e)
	}
	if bytes.Contains(export.PublicJSON(), []byte("agent.example.com")) || bytes.Contains(export.NativeJSON(), []byte("/chat")) {
		t.Fatal("private endpoint leaked")
	}
	bad := bytes.Replace(export.PublicJSON(), []byte(`"adapter":"https/v1"`), []byte(`"adapter":"interceptor/v1"`), 1)
	if _, e = p.Catalog().Validate(contracts.TargetCapabilityManifestSchema, bad, contracts.OrdinaryLimit); e == nil {
		t.Fatal("mixed adapter identities")
	}
}
