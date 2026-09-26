package capabilities

import (
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/intrusiveai/operator_sandbox/contracts"
	"github.com/intrusiveai/operator_sandbox/internal/interceptor"
)

func attachment(e *Export, profile string) (interceptor.Attachment, interceptor.Status) {
	b := interceptor.Binding{SessionID: "session-1", WorkerInstanceID: "worker-1", RunRevision: 1}
	a := interceptor.Attachment{CampaignID: "campaign-1", Binding: b, Capabilities: e.NativeJSON(), Session: interceptor.Session{ID: b.SessionID, CampaignID: "campaign-1", OperationAPIVersion: interceptor.OperationVersion, Phase: "running", FeedbackProfile: profile, EnvironmentDigest: e.native.EnvironmentDigest, AppDigest: e.native.ApplicationDigest, CapabilityManifestDigest: e.SourceDigest()}}
	s := interceptor.Status{InstanceID: "instance-1", CampaignID: a.CampaignID, Active: b, Phase: "ready", StoreAvailable: true, Sessions: map[string]interceptor.Binding{b.SessionID: b}}
	return a, s
}
func bind(t *testing.T, c *contracts.Catalog, e *Export, profile string) *Live {
	t.Helper()
	a, s := attachment(e, profile)
	l, err := BindLive(c, a, s, s.InstanceID, e.TargetID())
	if err != nil {
		t.Fatal(err)
	}
	return l
}

