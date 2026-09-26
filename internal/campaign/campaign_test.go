//go:build linux || darwin

package campaign

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/intrusiveai/operator_sandbox/contracts"
	"github.com/intrusiveai/operator_sandbox/schemas"
)

func testManifest(t *testing.T) RunManifest {
	t.Helper()
	d := contracts.RawDigest([]byte("fixture"))
	m := RunManifest{APIVersion: ManifestVersion, CampaignID: "campaign-1", LaunchID: "launch-1", ContainerID: strings.Repeat("a", 64), InitialRevision: 3,
		CreatedAt: "2026-09-23T12:00:00Z", HostPlatform: "darwin/arm64", ImagePlatform: "linux/arm64", Transport: "spool", RuntimeProfile: "operator-container/v1",
		ImageDigest: d, ReleaseRecordDigest: d, Contract: ContractPin{"0.1.0", d, d, d}, EngineContextDigest: d, InputTreeDigest: d, SkillSetDigest: d,
		ScenarioBundleDigest: d, HostPolicyDigest: d, ModelProfileDigest: d, Target: TargetBinding{"interceptor/v1", "session-1", "worker-1", "diagnostic", d, d},
		RemainingLimits: json.RawMessage(`{"campaign_time_ms":1000,"attempt_admissions":100,"model_tokens":1000,"model_turns":300,"artifact_bytes":10000,"artifact_objects":100,"snapshot_admissions":100,"snapshot_bytes":10000,"observation_reads":100,"observation_bytes":10000}`),
		HarnessLimits:   json.RawMessage(`{"max_model_turns":300,"max_tool_calls":2000,"max_tool_calls_per_response":16,"max_invalid_tool_calls":50,"max_consecutive_invalid_tool_calls":5,"max_read_bytes":268435456,"max_no_progress_turns":10}`),
		Retention:       Retention{"manual-purge", 16 << 20, MaxEventBytes}}
	if err := m.Validate(); err != nil {
		t.Fatal(err)
	}
	return m
}
func privateRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.Chmod(root, 0700); err != nil {
		t.Fatal(err)
	}
	return root
}
func newWriter(t *testing.T) (string, *Writer) {
	t.Helper()
	root := privateRoot(t)
	w, err := Create(root, testManifest(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { w.Close() })
	return root, w
}
func entry(state string) Entry {
	e := Entry{RunRevision: 3, Kind: "operation", Metadata: json.RawMessage(`{"worker_instance_id":"worker-1","attempt_index":1}`)}
	if state != "" {
		e.Operation = &OperationMark{"op-1", contracts.RawDigest([]byte("operation")), state}
	}
	return e
}
func appendOK(t *testing.T, w *Writer, e Entry) {
	t.Helper()
	if _, err := w.Append(e); err != nil {
		t.Fatal(err)
	}
}
func fixtureBinding(w *Writer) DockerBinding {
	m := w.manifest
	return DockerBinding{BindingVersion, m.CampaignID, m.LaunchID, m.ContainerID, w.ManifestDigest(), "unix:///Users/operator/.docker/run/docker.sock", "daemon-1", strings.Repeat("b", 64), m.ImageDigest, m.DockerLabels()}
}

func TestManifestValidation(t *testing.T) {
	for _, platform := range []string{"linux/amd64", "linux/arm64", "darwin/amd64", "darwin/arm64"} {
		m := testManifest(t)
		m.HostPlatform = platform
		m.ImagePlatform = "linux/" + strings.Split(platform, "/")[1]
		if strings.HasPrefix(platform, "linux/") {
			m.Transport = "fifo"
		}
		raw, err := m.Bytes()
		if err != nil {
			t.Fatal(err)
		}
		parsed, err := ParseManifest(raw)
		if err != nil {
			t.Fatal(err)
		}
		again, _ := parsed.Bytes()
		if !bytes.Equal(raw, again) {
			t.Fatal("manifest changed")
		}
	}
	for name, mutate := range map[string]func(*RunManifest){
		"path":              func(m *RunManifest) { m.CampaignID = "../other" },
		"wrong transport":   func(m *RunManifest) { m.Transport = "fifo" },
		"emulation":         func(m *RunManifest) { m.ImagePlatform = "linux/amd64" },
		"invalid pin":       func(m *RunManifest) { m.InputTreeDigest = "latest" },
		"native profile":    func(m *RunManifest) { m.Target.NativeFeedbackProfile = "custom" },
		"negative revision": func(m *RunManifest) { m.InitialRevision = -1 },
		"unsafe budget":     func(m *RunManifest) { m.Retention.MaxJournalBytes = 1 << 54 },
		"retention":         func(m *RunManifest) { m.Retention.Mode = "automatic" },
		"required limits":   func(m *RunManifest) { m.RemainingLimits = json.RawMessage(`{}`) },
		"fractional limit": func(m *RunManifest) {
			m.HarnessLimits = bytes.Replace(m.HarnessLimits, []byte(`300`), []byte(`1.00000000000000001`), 1)
		},
	} {
		t.Run(name, func(t *testing.T) {
			m := testManifest(t)
			mutate(&m)
			if _, err := m.Bytes(); err == nil {
				t.Fatal("accepted invalid manifest")
			}
		})
	}
	m := testManifest(t)
	m.InitialRevision = 0
	raw, _ := m.Bytes()
	for _, bad := range [][]byte{
		bytes.Replace(raw, []byte(`"initial_revision":0,`), nil, 1),
		bytes.Replace(raw, []byte(`"initial_revision":0`), []byte(`"initial_revision":null`), 1),
		bytes.Replace(raw, []byte(`"initial_revision":0`), []byte(`"initial_revision":0.00000000000000001`), 1),
		append([]byte(`{"extra":0,`), raw[1:]...),
		append([]byte(`{"initial_revision":1,`), raw[1:]...),
	} {
		if _, err := ParseManifest(bad); err == nil {
			t.Fatal("accepted missing/null/unknown/duplicate field")
		}
	}
}

func TestIndependentDockerIdentity(t *testing.T) {
	root, w := newWriter(t)
	b := fixtureBinding(w)
	if _, err := ReadDockerBinding(root, "campaign-1"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing binding: %v", err)
	}
	if err := w.SaveDockerBinding(b); err != nil {
		t.Fatal(err)
	}
	if err := w.SaveDockerBinding(b); err != nil {
		t.Fatal(err)
	}
	w.mu.Lock()
	done := make(chan error, 1)
	go func() {
		got, err := ReadDockerBinding(root, "campaign-1")
		if err == nil && (got.DockerContainerID != b.DockerContainerID || got.Endpoint != b.Endpoint) {
			err = ErrInvalid
		}
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Error(err)
		}
	case <-time.After(2 * time.Second):
		t.Error("identity read waited on worker")
	}
	w.mu.Unlock()
	for _, endpoint := range []string{"tcp://localhost:2375", "ssh://host", "unix:///a/../b", "unix:///socket?context=other", "unix://host/socket", "unix:///a%20b"} {
		changed := b
		changed.Endpoint = endpoint
		if err := w.SaveDockerBinding(changed); err == nil {
			t.Fatal("accepted endpoint", endpoint)
		}
	}
	changed := b
	changed.DockerContainerID = strings.Repeat("c", 64)
	if err := w.SaveDockerBinding(changed); err == nil {
		t.Fatal("replaced immutable binding")
	}
	changed = b
	changed.DockerContainerID = "short"
	if err := w.SaveDockerBinding(changed); err == nil {
		t.Fatal("accepted short ID")
	}
	changed = b
	changed.Labels = map[string]string{}
	if err := w.SaveDockerBinding(changed); err == nil {
		t.Fatal("accepted missing labels")
	}
	if _, err := Inspect(root, "campaign-1", nil); !errors.Is(err, ErrActive) {
		t.Fatal("active inspection", err)
	}
	if _, err := Create(root, testManifest(t)); !errors.Is(err, os.ErrExist) {
		t.Fatal("reused campaign", err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadDockerBinding(root, "campaign-1"); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Append(entry("")); !errors.Is(err, ErrClosed) {
		t.Fatal(err)
	}
}

func TestJournalAcrossRestoreAndRotation(t *testing.T) {
	root, w := newWriter(t)
	e := entry(IntentCommitted)
	e.Content = []Content{{"guest-request", "application/octet-stream", []byte{0, 255, 10, 27}}}
	appendOK(t, w, e)
	appendOK(t, w, entry(Dispatched))
	for i := 0; i < 10; i++ {
		e = entry("")
		e.Kind = "model-audit"
		e.Metadata = json.RawMessage(`{"text":"` + strings.Repeat("x", 40000) + `"}`)
		appendOK(t, w, e)
	}
	e = entry(ResultCommitted)
	e.RunRevision = 4
	e.Content = []Content{{"guest-response", "application/json", []byte(`{"ok":true}`)}}
	appendOK(t, w, e)
	e = entry("")
	e.RunRevision = 4
	e.Kind = "target-revision-changed"
	appendOK(t, w, e)
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	var events []Event
	report, err := Inspect(root, "campaign-1", func(e Event) error { events = append(events, e); return nil })
	if err != nil || !report.JournalIntact || report.VerifiedEvents != 14 || len(report.Operations) != 1 || report.Operations[0].Outcome != ResultCommitted {
		t.Fatalf("report=%+v err=%v", report, err)
	}
	if events[0].Sequence != 1 || events[13].Sequence != 14 || events[13].RunRevision != 4 || report.Manifest.InitialRevision != 3 {
		t.Fatal("restore rewrote launch or numbering")
	}
	got, err := os.ReadFile(filepath.Join(root, "campaigns/campaign-1", events[0].Content[0].Path))
	if err != nil || !bytes.Equal(got, []byte{0, 255, 10, 27}) {
		t.Fatal("raw bytes changed", err)
	}
	if _, err := os.Stat(filepath.Join(root, "campaigns/campaign-1/journals/0000000000000003/events-000002.jsonl")); err != nil {
		t.Fatal("rotation missing", err)
	}
	if report.VerifiedBytes <= 400000 {
		t.Fatal("missing accounting")
	}
	filepath.WalkDir(filepath.Join(root, "campaigns/campaign-1"), func(p string, d os.DirEntry, err error) error {
		if err != nil {
			t.Error(err)
			return nil
		}
		s, e := d.Info()
		if e != nil {
			t.Error(e)
		} else if s.Mode().Perm()&0077 != 0 {
			t.Error("public evidence", p)
		}
		return nil
	})
}

func TestOperationStateAndUnknownRecovery(t *testing.T) {
	for _, last := range []string{IntentCommitted, Dispatched, Unknown, ResultCommitted} {
		t.Run(last, func(t *testing.T) {
			root, w := newWriter(t)
			appendOK(t, w, entry(IntentCommitted))
			if _, err := w.Append(entry(IntentCommitted)); err == nil {
				t.Fatal("accepted duplicate intent")
			}
			bad := entry(Dispatched)
			bad.Operation.IdentityDigest = contracts.RawDigest([]byte("mutated"))
			if _, err := w.Append(bad); err == nil {
				t.Fatal("accepted changed command")
			}
			if last != IntentCommitted {
				appendOK(t, w, entry(Dispatched))
			}
			if last == Unknown || last == ResultCommitted {
				appendOK(t, w, entry(last))
				if _, err := w.Append(entry(last)); err == nil {
					t.Fatal("accepted repeated completion")
				}
			}
			w.Close()
			r, err := Inspect(root, "campaign-1", nil)
			if err != nil {
				t.Fatal(err)
			}
			want := Unknown
			if last == ResultCommitted {
				want = ResultCommitted
			}
			if len(r.Operations) != 1 || r.Operations[0].Outcome != want || r.Operations[0].LastRecordedState != last {
				t.Fatalf("%+v", r.Operations)
			}
		})
	}
}

func TestJournalCorruption(t *testing.T) {
	for name, corrupt := range map[string]func(string){
		"truncated tail": func(dir string) {
			p := filepath.Join(dir, "journals/0000000000000003/events-000001.jsonl")
			b, _ := os.ReadFile(p)
			os.WriteFile(p, b[:len(b)-20], 0600)
		},
		"missing complete final line": func(dir string) {
			p := filepath.Join(dir, "journals/0000000000000003/events-000001.jsonl")
			b, _ := os.ReadFile(p)
			n := bytes.IndexByte(b, '\n')
			os.WriteFile(p, b[:n+1], 0600)
		},
		"tampered event": func(dir string) {
			p := filepath.Join(dir, "journals/0000000000000003/events-000001.jsonl")
			b, _ := os.ReadFile(p)
			b = bytes.Replace(b, []byte(`"worker-1"`), []byte(`"worker-2"`), 1)
			os.WriteFile(p, b, 0600)
		},
		"corrupt content": func(dir string) { os.WriteFile(filepath.Join(dir, contentPath(3, 1, 0)), []byte("different"), 0600) },
		"missing content": func(dir string) { os.Remove(filepath.Join(dir, contentPath(3, 1, 0))) },
		"extra content": func(dir string) {
			os.WriteFile(filepath.Join(dir, "journals/0000000000000003/content/orphan.bin"), []byte("orphan"), 0600)
		},
		"symlink content": func(dir string) {
			p := filepath.Join(dir, contentPath(3, 1, 0))
			os.Remove(p)
			os.Symlink("../../../campaign.json", p)
		},
		"pending head": func(dir string) {
			os.WriteFile(filepath.Join(dir, "journal-head.json.pending"), []byte("partial"), 0600)
		},
		"missing head": func(dir string) { os.Remove(filepath.Join(dir, "journal-head.json")) },
		"bad manifest": func(dir string) {
			p := filepath.Join(dir, "launch/run-manifest.json")
			b, _ := os.ReadFile(p)
			b = bytes.Replace(b, []byte(`"session-1"`), []byte(`"session-2"`), 1)
			os.WriteFile(p, b, 0600)
		},
	} {
		t.Run(name, func(t *testing.T) {
			root, w := newWriter(t)
			e := entry(IntentCommitted)
			e.Content = []Content{{"request", "text/plain", []byte("request")}}
			appendOK(t, w, e)
			appendOK(t, w, entry(Dispatched))
			w.Close()
			corrupt(filepath.Join(root, "campaigns/campaign-1"))
			r, err := Inspect(root, "campaign-1", nil)
			if err == nil || r.JournalIntact {
				t.Fatal("accepted damaged evidence")
			}
		})
	}
}

func TestPersistenceFailuresFenceWriter(t *testing.T) {
	for _, stage := range []string{"content-sync", "event-sync", "head-sync", "head-directory-sync"} {
		t.Run(stage, func(t *testing.T) {
			root, w := newWriter(t)
			if err := w.SaveDockerBinding(fixtureBinding(w)); err != nil {
				t.Fatal(err)
			}
			appendOK(t, w, entry(IntentCommitted))
			hooks := diskHooks()
			w.hooks.sync = func(f *os.File) error {
				name := filepath.Base(f.Name())
				if (stage == "content-sync" && strings.HasSuffix(name, ".bin.pending")) || (stage == "event-sync" && strings.HasSuffix(name, ".jsonl")) || (stage == "head-sync" && name == "journal-head.json.pending") {
					return syscall.ENOSPC
				}
				return hooks.sync(f)
			}
			w.hooks.syncDir = func(r *os.Root, name string) error {
				if stage == "head-directory-sync" && name == "." {
					return syscall.EIO
				}
				return hooks.syncDir(r, name)
			}
			e := entry(Dispatched)
			e.Content = []Content{{"translated-request", "text/plain", []byte("request")}}
			if _, err := w.Append(e); !errors.Is(err, ErrStorage) {
				t.Fatal("failure not latched", err)
			}
			w.hooks = hooks
			if _, err := w.Append(entry(ResultCommitted)); !errors.Is(err, ErrStorage) {
				t.Fatal("writer continued", err)
			}
			if _, err := ReadDockerBinding(root, "campaign-1"); err != nil {
				t.Fatal("journal failure blocked identity", err)
			}
			w.Close()
			r, err := Inspect(root, "campaign-1", nil)
			if stage != "head-directory-sync" && err == nil {
				t.Fatal("accepted incomplete publication")
			}
			if len(r.Operations) != 1 || r.Operations[0].Outcome != Unknown {
				t.Fatalf("uncertain outcome lost: %+v %v", r, err)
			}
		})
	}
}

func TestQuotaAndRootIsolation(t *testing.T) {
	root := privateRoot(t)
	m := testManifest(t)
	m.Retention.MaxJournalBytes = MaxEventBytes
	w, err := Create(root, m)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	e := entry(IntentCommitted)
	e.Content = []Content{{"request", "application/octet-stream", make([]byte, MaxEventBytes)}}
	if _, err := w.Append(e); !errors.Is(err, ErrQuota) {
		t.Fatal(err)
	}
	if _, err := w.Append(entry("")); !errors.Is(err, ErrQuota) {
		t.Fatal("quota did not fence writer", err)
	}
	w.Close()
	r, err := Inspect(root, "campaign-1", nil)
	if err != nil || r.VerifiedEvents != 0 {
		t.Fatal("quota wrote partial event", r, err)
	}
	m.CampaignID = "campaign-2"
	other, err := Create(root, m)
	if err != nil {
		t.Fatal(err)
	}
	other.Close()
	if _, err := Create(root, testManifest(t)); !errors.Is(err, os.ErrExist) {
		t.Fatal("reused closed campaign", err)
	}
	public := t.TempDir()
	os.Chmod(public, 0755)
	if _, err := Create(public, m); err == nil {
		t.Fatal("accepted public state root")
	}
	if _, err := ReadDockerBinding(root, "../campaign-2"); err == nil {
		t.Fatal("accepted traversal")
	}
}

func TestCrashReleasesLockWithoutResuming(t *testing.T) {
	root := privateRoot(t)
	cmd := exec.Command(os.Args[0], "-test.run=^TestCrashHelper$")
	cmd.Env = append(os.Environ(), "OPERATOR_TEST_CRASH_ROOT="+root)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	r, err := Inspect(root, "campaign-1", nil)
	if err != nil || !r.JournalIntact || r.VerifiedEvents != 2 || len(r.Operations) != 1 || r.Operations[0].Outcome != Unknown {
		t.Fatalf("%+v %v", r, err)
	}
	if _, err := Create(root, testManifest(t)); !errors.Is(err, os.ErrExist) {
		t.Fatal("crash allowed relaunch", err)
	}
}
func TestCrashHelper(t *testing.T) {
	root := os.Getenv("OPERATOR_TEST_CRASH_ROOT")
	if root == "" {
		return
	}
	w, err := Create(root, testManifest(t))
	if err != nil {
		t.Fatal(err)
	}
	appendOK(t, w, entry(IntentCommitted))
	appendOK(t, w, entry(Dispatched))
	os.Exit(0) // Deliberately no Close: model abrupt host-process loss.
}

func TestSharedLaunchPins(t *testing.T) {
	raw, err := os.ReadFile("../../schemas/fixtures/identity-validation.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixtures []struct {
		Messages []json.RawMessage `json:"messages"`
		Tree     string            `json:"tree_base64"`
		Set      string            `json:"skill_set_base64"`
		Skills   []string          `json:"skills_base64"`
		Context  string            `json:"context_base64"`
		Bundle   string            `json:"bundle_base64"`
		Prompt   string            `json:"prompt_base64"`
	}
	if err := json.Unmarshal(raw, &fixtures); err != nil {
		t.Fatal(err)
	}
	f := fixtures[0]
	b64 := func(s string) []byte {
		b, e := base64.StdEncoding.DecodeString(s)
		if e != nil {
			t.Fatal(e)
		}
		return b
	}
	in := LaunchInputs{InputTree: b64(f.Tree), SkillSet: b64(f.Set), EngineContext: b64(f.Context), ScenarioBundle: b64(f.Bundle), Prompt: b64(f.Prompt), HostPolicy: []byte(`{"policy":"fixture"}`)}
	for _, raw := range f.Messages {
		in.Messages = append(in.Messages, raw)
	}
	for _, s := range f.Skills {
		in.Skills = append(in.Skills, b64(s))
	}
	p, err := contracts.LoadProtocol(schemas.Files)
	if err != nil {
		t.Fatal(err)
	}
	ctx, err := p.ValidateEngineContext(in.EngineContext)
	if err != nil {
		t.Fatal(err)
	}
	boot, _ := p.ValidateControl("host", in.Messages[0])
	body := boot["body"].(map[string]any)
	m := testManifest(t)
	m.HostPlatform = body["host_platform"].(string)
	m.ImagePlatform = "linux/" + strings.Split(m.HostPlatform, "/")[1]
	m.Transport = body["transport"].(string)
	digest := func(raw []byte) string {
		d, e := contracts.CanonicalDigest(raw, contracts.InputTreeManifestLimit)
		if e != nil {
			t.Fatal(e)
		}
		return d
	}
	m.EngineContextDigest = digest(in.EngineContext)
	m.InputTreeDigest = digest(in.InputTree)
	m.SkillSetDigest = digest(in.SkillSet)
	m.ScenarioBundleDigest = digest(in.ScenarioBundle)
	m.HostPolicyDigest = digest(in.HostPolicy)
	c := ctx["contract"].(map[string]any)
	m.Contract = ContractPin{c["version"].(string), c["digest"].(string), c["catalog_digest"].(string), c["operations_digest"].(string)}
	release := ctx["release"].(map[string]any)
	m.ImageDigest = release["image_digest"].(string)
	m.ReleaseRecordDigest = release["release_record_digest"].(string)
	target := ctx["target"].(map[string]any)
	m.Target.CapabilityProjectionDigest = target["capability_projection_digest"].(string)
	m.Target.CapabilitySourceDigest = target["source"].(map[string]any)["capability_source_digest"].(string)
	m.ModelProfileDigest = ctx["model"].(map[string]any)["profile_digest"].(string)
	m.RemainingLimits, _ = json.Marshal(ctx["remaining_limits"])
	m.HarnessLimits, _ = json.Marshal(ctx["limits"].(map[string]any)["harness"])
	setManifestDigest := func(m RunManifest) {
		raw, err := m.Bytes()
		if err != nil {
			t.Fatal(err)
		}
		body["run_manifest_digest"] = contracts.RawDigest(raw)
		in.Messages[0], _ = json.Marshal(boot)
		ready, _ := p.ValidateControl("guest", in.Messages[1])
		ready["body"].(map[string]any)["run_manifest_digest"] = contracts.RawDigest(raw)
		in.Messages[1], _ = json.Marshal(ready)
	}
	setManifestDigest(m)
	if err := m.ValidateLaunchInputs(p, in); err != nil {
		t.Fatal("valid launch", err)
	}
	for name, alter := range map[string]func(*RunManifest){
		"manifest digest": func(m *RunManifest) { m.Target.SessionID = "other" },
		"context":         func(m *RunManifest) { m.EngineContextDigest = contracts.RawDigest(nil) },
		"tree":            func(m *RunManifest) { m.InputTreeDigest = contracts.RawDigest(nil) },
		"bundle":          func(m *RunManifest) { m.ScenarioBundleDigest = contracts.RawDigest(nil) },
		"source":          func(m *RunManifest) { m.Target.CapabilitySourceDigest = contracts.RawDigest(nil) },
		"limits": func(m *RunManifest) {
			var limits map[string]any
			json.Unmarshal(m.RemainingLimits, &limits)
			limits["model_turns"] = limits["model_turns"].(float64) + 1
			m.RemainingLimits, _ = json.Marshal(limits)
		},
	} {
		t.Run(name, func(t *testing.T) {
			changed := m
			alter(&changed)
			if name != "manifest digest" {
				setManifestDigest(changed)
			}
			if err := changed.ValidateLaunchInputs(p, in); err == nil {
				t.Fatal("accepted pin mismatch", fmt.Sprint(name))
			}
		})
	}
}
