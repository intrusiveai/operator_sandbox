package campaign

import (
	"encoding/json"

	"github.com/intrusive-ai/operator-sandbox/contracts"
)

type LaunchInputs struct {
	Messages       [][]byte
	InputTree      []byte
	SkillSet       []byte
	Skills         [][]byte
	EngineContext  []byte
	ScenarioBundle []byte
	Prompt         []byte
	HostPolicy     []byte
}

// ValidateLaunchInputs links the private manifest to the existing shared launch
// transcript and exact input bytes. Use it after constructing bootstrap from the
// manifest digest. Release approval, actual file staging, the native session and
// host-policy authority still require independent host checks.
func (m RunManifest) ValidateLaunchInputs(p *contracts.Protocol, in LaunchInputs) error {
	raw, err := m.Bytes()
	if err != nil || p == nil {
		return ErrInvalid
	}
	if err := p.ValidateLaunchIdentities(in.Messages, in.InputTree, in.SkillSet, in.Skills, in.EngineContext, in.ScenarioBundle, in.Prompt); err != nil {
		return err
	}
	ctx, _ := p.ValidateEngineContext(in.EngineContext)
	boot, _ := p.ValidateControl("host", in.Messages[0])
	body := boot["body"].(map[string]any)
	revision, _ := ctx["run_revision"].(json.Number).Int64()
	if ctx["campaign_id"] != m.CampaignID || ctx["launch_id"] != m.LaunchID || revision != m.InitialRevision ||
		body["container_id"] != m.ContainerID || body["run_manifest_digest"] != contracts.RawDigest(raw) ||
		body["host_platform"] != m.HostPlatform || body["transport"] != m.Transport || body["runtime_profile"] != m.RuntimeProfile {
		return ErrInvalid
	}
	for _, pin := range []struct {
		digest  string
		content []byte
		limit   int
	}{
		{m.EngineContextDigest, in.EngineContext, contracts.EngineContextLimit},
		{m.InputTreeDigest, in.InputTree, contracts.InputTreeManifestLimit},
		{m.SkillSetDigest, in.SkillSet, contracts.ControlLimit},
		{m.ScenarioBundleDigest, in.ScenarioBundle, contracts.OrdinaryLimit},
		{m.HostPolicyDigest, in.HostPolicy, ManifestLimit},
	} {
		if d, err := contracts.CanonicalDigest(pin.content, pin.limit); err != nil || d != pin.digest {
			return ErrInvalid
		}
	}
	if v, err := contracts.Decode(in.HostPolicy, ManifestLimit); err != nil {
		return ErrInvalid
	} else if _, ok := v.(map[string]any); !ok {
		return ErrInvalid
	}
	contract := ctx["contract"].(map[string]any)
	release := ctx["release"].(map[string]any)
	target := ctx["target"].(map[string]any)
	source := target["source"].(map[string]any)
	if contract["version"] != m.Contract.Version || contract["digest"] != m.Contract.Digest || contract["catalog_digest"] != m.Contract.CatalogDigest || contract["operations_digest"] != m.Contract.OperationsDigest ||
		release["image_digest"] != m.ImageDigest || release["release_record_digest"] != m.ReleaseRecordDigest || source["adapter"] != m.Target.Adapter ||
		source["capability_source_digest"] != m.Target.CapabilitySourceDigest || target["capability_projection_digest"] != m.Target.CapabilityProjectionDigest ||
		ctx["model"].(map[string]any)["profile_digest"] != m.ModelProfileDigest {
		return ErrInvalid
	}
	for _, pair := range []struct {
		value any
		raw   []byte
	}{
		{ctx["remaining_limits"], m.RemainingLimits},
		{ctx["limits"].(map[string]any)["harness"], m.HarnessLimits},
	} {
		actual, err := encode(pair.value, ManifestLimit)
		if err != nil {
			return ErrInvalid
		}
		digest, err := contracts.CanonicalDigest(pair.raw, ManifestLimit)
		if err != nil || contracts.RawDigest(actual) != digest {
			return ErrInvalid
		}
	}
	return nil
}