// Test-only stand-in for installed policy resolution; production must resolve
// actual routes and selectors before declaring any action family selectable.
func testPolicy(e *Export) Policy {
	p := Policy{}
	for ref := range referenceIndex(e) {
		p.AllowedRefs = append(p.AllowedRefs, ref)
		if strings.HasPrefix(ref, "action:") {
			p.SelectableActionRefs = append(p.SelectableActionRefs, ref)
		}
	}
	return p
}
func mustExport(t *testing.T, c *contracts.Catalog, raw []byte) *Export {
	t.Helper()
	e, err := FromNative(c, raw, "delivery-example")
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func TestBundleCompatibilityCases(t *testing.T) {
	c := catalogForTest(t)
	authoring := mustExport(t, c, fixture(t, "interceptor-export.json"))
	type testcase struct {
		name   string
		bundle func(map[string]any)
		native func(map[string]any)
		policy func(*Policy)
		bad    bool
		gaps   []string
	}
	cases := []testcase{
		{name: "native invocation unavailable", native: func(n map[string]any) {
			n["cross_vm_operations"] = remove(stringsOf(n["cross_vm_operations"]), "application.invoke")
		}, bad: true},
		{name: "evidence denied", policy: func(p *Policy) { p.AllowedRefs = remove(p.AllowedRefs, "evidence:target_output") }, gaps: []string{"evidence:target_output"}},
		{name: "golden"},
		{name: "objectives only", bundle: func(b map[string]any) {
			b["scenarios"] = []any{}
			delete(b["target_requirements"].(map[string]any), "required_capability_refs")
		}},
		{name: "unknown required", bundle: func(b map[string]any) {
			b["target_requirements"].(map[string]any)["required_capability_refs"] = []string{"operation:missing"}
		}, bad: true},
		{name: "optional unknown", bundle: func(b map[string]any) {
			records(b["scenarios"])[0]["required_capability_refs"] = []string{"service:missing"}
		}, gaps: []string{"service:missing"}},
		{name: "required scenario optional guidance", bundle: func(b map[string]any) {
			s := records(b["scenarios"])[0]
			s["required"] = true
			s["guidance"].(map[string]any)["action_refs"] = []string{"action:injection:unknown"}
		}, gaps: []string{"action:injection:unknown"}},
		{name: "optional oracle", bundle: func(b map[string]any) {
			b["evidence"].(map[string]any)["requested_classes"] = []string{"evidence:oracle_outcome"}
		}, gaps: []string{"evidence:oracle_outcome"}},
		{name: "required oracle", bundle: func(b map[string]any) {
			b["target_requirements"].(map[string]any)["required_capability_refs"] = []string{"evidence:oracle_outcome"}
		}, bad: true},
		{name: "source mismatch", bundle: func(b map[string]any) {
			b["target_requirements"].(map[string]any)["capability_source_digest"] = "sha256:" + strings.Repeat("0", 64)
		}, bad: true},
		{name: "target mismatch", bundle: func(b map[string]any) { b["target_requirements"].(map[string]any)["target_id"] = "other" }, bad: true},
		{name: "exact pin", bundle: func(b map[string]any) {
			b["target_requirements"].(map[string]any)["capability_projection_digest"] = authoring.ProjectionDigest()
		}},
		{name: "unrelated addition", native: func(n map[string]any) { n["snapshot_capable"] = true }},
		{name: "exact pin rejects addition", bundle: func(b map[string]any) {
			b["target_requirements"].(map[string]any)["capability_projection_digest"] = authoring.ProjectionDigest()
		}, native: func(n map[string]any) { n["snapshot_capable"] = true }, bad: true},
		{name: "exact pin permits provenance change", bundle: func(b map[string]any) {
			b["target_requirements"].(map[string]any)["capability_projection_digest"] = authoring.ProjectionDigest()
		}, native: func(n map[string]any) {
			n["interceptor_release"] = "new"
			n["environment_digest"] = "sha256:" + strings.Repeat("2", 64)
		}},
		{name: "required operation removed", native: func(n map[string]any) { n["operations"] = nil }, bad: true},
		{name: "optional service removed", native: func(n map[string]any) { n["services"] = nil }, gaps: []string{"service:tickets"}},
		{name: "required service removed", bundle: func(b map[string]any) { records(b["scenarios"])[0]["required"] = true }, native: func(n map[string]any) { n["services"] = nil }, bad: true},
		{name: "missing delivery", native: func(n map[string]any) {
			op := records(n["operations"])[0]
			delete(op, "delivery")
			op["delivery_status"] = "missing-input-contract"
		}, bad: true},
		{name: "changed valid schema", native: func(n map[string]any) {
			records(n["operations"])[0]["delivery"].(map[string]any)["input"].(map[string]any)["schema"].(map[string]any)["required"] = []any{}
		}},
		{name: "changed description", native: func(n map[string]any) {
			records(n["operations"])[0]["delivery"].(map[string]any)["description"] = "new documentation"
		}},
		{name: "required only in live", bundle: func(b map[string]any) {
			b["target_requirements"].(map[string]any)["required_capability_refs"] = []string{"feature:snapshots"}
		}, native: func(n map[string]any) { n["snapshot_capable"] = true }, bad: true},
		{name: "denied operation", policy: func(p *Policy) { p.AllowedRefs = remove(p.AllowedRefs, "operation:invoke") }, bad: true},
		{name: "action without route", policy: func(p *Policy) { p.SelectableActionRefs = nil }, gaps: []string{"action:injection:mcp_tool_result"}},
		{name: "required action without route", bundle: func(b map[string]any) { records(b["scenarios"])[0]["required"] = true }, policy: func(p *Policy) { p.SelectableActionRefs = nil }, bad: true},
		{name: "unknown policy ref", policy: func(p *Policy) { p.AllowedRefs = append(p.AllowedRefs, "operation:typo") }, bad: true},
		{name: "required objective dependency", bundle: func(b map[string]any) {
			records(b["objectives"])[0]["required_capability_refs"] = []string{"operation:missing"}
		}, bad: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b := object(t, fixture(t, "submitted-bundle.json"))
			if tc.bundle != nil {
				tc.bundle(b)
			}
			raw := marshal(t, b)
			bundle, err := ParseBundle(c, raw)
			if err != nil {
				t.Fatal(err)
			}
			liveExport := authoring
			if tc.native != nil {
				liveExport = mustExport(t, c, nativeVariant(t, tc.native))
			}
			live := bind(t, c, liveExport, "black-box")
			p := testPolicy(liveExport)
			if tc.policy != nil {
				tc.policy(&p)
			}
			result, err := Check(bundle, authoring, live, p)
			if tc.bad {
				if err == nil {
					t.Fatal("accepted incompatible bundle")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			refs := []string{}
			for _, g := range result.Gaps() {
				refs = append(refs, g.Ref)
			}
			if strings.Join(refs, ",") != strings.Join(tc.gaps, ",") {
				t.Fatalf("gaps=%v, want %v", refs, tc.gaps)
			}
			if string(bundle.JSON()) != string(raw) {
				t.Fatal("rewrote authored bundle")
			}
			record := object(t, result.RecordJSON())
			if tc.name == "evidence denied" && len(result.AllowedKinds()) != 0 {
				t.Fatal("denied evidence remained readable")
			}

			if record["bundle_digest"] != contracts.RawDigest(raw) || record["native_profile"] != "black-box" {
				t.Fatal("wrong provenance")
			}
			if tc.name == "action without route" {
				if !reflect.DeepEqual(stringsOf(record["disabled_scenario_ids"]), []string{"scenario-marker"}) {
					t.Fatal("optional route not disabled")
				}
			}
		})
	}
}
func remove(a []string, s string) []string {
	out := []string{}
	for _, v := range a {
		if v != s {
			out = append(out, v)
		}
	}
	return out
}

func TestBundleStructuralAndSemanticRejections(t *testing.T) {
	c := catalogForTest(t)
	cases := map[string]func(map[string]any){
		"unknown execution field": func(b map[string]any) { b["exec"] = "shell" },
		"wrong action namespace": func(b map[string]any) {
			records(b["scenarios"])[0]["guidance"].(map[string]any)["action_refs"] = []string{"operation:invoke"}
		},
		"wrong evidence namespace": func(b map[string]any) {
			b["evidence"].(map[string]any)["requested_classes"] = []string{"service:tickets"}
		},
		"duplicate objective ID": func(b map[string]any) {
			v := object(t, marshal(t, records(b["objectives"])[0]))
			v["description"] = "different"
			b["objectives"] = append(b["objectives"].([]any), v)
		},
		"duplicate scenario ID": func(b map[string]any) {
			v := object(t, marshal(t, records(b["scenarios"])[0]))
			v["hypothesis"] = "different"
			b["scenarios"] = append(b["scenarios"].([]any), v)
		},
		"dangling objective":  func(b map[string]any) { records(b["scenarios"])[0]["objective_refs"] = []string{"missing"} },
		"dangling artifact":   func(b map[string]any) { records(b["objectives"])[0]["artifact_refs"] = []string{"missing"} },
		"incomplete coverage": func(b map[string]any) { b["coverage"].(map[string]any)["objective_refs"] = []string{"missing"} },
		"UTF8 bytes":          func(b map[string]any) { records(b["objectives"])[0]["description"] = strings.Repeat("界", 2000) },
		"context bytes":       func(b map[string]any) { b["context"] = strings.Repeat("界", 6000) },
		"invalid limit":       func(b map[string]any) { b["requested_limits"] = map[string]any{"attempt_admissions": 0} },
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			b := object(t, fixture(t, "submitted-bundle.json"))
			change(b)
			if _, err := ParseBundle(c, marshal(t, b)); err == nil {
				t.Fatal("accepted invalid bundle")
			}
		})
	}
	b := object(t, fixture(t, "submitted-bundle.json"))
	b["context"] = strings.Repeat("界", 5000)
	if _, err := ParseBundle(c, marshal(t, b)); err != nil {
		t.Fatal("context should allow 16KiB", err)
	}
}

