package preparation_test

import (
	"encoding/json"
	"testing"

	"github.com/intrusive-ai/operator-sandbox/contracts"
	"github.com/intrusive-ai/operator-sandbox/internal/campaign"
	"github.com/intrusive-ai/operator-sandbox/internal/preparation"
)

func inputTree(t *testing.T, target *preparation.Target, context, prompt []byte) []byte {
	t.Helper()
	c, _ := target.Protocol().ValidateEngineContext(context)
	bundle := c["scenario_bundle"].(map[string]any)
	descriptor := func(id, path, role, media, schema string, raw []byte) map[string]any {
		v := map[string]any{"entry_id": id, "root_kind": "input", "path": path, "role": role, "media_type": media, "size_bytes": len(raw), "digest": contracts.RawDigest(raw)}
		if schema != "" {
			v["schema_id"] = schema
		}
		return v
	}
	return encode(map[string]any{"api_version": "operator.dev/input-tree-manifest/v1alpha1", "entries": []any{descriptor("context", "run-context.json", "engine-context", "application/json", contracts.EngineContextSchema, context), bundle, descriptor("prompt", "system-prompt.txt", "system-prompt", "text/plain", "", prompt)}})
}
func startup(t *testing.T, target *preparation.Target, w *campaign.Writer, in campaign.LaunchInputs) [][]byte {
	t.Helper()
	var messages []map[string]any
	_ = json.Unmarshal(read(t, "../../schemas/fixtures/startup-example.json"), &messages)
	c, _ := target.Protocol().ValidateEngineContext(in.EngineContext)
	m := w.Manifest()
	pin, _ := target.Protocol().PackageIdentity()
	binding := map[string]any{"campaign_id": m.CampaignID, "launch_id": m.LaunchID, "run_revision": m.InitialRevision, "input_tree_digest": contracts.RawDigest(in.InputTree), "engine_context_digest": contracts.RawDigest(in.EngineContext), "prompt_digest": contracts.RawDigest(in.Prompt), "skill_set_digest": contracts.RawDigest(in.SkillSet), "image_digest": m.ImageDigest, "release_record_digest": m.ReleaseRecordDigest, "contract_package_digest": pin.Digest, "contract_package_version": pin.Version}
	refs := map[string]any{}
	for name, raw := range map[string][]byte{"input_tree": in.InputTree, "skill_set": in.SkillSet} {
		schema, path := contracts.InputTreeManifestSchema, "input-tree"
		if name == "skill_set" {
			schema, path = contracts.SkillSetManifestSchema, "skill-set"
		}
		digest, _ := contracts.CanonicalDigest(raw, contracts.InputTreeManifestLimit)
		refs[name] = map[string]any{"path_id": path, "schema_id": schema, "size_bytes": len(raw), "digest": contracts.RawDigest(raw), "object_digest": digest}
	}
	out := [][]byte{}
	for _, message := range messages {
		message["run_revision"] = m.InitialRevision
		body := message["body"].(map[string]any)
		for key := range body {
			switch key {
			case "contract", "release", "operations", "limits", "remaining_limits":
				body[key] = c[key]
			case "binding":
				body[key] = binding
			case "input_tree", "skill_set":
				body[key] = refs[key]
			case "engine_context_object_digest":
				body[key] = m.EngineContextDigest
			case "run_manifest_digest":
				body[key] = w.ManifestDigest()
			case "host_platform":
				body[key] = m.HostPlatform
			case "transport":
				body[key] = m.Transport
			}
		}
		out = append(out, encode(message))
	}
	return out
}
