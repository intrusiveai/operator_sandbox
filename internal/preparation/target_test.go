package preparation_test

import (
	"bytes"
	"encoding/json"
	"io/fs"
	"os"
	"strings"
	"testing"

	"github.com/intrusive-ai/operator-sandbox/contracts"
	"github.com/intrusive-ai/operator-sandbox/internal/attemptadapter"
	"github.com/intrusive-ai/operator-sandbox/internal/campaign"
	"github.com/intrusive-ai/operator-sandbox/internal/capabilities"
	"github.com/intrusive-ai/operator-sandbox/internal/interceptor"
	"github.com/intrusive-ai/operator-sandbox/internal/preparation"
	"github.com/intrusive-ai/operator-sandbox/internal/targetprofile"
	"github.com/intrusive-ai/operator-sandbox/schemas"
)

func encode(v any) []byte { b, _ := json.Marshal(v); return b }
func read(t *testing.T, name string) []byte {
	t.Helper()
	b, e := os.ReadFile(name)
	if e != nil {
		t.Fatal(e)
	}
	return b
}
func protocol(t *testing.T) *contracts.Protocol {
	t.Helper()
	p, e := contracts.LoadProtocol(schemas.Files)
	if e != nil {
		t.Fatal(e)
	}
	files := map[string][]byte{}
	entries, _ := fs.ReadDir(schemas.Files, ".")
	for _, entry := range entries {
		files[entry.Name()], e = schemas.Files.ReadFile(entry.Name())
		if e != nil {
			t.Fatal(e)
		}
	}
	profiles := map[string]string{"jcs-v1": "semantics/jcs", "manifest-paths-v1": "semantics/paths", "harness-loop-v1": "semantics/loop"}
	for _, name := range profiles {
		files[name] = []byte("test fixture only")
	}
	manifest, pin, e := p.BuildPackageManifest("0.1.0", files, profiles)
	if e != nil {
		t.Fatal(e)
	}
	p, e = p.LoadVerifiedProtocol(manifest, files, pin)
	if e != nil {
		t.Fatal(e)
	}
	return p
}
func fixture(t *testing.T) preparation.Input {
	t.Helper()
	p := protocol(t)
	native := read(t, "../interceptor/testdata/capability-delivery.json")
	authoring, err := capabilities.FromNative(p.Catalog(), native, "delivery-example")
	if err != nil {
		t.Fatal(err)
	}
	var n map[string]any
	_ = json.Unmarshal(native, &n)
	binding := interceptor.Binding{SessionID: "session-1", WorkerInstanceID: "worker-1", RunRevision: 1}
	a := interceptor.Attachment{CampaignID: "campaign-1", Binding: binding, Capabilities: native, Session: interceptor.Session{ID: binding.SessionID, CampaignID: "campaign-1", OperationAPIVersion: interceptor.OperationVersion, Revision: 5, Phase: "running", FeedbackProfile: "black-box", EnvironmentDigest: n["environment_digest"].(string), AppDigest: n["application_digest"].(string), CapabilityManifestDigest: authoring.SourceDigest()}}
	s := interceptor.Status{InstanceID: "instance-1", CampaignID: "campaign-1", Active: binding, Sessions: map[string]interceptor.Binding{binding.SessionID: binding}, Phase: "ready", StoreAvailable: true}
	profile, err := targetprofile.Parse([]byte(`{"api_version":"operator.dev/target-profile/v1alpha1","id":"test","target_id":"delivery-example","adapter":"interceptor/v1","allow_target_stop":false,"scopes":{"operation_ids":["invoke"],"routes":[],"caller_principal_ids":[],"allow_retained_injections":false},"feedback":{"ceiling":"black-box","allowed_kinds":["target_output"],"max_attempt_bytes":1048576},"operation_timeout_ms":30000}`))
	if err != nil {
		t.Fatal(err)
	}
	data := []byte(`{"query":"test"}`)
	d := interceptor.ArtifactDescriptor{Digest: contracts.RawDigest(data), SizeBytes: int64(len(data)), MediaType: "application/json", Canonicalization: "jcs-v1"}
	return preparation.Input{Protocol: p, Profile: profile, Attachment: a, Status: s, InstanceID: "instance-1", Authoring: authoring, Bundle: read(t, "../../schemas/fixtures/capability-chain/submitted-bundle.json"), Artifacts: []attemptadapter.Artifact{{CampaignID: "campaign-1", Descriptor: d, Bytes: data}}}
}
func preparedWriter(t *testing.T, target *preparation.Target) (*campaign.Writer, []byte) {
	w, in, _ := preparedLaunch(t, target)
	return w, in.EngineContext
}
func preparedLaunch(t *testing.T, target *preparation.Target, operations ...string) (*campaign.Writer, campaign.LaunchInputs, string) {
	t.Helper()
	var template map[string]any
	_ = json.Unmarshal(read(t, "../../schemas/fixtures/engine-context-example.json"), &template)
	template["operations"] = []string{"engine.attempt_execute", "engine.injection_delete", "engine.observation_read"}
	if operations != nil {
		template["operations"] = operations
	}
	template["remaining_limits"].(map[string]any)["artifact_bytes"] = 1 << 20
	template["limits"].(map[string]any)["campaign"].(map[string]any)["artifact_bytes"] = 1 << 20
	for _, limits := range []map[string]any{template["remaining_limits"].(map[string]any), template["limits"].(map[string]any)["campaign"].(map[string]any)} {
		limits["artifact_objects"] = 10
		limits["attempt_admissions"] = 10
	}
	prompt, provenance, err := contracts.ComposePrompt("default", []byte("Test campaign guidance."), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	template["prompt"] = map[string]any{"entry_id": "prompt", "provenance": provenance}
	set := template["skills"].(map[string]any)
	delete(set, "loading_digest")
	set["loading_digest"], _ = contracts.CanonicalDigest(encode(set), contracts.ControlLimit)
	context, err := target.Context(encode(template))
	if err != nil {
		t.Fatal(err)
	}
	c, _ := target.Protocol().ValidateEngineContext(context)
	release := c["release"].(map[string]any)
	digest := func(raw []byte) string {
		d, e := contracts.CanonicalDigest(raw, contracts.OrdinaryLimit)
		if e != nil {
			t.Fatal(e)
		}
		return d
	}
	setRaw := encode(set)
	tree := inputTree(t, target, context, prompt)
	pin, _ := target.Protocol().PackageIdentity()
	reg := target.Protocol().RegistryDigests()
	m := campaign.RunManifest{APIVersion: campaign.ManifestVersion, CampaignID: "campaign-1", LaunchID: "launch-1", ContainerID: strings.Repeat("a", 64), InitialRevision: 1, CreatedAt: "2026-09-25T12:00:00Z", HostPlatform: "darwin/arm64", ImagePlatform: "linux/arm64", Transport: "spool", RuntimeProfile: "operator-container/v1", ImageDigest: release["image_digest"].(string), ReleaseRecordDigest: release["release_record_digest"].(string), Contract: campaign.ContractPin{Version: pin.Version, Digest: pin.Digest, CatalogDigest: reg["catalog_digest"], OperationsDigest: reg["operations_digest"]}, EngineContextDigest: digest(context), InputTreeDigest: digest(tree), SkillSetDigest: digest(setRaw), ScenarioBundleDigest: digest(target.BundleJSON()), HostPolicyDigest: digest(target.HostPolicyJSON()), ModelProfileDigest: c["model"].(map[string]any)["profile_digest"].(string), Target: target.Binding(), RemainingLimits: encode(c["remaining_limits"]), HarnessLimits: encode(c["limits"].(map[string]any)["harness"]), Retention: campaign.Retention{Mode: "manual-purge", MaxJournalBytes: 256 << 20, MaxSegmentBytes: campaign.MaxEventBytes}}
	root := t.TempDir()
	_ = os.Chmod(root, 0700)
	w, err := campaign.Create(root, m)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { w.Close() })
	if err = w.ConfigureFreeSpace(0); err != nil {
		t.Fatal(err)
	}
	in := campaign.LaunchInputs{InputTree: tree, SkillSet: setRaw, EngineContext: context, ScenarioBundle: target.BundleJSON(), Prompt: prompt, HostPolicy: target.HostPolicyJSON()}
	in.Messages = startup(t, target, w, in)
	if err = m.ValidateLaunchInputs(target.Protocol(), in); err != nil {
		t.Fatal("startup fixture", err)
	}
	return w, in, root
}
func TestPreparationFreezesPolicyBytesAndManifestBindings(t *testing.T) {
	input := fixture(t)
	target, err := preparation.Build(input)
	if err != nil {
		t.Fatal(err)
	}
	input.Artifacts[0].Bytes[0] = 'x'
	input.Bundle[0] = 'x'
	w, context := preparedWriter(t, target)
	store, err := target.Persist(w, context)
	if err != nil {
		t.Fatal(err)
	}
	got, err := store.Inputs()
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range got.Artifacts {
		if !bytes.Equal(a.Bytes, []byte(`{"query":"test"}`)) {
			t.Fatal("caller altered retained bytes")
		}
		a.Bytes[0] = 'x'
	}
	got, err = store.Inputs()
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range got.Artifacts {
		if a.Bytes[0] != '{' {
			t.Fatal("returned bytes not detached")
		}
	}
	changed := bytes.Replace(context, []byte(`"launch-1"`), []byte(`"other-launch"`), 1)
	if _, err = target.Persist(w, changed); err == nil {
		t.Fatal("wrong context accepted")
	}
}
func TestPreparationRejectsForeignStaleAndCorruptInputs(t *testing.T) {
	for _, mode := range []string{"process", "revision", "artifact", "campaign", "policy", "unverified"} {
		t.Run(mode, func(t *testing.T) {
			in := fixture(t)
			switch mode {
			case "process":
				in.Status.InstanceID = "different"
			case "revision":
				in.Status.Active.RunRevision++
			case "artifact":
				in.Artifacts[0].Bytes[0] = 'x'
			case "campaign":
				in.Artifacts[0].CampaignID = "foreign"
			case "policy":
				in.Attachment.Session.CapabilityManifestDigest = contracts.RawDigest([]byte("changed"))
			case "unverified":
				in.Protocol, _ = contracts.LoadProtocol(schemas.Files)
			}
			if _, err := preparation.Build(in); err == nil {
				t.Fatal("invalid preparation accepted")
			}
		})
	}
}
