//go:build linux || darwin

package preparation

import (
	"bytes"
	"encoding/json"
	"sort"
	"strings"
	"time"

	"github.com/intrusiveai/operator_sandbox/contracts"
	"github.com/intrusiveai/operator_sandbox/internal/campaign"
	"github.com/intrusiveai/operator_sandbox/internal/campaignlimits"
	"github.com/intrusiveai/operator_sandbox/internal/imagerelease"
	"github.com/intrusiveai/operator_sandbox/internal/modelprovider"
	"github.com/intrusiveai/operator_sandbox/internal/skills"
)

type LaunchConfig struct {
	LaunchID, ContainerID string // Fresh host-generated identities; never bundle-selected.
	CreatedAt             time.Time
	Image                 imagerelease.Prepared
	Embedded              *imagerelease.Embedded
	Model                 *modelprovider.Profile
	Skills                *skills.Selection
	Limits                campaignlimits.Host
	Retention             campaign.Retention
	PromptMode            string
	Replacement           []byte
	Appends               [][]byte
}

// Launch is freshly prepared in memory. It does not authorize execution, persist
// a journal, contact providers or claim that a live guest completed bootstrap.
type Launch struct {
	Manifest                campaign.RunManifest
	Inputs                  campaign.LaunchInputs
	Contents                map[string][]byte
	ModelTools, ModelPolicy []byte
}

func canonicalJSON(value any, limit int) ([]byte, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	return contracts.Canonicalize(raw, limit)
}
func inputDescriptor(id, path, role, media, schema string, raw []byte) map[string]any {
	entry := map[string]any{"entry_id": id, "root_kind": "input", "path": path, "role": role, "media_type": media, "size_bytes": len(raw), "digest": contracts.RawDigest(raw)}
	if schema != "" {
		entry["schema_id"] = schema
	}
	return entry
}

