package interceptor

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestInterceptorContractModelResponseEvidence(t *testing.T) {
	for _, name := range []string{"native-evidence.tar", "native-evidence-partial.tar", "native-evidence-restored.tar"} {
		t.Run(name, func(t *testing.T) {
			members := nativeEvidenceMembers(t, "model-response/"+name)
			id := nativeEvidenceID(t, members)
			for _, m := range members {
				if m.header.Name == "restore-source/checkpoint.json" {
					var cp Checkpoint
					if err := json.Unmarshal(m.data, &cp); err != nil {
						t.Fatal(err)
					}
					id.ParentSessionID = cp.SourceSessionID
					id.ParentCheckpointID = cp.ID
					id.ParentCheckpointDigest = cp.Hash
				}
			}
			if _, err := verifyMembers(t, members, id); err != nil {
				t.Fatal(err)
			}
			found := false
			for _, m := range members {
				if strings.HasSuffix(m.header.Name, "state.jsonl") && bytes.Contains(m.data, []byte(`"model_codec":"openai_chat_completions"`)) {
					found = true
				}
			}
			if !found {
				t.Fatal("model response definition missing from current/source journal")
			}
		})
	}
	// Delivered and original bytes are distinct, independently verified blobs.
	members := nativeEvidenceMembers(t, "model-response/native-evidence.tar")
	originals, delivered := 0, 0
	for i := range members {
		if strings.HasPrefix(members[i].header.Name, "blobs/") && bytes.Contains(members[i].data, []byte("original response")) {
			originals++
		}
		if strings.HasPrefix(members[i].header.Name, "blobs/") && bytes.Contains(members[i].data, []byte("altered response")) {
			delivered++
			members[i].data = bytes.ReplaceAll(members[i].data, []byte("altered response"), []byte("changed response"))
		}
	}
	if originals != 1 || delivered != 1 {
		t.Fatalf("missing response evidence: original=%d delivered=%d", originals, delivered)
	}
	if _, err := verifyMembers(t, members, nativeEvidenceID(t, members)); err == nil {
		t.Fatal("altered delivered response accepted")
	}
}
