package contracts

import (
	"bytes"
	"encoding/json"
	"errors"
	"reflect"
	"sync"
	"testing"
	"testing/fstest"

	"github.com/intrusiveai/operator_sandbox/schemas"
)

// Each engine-context validation checks every installed guide. Measure repeated
// reads separately from protocol loading and first-use preparation.
func BenchmarkToolGuidance(b *testing.B) {
	p, err := LoadProtocol(schemas.Files)
	if err != nil {
		b.Fatal(err)
	}
	names, err := p.ModelToolNames()
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for _, name := range names {
			if _, err := p.ToolGuidance(name); err != nil {
				b.Fatal(err)
			}
		}
	}
}

func TestToolGuidanceConcurrentCopies(t *testing.T) {
	reference, err := LoadProtocol(schemas.Files)
	if err != nil {
		t.Fatal(err)
	}
	names, err := reference.ModelToolNames()
	if err != nil {
		t.Fatal(err)
	}
	expected := map[string][]byte{}
	for _, name := range names {
		expected[name], err = reference.ToolGuidance(name)
		if err != nil {
			t.Fatal(err)
		}
	}
	p, err := LoadProtocol(schemas.Files)
	if err != nil {
		t.Fatal(err)
	}
	// Concurrent first use and mutation of returned data must not change the
	// installed guide, another caller's result, or the catalog's names.
	var workers sync.WaitGroup
	for _, name := range names {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for i := 0; i < 8; i++ {
				got, err := p.ToolGuidance(name)
				if err != nil || !bytes.Equal(got, expected[name]) {
					t.Errorf("guide %s changed: %v", name, err)
					return
				}
				got[0] = 'x'
				returned, err := p.ModelToolNames()
				if err != nil || !reflect.DeepEqual(returned, names) {
					t.Errorf("names changed: %v", err)
					return
				}
				returned[0] = "mutated"
			}
		}()
	}
	workers.Wait()
	if _, err := p.ToolGuidance("unavailable-tool"); !errors.Is(err, ErrProtocol) {
		t.Fatal("unknown guide accepted", err)
	}
	// Reading cached guidance must not make invalid model arguments acceptable.
	if _, err := p.ValidateToolArguments("finish", []byte(`{"finish_reason":"budget-limit","unavailable_reason":"budget-limit"}`)); err == nil {
		t.Fatal("obsolete finish form accepted")
	}
}

func TestToolGuidanceUsesLoadedCatalog(t *testing.T) {
	original, err := LoadProtocol(schemas.Files)
	if err != nil {
		t.Fatal(err)
	}
	before, err := original.ToolGuidance("finish")
	if err != nil {
		t.Fatal(err)
	}
	files, _ := packageTestFiles(t)
	var catalog map[string]any
	if err := json.Unmarshal(files["model-tool-arguments.schema.json"], &catalog); err != nil {
		t.Fatal(err)
	}
	for _, item := range catalog["oneOf"].([]any) {
		branch := item.(map[string]any)
		if branch["properties"].(map[string]any)["name"].(map[string]any)["const"] == "finish" {
			branch["description"] = "Separate installed guide fixture."
		}
	}
	files["model-tool-arguments.schema.json"], err = json.Marshal(catalog)
	if err != nil {
		t.Fatal(err)
	}
	changedFiles := fstest.MapFS{}
	for name, raw := range files {
		changedFiles[name] = &fstest.MapFile{Data: raw}
	}
	changed, err := LoadProtocol(changedFiles)
	if err != nil {
		t.Fatal(err)
	}
	after, err := changed.ToolGuidance("finish")
	if err != nil || !bytes.Contains(after, []byte("Separate installed guide fixture.")) || bytes.Equal(before, after) {
		t.Fatal("guide leaked across protocols", err)
	}
	again, err := original.ToolGuidance("finish")
	if err != nil || !bytes.Equal(before, again) {
		t.Fatal("original protocol was changed", err)
	}
}
