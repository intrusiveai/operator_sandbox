//go:build linux || darwin

package interceptor

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
)

func nativeEvidenceMembers(t *testing.T, name string) []testMember {
	t.Helper()
	raw, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	reader := tar.NewReader(bytes.NewReader(raw))
	var out []testMember
	for {
		header, err := reader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(reader)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, testMember{*header, data})
	}
	return out
}
func nativeEvidenceID(t *testing.T, members []testMember) EvidenceIdentity {
	t.Helper()
	for _, m := range members {
		if m.header.Name == "session.json" {
			var meta evidenceMetadata
			if err := json.Unmarshal(m.data, &meta); err != nil {
				t.Fatal(err)
			}
			return EvidenceIdentity{CampaignID: meta.CampaignID, SessionID: meta.ID, EnvironmentDigest: meta.EnvironmentDigest, ApplicationDigest: meta.AppDigest, CapabilityDigest: meta.CapabilityManifestDigest, FeedbackProfile: meta.FeedbackProfile}
		}
	}
	t.Fatal("metadata missing")
	return EvidenceIdentity{}
}
func verifyMembers(t *testing.T, m []testMember, id EvidenceIdentity) (*VerifiedEvidence, error) {
	t.Helper()
	data := archiveBytes(t, m)
	q := evidenceQuery()
	q.SessionID = id.SessionID
	q.CampaignID = id.CampaignID
	c := evidenceClient(func(*http.Request) (*http.Response, error) { return evidenceResponse(data), nil })
	d, err := c.DownloadEvidence(context.Background(), q, privateEvidenceDir(t))
	if err != nil {
		return nil, err
	}
	t.Cleanup(func() { d.Close() })
	a, err := d.InspectArchive(context.Background(), DefaultArchiveLimits(4<<30))
	if err != nil {
		return nil, err
	}
	return a.VerifyProvenance(context.Background(), id)
}
func TestNativeEvidenceGolden(t *testing.T) {
	for _, name := range []string{"native-evidence.tar", "native-evidence-partial.tar"} {
		t.Run(name, func(t *testing.T) {
			members := nativeEvidenceMembers(t, name)
			v, err := verifyMembers(t, members, nativeEvidenceID(t, members))
			if err != nil {
				t.Fatal(err)
			}
			expected := "complete"
			if strings.Contains(name, "partial") {
				expected = "partial"
			}
			r := v.Receipt()
			if r.State != expected || r.EventCount != 2 || r.StateCount != 3 || !digest.MatchString(r.BundleDigest) {
				t.Fatal(r)
			}
			if expected == "partial" {
				r.Gaps[0] = "changed"
				if v.Receipt().Gaps[0] == "changed" {
					t.Fatal("mutable receipt")
				}
			}
		})
	}
}
func TestNativeEvidenceRejectsTampering(t *testing.T) {
	for _, name := range []string{"session.json", "manifest.json", "events.jsonl", "state.jsonl", "execution.json", "file-manifest.json", "evidence-bundle.json", "artifacts.json", "attempts.json", "final-evaluation.json"} {
		t.Run(name, func(t *testing.T) {
			members := nativeEvidenceMembers(t, "native-evidence.tar")
			id := nativeEvidenceID(t, members)
			for i := range members {
				if members[i].header.Name == name {
					members[i].data = bytes.Replace(members[i].data, []byte("{"), []byte(`{"unknown":true,`), 1)
				}
			}
			if _, err := verifyMembers(t, members, id); err == nil {
				t.Fatal("tampered native member accepted")
			}
		})
	}
	for _, change := range []string{"campaign", "session", "profile", "environment", "blob-missing", "checkpoint-missing", "execution-journal", "bundle-hash", "event-chain", "closed"} {
		t.Run(change, func(t *testing.T) {
			members := nativeEvidenceMembers(t, "native-evidence.tar")
			id := nativeEvidenceID(t, members)
			switch change {
			case "campaign":
				id.CampaignID = "other"
			case "session":
				id.SessionID = "other"
			case "profile":
				id.FeedbackProfile = "diagnostic"
			case "environment":
				id.EnvironmentDigest = rawDigest([]byte("other"))
			}
			for i := 0; i < len(members); i++ {
				m := &members[i]
				if change == "blob-missing" && strings.HasPrefix(m.header.Name, "blobs/") || change == "checkpoint-missing" && strings.HasPrefix(m.header.Name, "checkpoints/") {
					members = append(members[:i], members[i+1:]...)
					break
				}
				if change == "execution-journal" && m.header.Name == "execution.json" {
					m.data = append(m.data, '\n')
				}
				if change == "bundle-hash" && m.header.Name == "evidence-bundle.json" {
					m.data = bytes.Replace(m.data, []byte(`"complete"`), []byte(`"partial"`), 1)
				}
				if change == "event-chain" && m.header.Name == "events.jsonl" {
					m.data = bytes.Replace(m.data, []byte(`"seq":1`), []byte(`"seq":2`), 1)
				}
				if change == "closed" && m.header.Name == "execution.json" {
					m.data = bytes.Replace(m.data, []byte(`"closed_at":`), []byte(`"CLOSED_AT":`), 1)
				}
			}
			if _, err := verifyMembers(t, members, id); err == nil {
				t.Fatal("invalid provenance accepted")
			}
		})
	}
}

func TestNativeRestoredEvidence(t *testing.T) {
	source := nativeEvidenceMembers(t, "native-evidence.tar")
	var checkpoint Checkpoint
	for _, m := range source {
		if strings.HasPrefix(m.header.Name, "checkpoints/") {
			if err := json.Unmarshal(m.data, &checkpoint); err != nil {
				t.Fatal(err)
			}
		}
	}
	restored := nativeEvidenceMembers(t, "native-evidence-restored.tar")
	id := nativeEvidenceID(t, restored)
	id.ParentSessionID = checkpoint.SourceSessionID
	id.ParentCheckpointID = checkpoint.ID
	id.ParentCheckpointDigest = checkpoint.Hash
	if _, err := verifyMembers(t, restored, id); err != nil {
		t.Fatal(err)
	}
	id.ParentCheckpointDigest = rawDigest([]byte("different checkpoint"))
	if _, err := verifyMembers(t, restored, id); err == nil {
		t.Fatal("accepted different restored source")
	}
	id.ParentCheckpointDigest = checkpoint.Hash
	for i := range restored {
		if restored[i].header.Name == "restore-source/state.jsonl" {
			restored[i].data = nil
		}
	}
	if _, err := verifyMembers(t, restored, id); err == nil {
		t.Fatal("accepted missing restored state")
	}
}
