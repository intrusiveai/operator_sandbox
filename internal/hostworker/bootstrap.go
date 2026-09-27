//go:build linux || darwin

package hostworker

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/intrusiveai/operator_sandbox/contracts"
	"github.com/intrusiveai/operator_sandbox/internal/campaign"
	"github.com/intrusiveai/operator_sandbox/internal/campaignservice"
	"github.com/intrusiveai/operator_sandbox/internal/transport"
)

// StartupMessages builds host messages from frozen launch content. It does not
// manufacture guest responses. Inputs.Messages is deliberately ignored.
func StartupMessages(p *contracts.Protocol, m campaign.RunManifest, in campaign.LaunchInputs) ([][]byte, error) {
	raw, err := m.Bytes()
	if err != nil || p == nil {
		return nil, ErrWorker
	}
	c, err := p.ValidateEngineContext(in.EngineContext)
	if err != nil {
		return nil, err
	}
	if err = p.ValidateManifestSet(in.InputTree, in.SkillSet, in.Skills); err != nil {
		return nil, err
	}
	binding := map[string]any{"campaign_id": m.CampaignID, "launch_id": m.LaunchID, "run_revision": m.InitialRevision, "input_tree_digest": contracts.RawDigest(in.InputTree), "engine_context_digest": contracts.RawDigest(in.EngineContext), "prompt_digest": contracts.RawDigest(in.Prompt), "skill_set_digest": contracts.RawDigest(in.SkillSet), "image_digest": m.ImageDigest, "release_record_digest": m.ReleaseRecordDigest, "contract_package_digest": m.Contract.Digest, "contract_package_version": m.Contract.Version}
	ref := func(name, schema string, raw []byte) map[string]any {
		d, _ := contracts.CanonicalDigest(raw, contracts.InputTreeManifestLimit)
		return map[string]any{"path_id": name, "schema_id": schema, "size_bytes": len(raw), "digest": contracts.RawDigest(raw), "object_digest": d}
	}
	bodies := []map[string]any{
		{"container_id": m.ContainerID, "contract": c["contract"], "release": c["release"], "runtime_profile": m.RuntimeProfile, "host_platform": m.HostPlatform, "transport": m.Transport, "run_manifest_digest": contracts.RawDigest(raw), "timeout_ms": 60000},
		{"input_tree": ref("input-tree", contracts.InputTreeManifestSchema, in.InputTree), "skill_set": ref("skill-set", contracts.SkillSetManifestSchema, in.SkillSet), "binding": binding, "engine_context_object_digest": m.EngineContextDigest, "timeout_ms": 60000},
		{"binding": binding, "operations": c["operations"], "limits": c["limits"], "remaining_limits": c["remaining_limits"]},
	}
	var out [][]byte
	for i, kind := range []string{"bootstrap", "initialize", "admission_open"} {
		raw, err := json.Marshal(map[string]any{"api_version": "operator.dev/engine-pipe/v1alpha1", "kind": kind, "seq": i, "campaign_id": m.CampaignID, "launch_id": m.LaunchID, "run_revision": m.InitialRevision, "body": bodies[i]})
		if err != nil {
			return nil, err
		}
		if _, err = p.ValidateControl("host", raw); err != nil {
			return nil, err
		}
		out = append(out, raw)
	}
	return out, nil
}

func cloneInputs(in campaign.LaunchInputs) campaign.LaunchInputs {
	in.Messages = nil
	in.InputTree = bytes.Clone(in.InputTree)
	in.SkillSet = bytes.Clone(in.SkillSet)
	skills := make([][]byte, len(in.Skills))
	for i, raw := range in.Skills {
		skills[i] = bytes.Clone(raw)
	}
	in.Skills = skills
	in.EngineContext = bytes.Clone(in.EngineContext)
	in.ScenarioBundle = bytes.Clone(in.ScenarioBundle)
	in.Prompt = bytes.Clone(in.Prompt)
	in.HostPolicy = bytes.Clone(in.HostPolicy)
	return in
}

// Bootstrap exchanges actual messages with the running guest. The caller runs
// the transport pump and independent termination concurrently. Admission is
// journaled by Service before admission_open is published.
func Bootstrap(ctx context.Context, p *contracts.Protocol, w *campaign.Writer, channel *transport.Session, service *campaignservice.Service, in campaign.LaunchInputs, check func(context.Context) error) (resultErr error) {
	if p == nil || w == nil || channel == nil || service == nil || check == nil {
		return ErrWorker
	}
	defer func() {
		if resultErr != nil {
			w.Fence().Stop(resultErr)
		}
	}()
	in = cloneInputs(in)
	messages, err := StartupMessages(p, w.Manifest(), in)
	if err != nil {
		return err
	}
	journal := func(kind string, raw []byte) error {
		_, err := w.Append(campaign.Entry{RunRevision: w.Manifest().InitialRevision, Kind: kind, Metadata: json.RawMessage(`{}`), Content: []campaign.Content{{Role: "control", MediaType: "application/json", Bytes: raw}}})
		return err
	}
	wait := func() error {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-w.Fence().Done():
			return w.Fence().Err()
		case <-time.After(time.Millisecond):
			return nil
		}
	}
	send := func(raw []byte, seq int64) error {
		if err := check(ctx); err != nil {
			return err
		}
		if err := journal("launch.control-intent", raw); err != nil {
			return err
		}
		for {
			err := channel.Enqueue("control-in", raw)
			if err == nil {
				break
			}
			if !errors.Is(err, transport.ErrQueueFull) && !errors.Is(err, transport.ErrNotReady) {
				return err
			}
			if err = wait(); err != nil {
				return err
			}
		}
		for !channel.Published("control-in", seq) {
			if err := wait(); err != nil {
				return err
			}
		}
		return journal("launch.control-published", raw)
	}
	receive := func() ([]byte, error) {
		for {
			if raw, ok := channel.Receive("control-out"); ok {
				return raw, journal("launch.control-received", raw)
			}
			if err := wait(); err != nil {
				return nil, err
			}
		}
	}
	if err = send(messages[0], 0); err != nil {
		return err
	}
	confinement, err := receive()
	if err != nil {
		return err
	}
	boot, _ := p.ValidateControl("host", messages[0])
	ready, err := p.ValidateControl("guest", confinement)
	if err != nil || ready["kind"] != "confinement_ready" {
		return ErrWorker
	}
	// Compare canonical wire values, not decimal spellings of valid integers.
	boot["kind"] = "confinement_ready"
	delete(boot["body"].(map[string]any), "timeout_ms")
	expected, _ := json.Marshal(boot)
	actual, _ := json.Marshal(ready)
	expected, err = contracts.Canonicalize(expected, contracts.ControlLimit)
	if err != nil {
		return err
	}
	actual, err = contracts.Canonicalize(actual, contracts.ControlLimit)
	if err != nil || !bytes.Equal(expected, actual) {
		return ErrWorker
	}
	if err = check(ctx); err != nil {
		return err
	}
	if err = channel.BeginInitialization(); err != nil {
		return err
	}
	if err = send(messages[1], 1); err != nil {
		return err
	}
	initialized, err := receive()
	if err != nil {
		return err
	}
	in.Messages = [][]byte{messages[0], confinement, messages[1], initialized, messages[2]}
	if err = w.Manifest().ValidateLaunchInputs(p, in); err != nil {
		return err
	}
	if err = check(ctx); err != nil {
		return err
	}
	if err = service.Admit(ctx, in); err != nil {
		return err
	}
	if err = send(messages[2], 2); err != nil {
		return err
	}
	return channel.OpenAdmission()
}