func TestLiveBindingFailures(t *testing.T) {
	c := catalogForTest(t)
	e := mustExport(t, c, fixture(t, "interceptor-export.json"))
	cases := map[string]func(*interceptor.Attachment, *interceptor.Status){
		"closed":            func(a *interceptor.Attachment, s *interceptor.Status) { s.Closed = true },
		"transitioning":     func(a *interceptor.Attachment, s *interceptor.Status) { s.Phase = "transitioning" },
		"store unavailable": func(a *interceptor.Attachment, s *interceptor.Status) { s.StoreAvailable = false },
		"failure": func(a *interceptor.Attachment, s *interceptor.Status) {
			s.Failure = &interceptor.Failure{Reason: "WALL_TIME_LIMIT"}
		},
		"instance": func(a *interceptor.Attachment, s *interceptor.Status) { s.InstanceID = "different" },
		"session":  func(a *interceptor.Attachment, s *interceptor.Status) { s.Active.SessionID = "different" },
		"revision": func(a *interceptor.Attachment, s *interceptor.Status) { s.Active.RunRevision++ },
		"campaign": func(a *interceptor.Attachment, s *interceptor.Status) { s.CampaignID = "different" },
		"environment": func(a *interceptor.Attachment, s *interceptor.Status) {
			a.Session.EnvironmentDigest = "sha256:" + strings.Repeat("1", 64)
		},
		"application": func(a *interceptor.Attachment, s *interceptor.Status) {
			a.Session.AppDigest = "sha256:" + strings.Repeat("1", 64)
		},
		"capability": func(a *interceptor.Attachment, s *interceptor.Status) {
			a.Session.CapabilityManifestDigest = "sha256:" + strings.Repeat("1", 64)
		},
		"unsupported profile": func(a *interceptor.Attachment, s *interceptor.Status) { a.Session.FeedbackProfile = "diagnostic" },
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			a, s := attachment(e, "black-box")
			change(&a, &s)
			if _, err := BindLive(c, a, s, "instance-1", "delivery-example"); !errors.Is(err, ErrBinding) {
				t.Fatalf("got %v", err)
			}
		})
	}
	a, s := attachment(e, "black-box")
	s.Active.WorkerInstanceID = "other-worker"
	if _, err := BindLive(c, a, s, "instance-1", "delivery-example"); err != nil {
		t.Fatal("worker attribution became an access restriction", err)
	}
}

