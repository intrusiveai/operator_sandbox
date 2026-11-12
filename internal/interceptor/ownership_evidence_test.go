//go:build linux || darwin

package interceptor

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/intrusiveai/operator_sandbox/internal/contractcheck"
	"github.com/intrusiveai/operator_sandbox/internal/diagnostictext"
)

// Supplied exports stay private and unchanged. The case file supplies identities
// from Operator's saved evidence selections, independently of archive contents.
func TestSuppliedNativeEvidence(t *testing.T) {
	path := os.Getenv("OPERATOR_NATIVE_EVIDENCE_CASES")
	if path == "" {
		t.Skip("set OPERATOR_NATIVE_EVIDENCE_CASES to private verification cases")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		Path     string
		Identity EvidenceIdentity
		SHA256   string
		Bytes    int64
	}
	if err := json.Unmarshal(raw, &cases); err != nil || len(cases) == 0 {
		t.Fatal("invalid evidence cases", err)
	}
	for _, tc := range cases {
		t.Run(tc.Identity.SessionID, func(t *testing.T) {
			file, err := os.Open(tc.Path)
			if err != nil {
				t.Fatal(err)
			}
			defer file.Close()
			receipt := EvidenceReceipt{CampaignID: tc.Identity.CampaignID, SessionID: tc.Identity.SessionID, SHA256: tc.SHA256, Bytes: tc.Bytes, LocalMaxBytes: 1 << 30, InterceptorMaxBytes: 1 << 30}
			verified, err := VerifyEvidenceFile(context.Background(), file, receipt, tc.Identity)
			if err != nil {
				t.Fatal(err)
			}
			r := verified.Receipt()
			t.Logf("verified unchanged bytes: %s; state=%s events=%d transactions=%d restored=%t", r.Transfer.SHA256, r.State, r.EventCount, r.StateCount, tc.Identity.ParentSessionID != "")
		})
	}
}

func ownershipID(t *testing.T, members []testMember) EvidenceIdentity {
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
	return id
}

func TestOwnershipNativeEvidence(t *testing.T) {
	for _, name := range []string{"ownership-evidence.tar", "ownership-evidence-partial.tar", "ownership-evidence-restored.tar"} {
		t.Run(name, func(t *testing.T) {
			members := nativeEvidenceMembers(t, name)
			v, err := verifyMembers(t, members, ownershipID(t, members))
			if err != nil {
				t.Fatal(err)
			}
			want := "complete"
			if strings.Contains(name, "partial") {
				want = "partial"
			}
			if v.Receipt().State != want {
				t.Fatal(v.Receipt())
			}
		})
	}
}

func TestOwnershipNativeEvidenceRejections(t *testing.T) {
	for _, tc := range []struct {
		name, file, find, replace, want string
		rehash                          bool
	}{
		{"manifest-owner", "file-manifest.json", `"owner":"application"`, `"owner":"root"`, "owner", true},
		{"manifest-owner-empty", "file-manifest.json", `"owner":"application"`, `"owner":""`, "owner", true},
		{"manifest-owner-null", "file-manifest.json", `"owner":"application"`, `"owner":null`, "owner", false},
		{"manifest-owner-missing", "file-manifest.json", `"owner":"application",`, ``, "owner", true},
		{"manifest-version", "file-manifest.json", `"schema_version":2`, `"schema_version":3`, "unsupported version 3; supported versions: 1, 2", true},
		{"legacy-owner", "file-manifest.json", `"schema_version":2`, `"schema_version":1`, "owner", true},
		{"manifest-hash", "file-manifest.json", `"owner":"application"`, `"owner":"supervisor"`, "digest", false},
		{"journal-owner", "state.jsonl", `"owner":"application"`, `"owner":"root"`, "owner", true},
		{"journal-owner-empty", "state.jsonl", `"owner":"application"`, `"owner":""`, "owner", true},
		{"journal-unknown", "state.jsonl", `"owner":"application"`, `"owner":"application","surprise":true`, "surprise", false},
		{"journal-hash", "state.jsonl", `"owner":"application"`, `"owner":"supervisor"`, "hash", false},
		{"source-owner", "restore-source/state.jsonl", `"owner":"application"`, `"owner":"root"`, "owner", true},
		{"source-hash", "restore-source/state.jsonl", `"owner":"application"`, `"owner":"supervisor"`, "hash", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			name := "ownership-evidence.tar"
			if strings.HasPrefix(tc.file, "restore-source/") {
				name = "ownership-evidence-restored.tar"
			}
			members := nativeEvidenceMembers(t, name)
			id := ownershipID(t, members)
			changed := false
			for i := range members {
				m := &members[i]
				if m.header.Name != tc.file {
					continue
				}
				if tc.file == "file-manifest.json" {
					var compact bytes.Buffer
					if err := json.Compact(&compact, m.data); err != nil {
						t.Fatal(err)
					}
					m.data = compact.Bytes()
				}
				if !bytes.Contains(m.data, []byte(tc.find)) {
					t.Fatal("mutation not applied")
				}
				m.data = bytes.Replace(m.data, []byte(tc.find), []byte(tc.replace), 1)
				changed = true
				if tc.rehash {
					if tc.file == "file-manifest.json" {
						var v evidenceManifest
						if err := json.Unmarshal(m.data, &v); err != nil {
							t.Fatal(err)
						}
						v.Digest = ""
						v.Digest = nativeHash(v)
						m.data, _ = json.Marshal(v)
					} else {
						lines := bytes.Split(m.data, []byte("\n"))
						for j, line := range lines {
							if !bytes.Contains(line, []byte(tc.replace)) {
								continue
							}
							var tx evidenceTransaction
							if err := json.Unmarshal(line, &tx); err != nil {
								t.Fatal(err)
							}
							tx.Hash = ""
							tx.Hash = nativeHash(tx)
							lines[j], _ = json.Marshal(tx)
							break
						}
						m.data = bytes.Join(lines, []byte("\n"))
					}
				}
			}
			if !changed {
				t.Fatal("missing member")
			}
			_, err := verifyMembers(t, members, id)
			if !errors.Is(err, ErrProvenance) {
				t.Fatal("lost provenance classification", err)
			}
			message, ok := diagnostictext.FromError(err)
			if !ok || !strings.Contains(message, tc.file) || !strings.Contains(message, tc.want) {
				t.Fatalf("missing location/rule: %s (%v)", message, err)
			}
			if strings.HasSuffix(tc.file, "jsonl") && !strings.Contains(message, "record ") {
				t.Fatal("lost record number", message)
			}
		})
	}
}

