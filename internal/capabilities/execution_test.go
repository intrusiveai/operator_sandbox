package capabilities

import (
	"slices"
	"testing"
)

func TestExecutionFactsPreserveVerifiedAvailability(t *testing.T) {
	c := catalogForTest(t)
	for _, tc := range []struct {
		name            string
		capable, create bool
	}{
		{"supported", true, true}, {"disabled", false, true}, {"missing-native-operation", true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := mustExport(t, c, nativeVariant(t, func(n map[string]any) {
				n["snapshot_capable"] = tc.capable
				if !tc.create {
					n["cross_vm_operations"] = remove(stringsOf(n["cross_vm_operations"]), "snapshot.create")
				}
			}))
			facts := e.ExecutionFacts()
			if slices.Contains(facts.Features, FeatureSnapshots) != (tc.capable && tc.create) {
				t.Fatal(facts.Features)
			}
			if !slices.Equal(facts.EvidenceKinds, []string{"target_output"}) {
				t.Fatal(facts.EvidenceKinds)
			}
			facts.EvidenceKinds[0] = "changed"
			if len(facts.Features) > 0 {
				facts.Features[0] = "changed"
				if !slices.Contains(e.ExecutionFacts().Features, FeatureSnapshots) {
					t.Fatal("mutable feature facts")
				}
			}
			if e.ExecutionFacts().EvidenceKinds[0] != "target_output" {
				t.Fatal("mutable evidence facts")
			}
		})
	}
}
