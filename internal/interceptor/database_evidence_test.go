//go:build linux || darwin

package interceptor

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestDatabaseEvidenceArchives(t *testing.T) {
	for _, name := range []string{"database-evidence.tar", "database-evidence-restored.tar"} {
		t.Run(name, func(t *testing.T) {
			members := nativeEvidenceMembers(t, name)
			id := nativeEvidenceID(t, members)
			if strings.Contains(name, "restored") {
				for _, m := range members {
					if m.header.Name == "restore-source/checkpoint.json" {
						var cp Checkpoint
						_ = json.Unmarshal(m.data, &cp)
						id.ParentSessionID = cp.SourceSessionID
						id.ParentCheckpointID = cp.ID
						id.ParentCheckpointDigest = cp.Hash
					}
				}
			}
			v, err := verifyMembers(t, members, id)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(name, "restored") {
				facts := v.Receipt().Database
				if facts == nil || len(facts.Events) != 1 || facts.Events[0].ObservedAttemptID != "attempt-1" {
					t.Fatalf("lost observed database evidence: %+v", facts)
				}
			}
			missing := []testMember{}
			for _, m := range members {
				if strings.HasPrefix(m.header.Name, "blobs/") && string(m.data) == "fixture logical backup bytes" {
					continue
				}
				missing = append(missing, m)
			}
			if _, err = verifyMembers(t, missing, id); err == nil {
				t.Fatal("missing backup accepted")
			}
			for i := range members {
				if strings.HasPrefix(members[i].header.Name, "blobs/") && string(members[i].data) == "fixture logical backup bytes" {
					members[i].data[0] = 'X'
				}
			}
			if _, err = verifyMembers(t, members, id); err == nil {
				t.Fatal("corrupt backup accepted")
			}
		})
	}
}
func TestDatabaseCheckpointMetadata(t *testing.T) {
	for _, value := range []string{`{}`, `{"version":"18.6/0.8.7","unknown":1}`, `{"version":"x","oracle_baselines":{"refund":null}}`, `{"version":"x","oracle_baselines":{"refund":{}}}`} {
		if validDatabaseCheckpoint(json.RawMessage(value)) {
			t.Fatal("invalid metadata accepted", value)
		}
	}
	if !validDatabaseCheckpoint(json.RawMessage(`{"version":"18.6/0.8.7","oracle_baselines":{"refund":[[0]]}}`)) {
		t.Fatal("valid metadata rejected")
	}
}