func TestFeedbackProfileFixturesAtAdmission(t *testing.T) {
	c := catalogForTest(t)
	raw := nativeVariant(t, func(n map[string]any) {
		n["feedback_profiles"] = []string{"black-box", "diagnostic", "oracle-assisted"}
	})
	e := mustExport(t, c, raw)
	data, err := os.ReadFile("../../schemas/fixtures/feedback-translation.json")
	if err != nil {
		t.Fatal(err)
	}
	vectors := object(t, data)
	for _, v := range records(vectors["cases"]) {
		t.Run(v["name"].(string), func(t *testing.T) {
			input, want := v["input"].(map[string]any), v["expected"].(map[string]any)

			b := object(t, fixture(t, "submitted-bundle.json"))
			b["target_requirements"].(map[string]any)["capability_source_digest"] = e.SourceDigest()
			b["feedback"].(map[string]any)["requested_profile"] = input["requested_profile"]
			bundle, err := ParseBundle(c, marshal(t, b))
			if err != nil {
				t.Fatal(err)
			}
			p := testPolicy(e)
			if v, ok := input["host_profile_ceiling"]; ok {
				p.FeedbackCeiling = v.(string)
			}
			if v, ok := input["host_allowed_kinds"]; ok {
				p.FeedbackKinds = stringsOf(v)
			}
			result, err := Check(bundle, e, bind(t, c, e, input["native_profile"].(string)), p)
			if err != nil {
				t.Fatal(err)
			}
			if result.NativeProfile() != want["native_attempt_profile"] || result.EffectiveProfile() != want["feedback"].(map[string]any)["profile"] || !reflect.DeepEqual(result.AllowedKinds(), stringsOf(want["allowed_kinds"])) {
				t.Fatalf("wrong feedback narrowing: %s, %s, %v", result.NativeProfile(), result.EffectiveProfile(), result.AllowedKinds())
			}
		})
	}
}

func TestArtifactInventoryAndImmutableResults(t *testing.T) {
	c := catalogForTest(t)
	e := mustExport(t, c, fixture(t, "interceptor-export.json"))
	b := object(t, fixture(t, "submitted-bundle.json"))
	a := map[string]any{"artifact_id": "a", "digest": contracts.RawDigest([]byte("x")), "size_bytes": json.Number("1"), "media_type": "text/plain", "purpose": "context", "visibility": "operator-engine", "required": false}
	b["artifacts"] = []any{a}
	bundle, err := ParseBundle(c, marshal(t, b))
	if err != nil {
		t.Fatal(err)
	}
	p := testPolicy(e)
	p.FeedbackKinds = []string{}
	result, err := Check(bundle, e, bind(t, c, e, "black-box"), p)
	if err != nil {
		t.Fatal(err)
	}
	frozen := string(result.RecordJSON())
	p.AllowedRefs[0] = "mutated"
	x := result.RecordJSON()
	x[0] = '!'
	g := result.Gaps()
	g[0].Reason = "mutated"
	if string(result.RecordJSON()) != frozen || result.Gaps()[0].Reason == "mutated" {
		t.Fatal("mutable policy/record")
	}
	a2 := object(t, marshal(t, a))
	a2["artifact_id"] = "b"
	a2["media_type"] = "application/json"
	b["artifacts"] = []any{a, a2}
	if _, err := ParseBundle(c, marshal(t, b)); !errors.Is(err, ErrBundle) {
		t.Fatal("conflicting same-byte claims accepted")
	}
	b["artifacts"] = []any{a}
	a["omission_reason"] = "not provided"
	records(b["objectives"])[0]["artifact_refs"] = []string{"a"}
	if _, err := ParseBundle(c, marshal(t, b)); !errors.Is(err, ErrBundle) {
		t.Fatal("required objective with omitted artifact accepted")
	}
}