func TestEvidenceShapeDiagnosticSafety(t *testing.T) {
	for _, field := range []string{"unknown", "authorization: Bearer synthetic-secret", strings.Repeat("x", 1000), "unsafe\nfield"} {
		raw, _ := json.Marshal(map[string]any{"kind": "directory_ensure", "path": "/state/app", field: "private-value-not-for-diagnostics"})
		var op evidenceOperation
		err := strictEvidence(raw, &op)
		message, ok := diagnostictext.FromError(err)
		if !ok || !strings.Contains(message, "unknown field") || strings.Contains(message, "synthetic-secret") || strings.Contains(message, "private-value-not") || strings.Contains(message, "\n") || len(message) > 800 {
			t.Fatal(message, err)
		}
	}
}

// Native hashes depend on Go field ordering and omitempty, not just JSON shape.
func TestInterceptorContractEvidenceSerialization(t *testing.T) {
	d, err := contractcheck.Load("openapi.json")
	if err != nil {
		t.Fatal(err)
	}
	for name, model := range map[string]any{"snapshot.Manifest": evidenceManifest{}, "snapshot.Entry": evidenceEntry{}, "statejournal.Transaction": evidenceTransaction{}, "statejournal.Operation": evidenceOperation{}} {
		schema := d.At("#/components/schemas/" + name)
		typ := reflect.TypeOf(model)
		var order, omit []string
		for i := 0; i < typ.NumField(); i++ {
			tag := strings.Split(typ.Field(i).Tag.Get("json"), ",")
			order = append(order, tag[0])
			if slices.Contains(tag, "omitempty") {
				omit = append(omit, tag[0])
			}
		}
		for key, actual := range map[string][]string{"x-native-json-order": order, "x-native-json-omitempty": omit} {
			var expected []string
			for _, v := range schema[key].([]any) {
				expected = append(expected, v.(string))
			}
			if !slices.Equal(actual, expected) {
				t.Fatalf("%s %s differs: %v != %v", name, key, actual, expected)
			}
		}
	}
}

func TestInterceptorContractEvidenceFixtures(t *testing.T) {
	d, err := contractcheck.Load("openapi.json")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"ownership-evidence.tar", "ownership-evidence-partial.tar", "ownership-evidence-restored.tar"} {
		members := nativeEvidenceMembers(t, name)
		for _, m := range members {
			pointer := ""
			records := [][]byte{m.data}
			switch m.header.Name {
			case "file-manifest.json":
				pointer = "#/components/schemas/snapshot.Manifest"
			case "state.jsonl", "restore-source/state.jsonl":
				pointer = "#/components/schemas/statejournal.Transaction"
				records = bytes.Split(bytes.TrimSpace(m.data), []byte("\n"))
			}
			if pointer == "" {
				continue
			}
			for i, raw := range records {
				if err := d.Validate(pointer, raw); err != nil {
					t.Fatalf("%s %s record %d: %v", name, m.header.Name, i+1, err)
				}
			}
		}
		if _, err := verifyMembers(t, members, ownershipID(t, members)); err != nil {
			t.Fatal(name, err)
		}
	}
}
