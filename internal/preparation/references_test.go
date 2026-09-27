package preparation_test

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/intrusiveai/operator_sandbox/contracts"
	"github.com/intrusiveai/operator_sandbox/internal/campaign"
	"github.com/intrusiveai/operator_sandbox/internal/preparation"
)

func TestPassiveReferencesAreFrozenAndBound(t *testing.T) {
	for _, mode := range []string{"success", "missing", "corrupt", "extra", "conflicting metadata", "protected", "omitted supplied", "schema mismatch"} {
		t.Run(mode, func(t *testing.T) {
			input := fixture(t)
			var bundle map[string]any
			json.Unmarshal(input.Bundle, &bundle)
			raw := []byte("Passive scenario guidance.")
			digest := contracts.RawDigest(raw)
			descriptor := func(id string) map[string]any {
				return map[string]any{"artifact_id": id, "digest": digest, "size_bytes": len(raw), "media_type": "text/plain", "purpose": "reference", "visibility": "operator-engine", "required": false}
			}
			first, second, omitted := descriptor("z-ref"), descriptor("a-ref"), descriptor("m-omitted")
			omitted["digest"] = contracts.RawDigest([]byte("unavailable"))
			omitted["omission_reason"] = "Not supplied."
			bundle["artifacts"] = []any{first, omitted, second}
			input.References = map[string][]byte{digest: raw}
			switch mode {
			case "missing":
				delete(input.References, digest)
			case "corrupt":
				input.References[digest] = []byte("changed")
			case "extra":
				input.References[contracts.RawDigest([]byte("extra"))] = []byte("extra")
			case "conflicting metadata":
				second["media_type"] = "application/octet-stream"
			case "protected":
				first["visibility"] = "operator-only"
			case "omitted supplied":
				input.References[omitted["digest"].(string)] = []byte("unavailable")
			case "schema mismatch":
				first["schema_id"] = contracts.EngineConclusionSchema
			}
			input.Bundle = encode(bundle)
			target, err := preparation.Build(input)
			if (err == nil) != (mode == "success") {
				t.Fatalf("mode=%s err=%v", mode, err)
			}
			if err != nil {
				return
			}
			raw[0] = 'X'
			files := target.ReferenceContents()
			for key, data := range files {
				if !bytes.Equal(data, []byte("Passive scenario guidance.")) {
					t.Fatal("source mutated")
				}
				data[0] = 'Y'
				files[key] = data
			}
			// Existing launch fixture supplies the three core descriptors; add the real
			// reference inventory before checking identities and persisting preparation.
			writer, launch, root := preparedLaunchConfigured(t, target, nil, "darwin/arm64")
			context, err := target.Protocol().ValidateEngineContext(launch.EngineContext)
			if err != nil {
				t.Fatal(err)
			}
			refs := context["references"].([]any)
			bindings := context["artifact_bindings"].([]any)
			if len(refs) != 1 || len(bindings) != 2 || bindings[0].(map[string]any)["artifact_id"] != "a-ref" || bindings[1].(map[string]any)["artifact_id"] != "z-ref" {
				t.Fatal("alias/order binding wrong")
			}
			if len(context["omissions"].([]any)) != 1 {
				t.Fatal("omission lost")
			}
			if _, err = target.Persist(writer, launch.EngineContext); err != nil {
				t.Fatal(err)
			}
			if len(target.ReferenceContents()) != 1 {
				t.Fatal("inventory changed")
			}
			// The journal retains passive bytes under their own attribution, without
			// manufacturing native execution artifacts.
			if err = writer.Close(); err != nil {
				t.Fatal(err)
			}
			count := 0
			_, err = campaign.Inspect(root, "campaign-1", func(e campaign.Event) error {
				if e.Kind == "campaign.reference-staged" {
					count++
				}
				return nil
			})
			if err != nil || count != 1 {
				t.Fatal("reference journal retention", count, err)
			}
		})
	}
}