// BuildLaunch composes production inputs from host-verified dependencies. It
// freezes exact data bytes in acyclic identity order and derives the provider's
// native tool projection from the installed package. Startup Messages stay empty:
// only the later worker may perform the real five-message bootstrap exchange.
func (t *Target) BuildLaunch(c LaunchConfig) (*Launch, error) {
	if t == nil || c.Image.Image.Validate() != nil || c.Embedded == nil || !c.Embedded.Matches(c.Image) || c.Model == nil || c.Skills == nil || c.CreatedAt.IsZero() {
		return nil, ErrPreparation
	}
	pin, ok := t.protocol.PackageIdentity()
	if !ok {
		return nil, ErrPreparation
	}
	release := c.Image.Release.Record()
	if release.ContractPackageVersion != pin.Version || release.ContractPackageDigest != pin.Digest || release.Platform != c.Image.Image.ImagePlatform || release.RuntimeProfile != "operator-container/v1" {
		return nil, ErrPreparation
	}
	set := c.Skills.Manifest()
	skillSet, err := t.protocol.ValidateSkillSet(set)
	if err != nil || skillSet["loader_digest"] != c.Embedded.LoaderDigest() {
		return nil, ErrPreparation
	}
	prompt, provenance, err := contracts.ComposePrompt(c.PromptMode, c.Embedded.DefaultPrompt(), c.Replacement, c.Appends)
	if err != nil {
		return nil, err
	}
	bundle, err := contracts.Decode(t.BundleJSON(), contracts.OrdinaryLimit)
	if err != nil {
		return nil, err
	}
	requested := []byte(`{}`)
	if value, ok := bundle.(map[string]any)["requested_limits"]; ok {
		requested, err = canonicalJSON(value, contracts.ControlLimit)
		if err != nil {
			return nil, err
		}
	}
	resolved, err := campaignlimits.Resolve(t.protocol, c.Limits, requested)
	if err != nil {
		return nil, err
	}
	operations := []string{}
	for _, op := range t.protocol.Operations() {
		operations = append(operations, op.Name)
	}
	tools, err := t.protocol.ModelTools(c.Model.Settings().Codec, operations)
	if err != nil {
		return nil, err
	}
	model, err := c.Model.PublicModel(tools)
	if err != nil {
		return nil, err
	}
	template, err := canonicalJSON(map[string]any{
		"api_version": "operator.dev/engine-context/v1alpha1", "campaign_id": t.live.CampaignID(), "launch_id": c.LaunchID, "attempt_index_high_watermark": 0,
		"release":         map[string]any{"image_digest": c.Image.Image.ImageID, "release_record_digest": c.Image.Release.Digest()},
		"scenario_bundle": inputDescriptor("bundle", "scenario-bundle.json", "scenario-bundle", "application/json", contracts.ScenarioBundleSchema, t.BundleJSON()),
		"prompt":          map[string]any{"entry_id": "prompt", "provenance": provenance}, "skills": json.RawMessage(set), "model": json.RawMessage(model), "operations": operations,
		"limits": map[string]any{"campaign": resolved.Campaign, "harness": resolved.Harness}, "remaining_limits": resolved.Campaign,
	}, contracts.EngineContextLimit)
	if err != nil {
		return nil, err
	}
	context, err := t.Context(template)
	if err != nil {
		return nil, err
	}
	context, err = contracts.Canonicalize(context, contracts.EngineContextLimit)
	if err != nil {
		return nil, err
	}
	entries := []map[string]any{
		inputDescriptor("context", "run-context.json", "engine-context", "application/json", contracts.EngineContextSchema, context),
		inputDescriptor("bundle", "scenario-bundle.json", "scenario-bundle", "application/json", contracts.ScenarioBundleSchema, t.BundleJSON()),
		inputDescriptor("prompt", "system-prompt.txt", "system-prompt", "text/plain", "", prompt),
	}
	entries = append(entries, t.references.entries...)
	sort.Slice(entries, func(i, j int) bool { return entries[i]["path"].(string) < entries[j]["path"].(string) })
	tree, err := canonicalJSON(map[string]any{"api_version": "operator.dev/input-tree-manifest/v1alpha1", "entries": entries}, contracts.InputTreeManifestLimit)
	if err != nil {
		return nil, err
	}
	skillManifests := c.Skills.Manifests()
	if err = t.protocol.ValidateManifestSet(tree, set, skillManifests); err != nil {
		return nil, err
	}
	policy, err := t.protocol.ModelPolicyFromContext(context, tools, prompt)
	if err != nil {
		return nil, err
	}
	digest := func(raw []byte, limit int) (string, error) { return contracts.CanonicalDigest(raw, limit) }
	bundleDigest, err := digest(t.BundleJSON(), contracts.OrdinaryLimit)
	if err != nil {
		return nil, err
	}
	hostPolicyDigest, err := digest(t.HostPolicyJSON(), campaign.ManifestLimit)
	if err != nil {
		return nil, err
	}
	// Canonicalize manifests before using their raw digest as the object digest.
	set, err = contracts.Canonicalize(set, contracts.ControlLimit)
	if err != nil {
		return nil, err
	}
	reg := t.protocol.RegistryDigests()
	remaining, _ := canonicalJSON(resolved.Campaign, contracts.ControlLimit)
	harness, _ := canonicalJSON(resolved.Harness, contracts.ControlLimit)
	transport := "fifo"
	if strings.HasPrefix(c.Image.Image.HostPlatform, "darwin/") {
		transport = "spool"
	}
	m := campaign.RunManifest{
		APIVersion: campaign.ManifestVersion, CampaignID: t.live.CampaignID(), LaunchID: c.LaunchID, ContainerID: c.ContainerID, InitialRevision: int64(t.live.Binding().RunRevision), CreatedAt: c.CreatedAt.UTC().Format(time.RFC3339Nano),
		HostPlatform: c.Image.Image.HostPlatform, ImagePlatform: c.Image.Image.ImagePlatform, Transport: transport, RuntimeProfile: release.RuntimeProfile, ImageDigest: c.Image.Image.ImageID, ReleaseRecordDigest: c.Image.Release.Digest(),
		Contract:            campaign.ContractPin{Version: pin.Version, Digest: pin.Digest, CatalogDigest: reg["catalog_digest"], OperationsDigest: reg["operations_digest"]},
		EngineContextDigest: contracts.RawDigest(context), InputTreeDigest: contracts.RawDigest(tree), SkillSetDigest: contracts.RawDigest(set), ScenarioBundleDigest: bundleDigest, HostPolicyDigest: hostPolicyDigest, ModelProfileDigest: c.Model.Digest(), Target: t.Binding(), RemainingLimits: remaining, HarnessLimits: harness, Retention: c.Retention,
	}
	if err = m.Validate(); err != nil {
		return nil, err
	}
	contents := t.ReferenceContents()
	for name, raw := range c.Skills.Contents() {
		contents[name] = raw
	}
	contents["input/run-context.json"] = bytes.Clone(context)
	contents["input/scenario-bundle.json"] = t.BundleJSON()
	contents["input/system-prompt.txt"] = bytes.Clone(prompt)
	return &Launch{Manifest: m, Inputs: campaign.LaunchInputs{InputTree: tree, SkillSet: set, Skills: skillManifests, EngineContext: context, ScenarioBundle: t.BundleJSON(), Prompt: prompt, HostPolicy: t.HostPolicyJSON()}, Contents: contents, ModelTools: tools, ModelPolicy: policy}, nil
}
