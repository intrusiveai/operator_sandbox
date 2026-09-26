package feedback

import (
	"bytes"
	"encoding/json"
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/intrusiveai/operator_sandbox/contracts"
	"github.com/intrusiveai/operator_sandbox/internal/interceptor"
	"github.com/intrusiveai/operator_sandbox/schemas"
)

func catalog(t *testing.T) *contracts.Catalog {
	t.Helper()
	c, err := contracts.LoadCatalog(schemas.Files)
	if err != nil {
		t.Fatal(err)
	}
	return c
}
func encoded(v any) []byte { b, _ := json.Marshal(v); return b }

func TestAllSharedTranslationVectors(t *testing.T) {
	raw, err := os.ReadFile("../../schemas/fixtures/feedback-translation.json")
	if err != nil {
		t.Fatal(err)
	}
	var vectors struct {
		Cases []struct {
			Name  string `json:"name"`
			Input struct {
				Native    string                            `json:"native_profile"`
				Requested string                            `json:"requested_profile"`
				Ceiling   string                            `json:"host_profile_ceiling"`
				Kinds     *[]string                         `json:"host_allowed_kinds"`
				Selection *interceptor.ObservationSelection `json:"observation_selection"`
			} `json:"input"`
			Expected struct {
				Native    string                            `json:"native_attempt_profile"`
				Selection *interceptor.ObservationSelection `json:"native_observation_selection"`
				Collect   bool                              `json:"collect_native_feedback"`
				Allowed   []string                          `json:"allowed_kinds"`
				Manifest  json.RawMessage                   `json:"feedback"`
			} `json:"expected"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(raw, &vectors); err != nil {
		t.Fatal(err)
	}
	c := catalog(t)
	for _, tc := range vectors.Cases {
		t.Run(tc.Name, func(t *testing.T) {
			effective := tc.Input.Native
			for _, ceiling := range []string{tc.Input.Requested, tc.Input.Ceiling} {
				if ceiling != "" && rank(ceiling) < rank(effective) {
					effective = ceiling
				}
			}
			allowed := []string{}
			for _, k := range kinds {
				if permitted(effective, k) && (tc.Input.Kinds == nil || contains(*tc.Input.Kinds, k)) {
					allowed = append(allowed, k)
				}
			}
			p, err := New(tc.Input.Native, effective, allowed, tc.Input.Selection)
			if err != nil {
				t.Fatal(err)
			}
			if p.native != tc.Expected.Native || p.Collect() != tc.Expected.Collect || !reflect.DeepEqual(p.NativeSelection(), tc.Expected.Selection) || !reflect.DeepEqual(p.allowed, tc.Expected.Allowed) {
				t.Fatalf("selection mismatch: %#v, want %#v", p, tc.Expected)
			}
			s, v := view(p)
			var native []byte
			if p.Collect() {
				native = seal(v)
			}
			r, err := Project(c, p, s, "receipt", native, nil, nil, 1<<20)
			if err != nil {
				t.Fatal(err)
			}
			actual, _ := contracts.Canonicalize(r.ManifestJSON(), contracts.OrdinaryLimit)
			expected, _ := contracts.Canonicalize(tc.Expected.Manifest, contracts.OrdinaryLimit)
			if !bytes.Equal(actual, expected) {
				t.Fatalf("manifest got %s want %s", actual, expected)
			}
		})
	}
}
func contains(a []string, s string) bool {
	for _, v := range a {
		if v == s {
			return true
		}
	}
	return false
}

func view(p *Policy) (Source, interceptor.ObservationView) {
	s := Source{CampaignID: "campaign", SessionID: "original-session", AttemptID: "attempt", AttemptContextDigest: contracts.RawDigest([]byte("context")), TurnID: "turn-1", RunRevision: 1, SessionRevision: 10}
	now := time.Date(2026, 9, 25, 0, 0, 0, 0, time.UTC)
	v := interceptor.ObservationView{APIVersion: "interceptor.dev/observation-view/v1alpha2", ReceiptID: interceptor.FeedbackReceiptID(s.SessionID, s.TurnID), CampaignID: s.CampaignID, SessionID: s.SessionID, AttemptID: s.AttemptID, AttemptContextDigest: s.AttemptContextDigest, TurnID: s.TurnID, FeedbackProfile: p.native, SessionRevision: s.SessionRevision, CapturedAt: now, WindowStart: now, WindowEnd: now, CollectionState: "complete", Categories: []interceptor.FeedbackCategory{}, Entries: []interceptor.FeedbackEntry{}, Observations: []interceptor.Observation{}, Operation: interceptor.OperationView{State: "SUCCEEDED", ReceiptID: s.TurnID}}
	for _, k := range kinds {
		v.Categories = append(v.Categories, interceptor.FeedbackCategory{Kind: k, State: "empty"})
	}
	return s, v
}
func seal(v interceptor.ObservationView) []byte {
	v.Hash = interceptor.ObservationViewDigest(v)
	return encoded(v)
}
func entry(v *interceptor.ObservationView, id, kind string, content []byte, visibility interceptor.Visibility) {
	e := interceptor.FeedbackEntry{ID: id, Kind: kind, Visibility: visibility, Source: "untrusted metadata", Assurance: "untrusted metadata", Availability: "available", Artifact: &interceptor.ArtifactDescriptor{Digest: contracts.RawDigest(content), SizeBytes: int64(len(content)), MediaType: "application/json", Canonicalization: "raw"}, OriginalSizeBytes: int64(len(content))}
	v.Entries = append(v.Entries, e)
	for i := range v.Categories {
		if v.Categories[i].Kind == kind {
			v.Categories[i].State = "available"
		}
	}
}

func TestProjectionFiltersAliasesProtectedAndHiddenData(t *testing.T) {
	p, _ := New("oracle-assisted", "black-box", []string{"target_output"}, nil)
	s, v := view(p)
	entry(&v, "output", "target_output", []byte("hello"), interceptor.TargetVisible)
	entry(&v, "private", "operation_error", []byte("SECRET"), interceptor.HarnessVisible)
	entry(&v, "protected", "target_output", []byte("SECRET"), interceptor.Protected)
	v.TargetOutput.Inline = "SECRET"
	v.Observations = []interceptor.Observation{{Kind: "oracle_outcome", Visibility: interceptor.Protected, Value: "SECRET"}}
	v.CollectionState = "partial" // hidden failure cannot make permitted feedback partial
	r, err := Project(catalog(t), p, s, "receipt", seal(v), map[string][]byte{"output": []byte("hello")}, nil, 1024)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(r.ManifestJSON(), []byte("SECRET")) || bytes.Contains(r.ManifestJSON(), []byte("untrusted")) {
		t.Fatal("metadata leak")
	}
	var m Manifest
	_ = json.Unmarshal(r.ManifestJSON(), &m)
	if len(m.Entries) != 1 || m.CollectionState != "complete" {
		t.Fatalf("unexpected manifest %s", r.ManifestJSON())
	}
	id := m.Entries[0].ID
	for _, off := range []int64{0, 5} {
		b, err := r.Read("campaign", "receipt", id, off, 5, true, []string{"target_output"})
		if err != nil {
			t.Fatal(err)
		}
		if _, err = catalog(t).Validate("urn:operator:schema:engine-observation-read-result:v1alpha1", b, contracts.OrdinaryLimit); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct {
		campaign, receipt, id string
		offset                int64
		admitted              bool
		allowed               []string
	}{
		{"foreign", "receipt", id, 0, true, []string{"target_output"}}, {"campaign", "foreign", id, 0, true, []string{"target_output"}}, {"campaign", "receipt", "private", 0, true, kinds}, {"campaign", "receipt", id, 6, true, kinds}, {"campaign", "receipt", id, 0, false, kinds}, {"campaign", "receipt", id, 0, true, nil},
	} {
		if _, err := r.Read(tc.campaign, tc.receipt, tc.id, tc.offset, 5, tc.admitted, tc.allowed); err == nil {
			t.Fatal("invalid read admitted")
		}
	}
	// Reads have no current-session parameter: original attribution remains fixed.
	if !bytes.Contains(r.RecordJSON(), []byte("original-session")) {
		t.Fatal("lost binding")
	}
}

func TestProjectionNormalizesAndBounds(t *testing.T) {
	p, _ := New("oracle-assisted", "oracle-assisted", kinds, nil)
	s, v := view(p)
	b := []byte(`{"injection_id":"native-injection","state":"applied","scope":"turn","event_seq":9007199254740993,"secret":"SECRET"}`)
	entry(&v, "delivery", "injection_delivery", b, interceptor.HarnessVisible)
	r, err := Project(catalog(t), p, s, "receipt", seal(v), map[string][]byte{"delivery": b}, map[string]string{"native-injection": "action-1"}, 1024)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := r.Content(r.entries[0].entry.ID)
	if bytes.Contains(data, []byte("native-injection")) || bytes.Contains(data, []byte("SECRET")) || !bytes.Contains(data, []byte("action-1")) {
		t.Fatal(string(data))
	}
	r, err = Project(catalog(t), p, s, "receipt", seal(v), map[string][]byte{"delivery": b}, map[string]string{"native-injection": "action-1"}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if r.entries[0].entry.Availability != "unavailable" || !bytes.Contains(r.ManifestJSON(), []byte(`"collection_state":"partial"`)) {
		t.Fatal(string(r.ManifestJSON()))
	}
	if _, err = Project(catalog(t), p, s, "receipt", seal(v), map[string][]byte{"delivery": []byte("changed")}, nil, 1024); err == nil {
		t.Fatal("corrupt content accepted")
	}
}

func TestNativeViewRejectsCorruptionAndForeignBindings(t *testing.T) {
	p, _ := New("diagnostic", "diagnostic", kinds[:3], nil)
	s, v := view(p)
	for name, change := range map[string]func(*interceptor.ObservationView){
		"campaign": func(v *interceptor.ObservationView) { v.CampaignID = "other" }, "session": func(v *interceptor.ObservationView) { v.SessionID = "other" }, "turn": func(v *interceptor.ObservationView) { v.TurnID = "other" }, "profile": func(v *interceptor.ObservationView) { v.FeedbackProfile = "black-box" }, "revision": func(v *interceptor.ObservationView) { v.SessionRevision++ }, "kind": func(v *interceptor.ObservationView) { v.Categories[0].Kind = "new-kind" }, "duplicate": func(v *interceptor.ObservationView) { v.Categories[1] = v.Categories[0] },
	} {
		t.Run(name, func(t *testing.T) {
			var copy interceptor.ObservationView
			_ = json.Unmarshal(encoded(v), &copy)
			change(&copy)
			if _, err := VerifyView(seal(copy), s, p); err == nil {
				t.Fatal("invalid receipt accepted")
			}
		})
	}
	for _, raw := range [][]byte{bytes.Replace(seal(v), []byte(`"campaign_id"`), []byte(`"Campaign_ID"`), 1), bytes.Replace(seal(v), []byte(`"through_event_seq":0`), []byte(`"through_event_seq":null`), 1), bytes.Replace(seal(v), []byte(`"complete"`), []byte(`"partial"`), 1)} {
		if _, err := VerifyView(raw, s, p); err == nil {
			t.Fatal("invalid wire/hash accepted")
		}
	}
}

func TestAssemblyChunksAndTampering(t *testing.T) {
	p, _ := New("black-box", "black-box", []string{"target_output"}, nil)
	_, v := view(p)
	b := []byte("abcdef")
	entry(&v, "entry", "target_output", b, interceptor.TargetVisible)
	a, err := NewAssembly(v.ReceiptID, v.Entries[0])
	if err != nil {
		t.Fatal(err)
	}
	for offset := 0; offset < 6; offset += 3 {
		q, err := a.Next(3)
		if err != nil {
			t.Fatal(err)
		}
		c := interceptor.FeedbackChunk{ReceiptID: v.ReceiptID, Entry: v.Entries[0], Offset: int64(offset), Content: b[offset : offset+3], RawLength: 3, EOF: offset == 3}
		if err = a.Accept(q, encoded(c)); err != nil {
			t.Fatal(err)
		}
		if offset == 0 {
			if _, err = a.Bytes(); err == nil {
				t.Fatal("unverified prefix exposed")
			}
		}
	}
	actual, err := a.Bytes()
	if err != nil || !bytes.Equal(actual, b) {
		t.Fatal("assembly mismatch", err)
	}
	for name, mutate := range map[string]func(*interceptor.FeedbackChunk){"offset": func(c *interceptor.FeedbackChunk) { c.Offset = 1 }, "foreign": func(c *interceptor.FeedbackChunk) { c.ReceiptID = "other" }, "length": func(c *interceptor.FeedbackChunk) { c.RawLength = 3 }, "eof": func(c *interceptor.FeedbackChunk) { c.EOF = false }, "digest": func(c *interceptor.FeedbackChunk) { c.Content = []byte("xxxxxx") }} {
		t.Run(name, func(t *testing.T) {
			a, _ := NewAssembly(v.ReceiptID, v.Entries[0])
			q, _ := a.Next(6)
			c := interceptor.FeedbackChunk{ReceiptID: v.ReceiptID, Entry: v.Entries[0], Content: b, RawLength: 6, EOF: true}
			mutate(&c)
			if a.Accept(q, encoded(c)) == nil {
				t.Fatal("invalid chunk accepted")
			}
			if _, err := a.Bytes(); err == nil {
				t.Fatal("invalid bytes exposed")
			}
		})
	}
}

func TestZeroByteOutputRemainsAvailable(t *testing.T) {
	p, _ := New("black-box", "black-box", []string{"target_output"}, nil)
	s, v := view(p)
	entry(&v, "empty", "target_output", nil, interceptor.TargetVisible)
	r, err := Project(catalog(t), p, s, "receipt", seal(v), map[string][]byte{"empty": nil}, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	if r.entries[0].entry.Availability != "available" {
		t.Fatal(string(r.ManifestJSON()))
	}
	b, err := r.Read("campaign", "receipt", r.entries[0].entry.ID, 0, 1, true, []string{"target_output"})
	if err != nil {
		t.Fatal(err)
	}
	var read ReadResult
	_ = json.Unmarshal(b, &read)
	if !read.EOF || read.RawLength != 0 || read.Artifact.Digest != contracts.RawDigest(nil) {
		t.Fatal(string(b))
	}
}

func TestNormalizedOracleAndErrorFacts(t *testing.T) {
	for _, tc := range []struct{ kind, body string }{
		{"operation_error", `{"code":"APPLICATION_OPERATION_FAILED","failed":true,"stack":"SECRET"}`},
		{"oracle_outcome", `{"oracle_id":"private-detector","oracle_kind":"event_match","fired":true,"definition":"SECRET"}`},
	} {
		t.Run(tc.kind, func(t *testing.T) {
			b, err := normalize(tc.kind, []byte(tc.body), false, nil, "session")
			if err != nil {
				t.Fatal(err)
			}
			if bytes.Contains(b, []byte("SECRET")) || bytes.Contains(b, []byte("private-detector")) {
				t.Fatal(string(b))
			}
			if b, err = normalize(tc.kind, []byte(tc.body), true, nil, "session"); err != nil || b != nil {
				t.Fatal("truncated diagnostic treated as a fact")
			}
		})
	}
}

func TestContradictoryCategoryAndEntryIsRejected(t *testing.T) {
	p, _ := New("black-box", "black-box", []string{"target_output"}, nil)
	s, v := view(p)
	entry(&v, "output", "target_output", []byte("test"), interceptor.TargetVisible)
	for _, state := range []string{"empty", "withheld", "not_requested", "unavailable"} {
		v.Categories[0].State = state
		if _, err := VerifyView(seal(v), s, p); err == nil {
			t.Fatal("contradictory category accepted", state)
		}
	}
}
