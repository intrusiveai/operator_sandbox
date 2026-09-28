package preparation_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/intrusiveai/operator_sandbox/contracts"
	"github.com/intrusiveai/operator_sandbox/internal/campaign"
	"github.com/intrusiveai/operator_sandbox/internal/campaignlimits"
	"github.com/intrusiveai/operator_sandbox/internal/dockercontrol"
	"github.com/intrusiveai/operator_sandbox/internal/hostworker"
	"github.com/intrusiveai/operator_sandbox/internal/imagerelease"
	"github.com/intrusiveai/operator_sandbox/internal/modelprovider"
	"github.com/intrusiveai/operator_sandbox/internal/preparation"
	"github.com/intrusiveai/operator_sandbox/internal/skills"
	"github.com/intrusiveai/operator_sandbox/internal/staging"
)

type embeddedInspector map[string][]byte

func (files embeddedInspector) ReadReleaseFiles(_ context.Context, pin dockercontrol.ImagePin) (dockercontrol.ReleaseFiles, error) {
	return dockercontrol.ReleaseFiles{Files: files, Endpoint: pin.Endpoint, DaemonID: pin.DaemonID, ContainerID: strings.Repeat("f", 64), Removed: true}, nil
}
func launchConfig(t *testing.T, target *preparation.Target, codec string) preparation.LaunchConfig {
	t.Helper()
	p := target.Protocol()
	image, h := cachedApproval(t, p)
	codecs := []string{"openai-chat-text-tools-v1", "openai-responses-text-tools-v1", "anthropic-messages-text-tools-v1", "bedrock-converse-text-tools-v1", "gemini-text-tools-v1"}
	tools := map[string]json.RawMessage{}
	operations := []string{}
	for _, op := range p.Operations() {
		operations = append(operations, op.Name)
	}
	for _, id := range codecs {
		raw, err := p.ModelTools(id, operations)
		if err != nil {
			t.Fatal(err)
		}
		tools[id] = raw
	}
	files := embeddedInspector{imagerelease.PromptPath: []byte("Conduct only authorized experiments.\n"), imagerelease.LoaderPath: []byte("# Test loader fixture, never executed.\n"), imagerelease.ToolCatalogPath: encode(map[string]any{"api_version": "operator.dev/model-tool-catalog/v1alpha1", "codecs": tools})}
	descriptor := func(path string) map[string]any {
		return map[string]any{"size_bytes": len(files[path]), "digest": contracts.RawDigest(files[path])}
	}
	files[imagerelease.ManifestPath] = encode(map[string]any{"api_version": "operator.dev/engine-manifest/v1alpha1", "contract": h.Contract, "runtime_profile": h.RuntimeProfile, "platform": image.Image.ImagePlatform, "entrypoint": []string{"/usr/bin/python3", "-I", "-S", "-B", "/opt/operator/engine/bootstrap.py"}, "transports": []string{"fifo", "spool"}, "prompt": descriptor(imagerelease.PromptPath), "skill_loader": descriptor(imagerelease.LoaderPath), "tool_catalog": descriptor(imagerelease.ToolCatalogPath)})
	embedded, _, err := imagerelease.InspectEmbedded(context.Background(), files, image, h, p)
	if err != nil {
		t.Fatal(err)
	}
	selection, err := skills.Select(context.Background(), p, "", embedded.LoaderDigest(), nil)
	if err != nil {
		t.Fatal(err)
	}
	settings := modelprovider.Settings{APIVersion: modelprovider.Version, ID: "test-model-profile", Codec: codec, Model: "test-model", Authentication: "secret-store", CredentialID: "host-only-key", MaximumPromptTokens: 10000, MaximumCompletionTokens: 1024, MaximumResponseBytes: 1 << 20}
	options := map[string]any{"response_models": []string{"test-model"}}
	suffix := ""
	switch codec {
	case "openai-chat-text-tools-v1":
		settings.Provider = "openai-chat"
		suffix = "/chat/completions"
		options["instruction_role"] = "developer"
	case "openai-responses-text-tools-v1":
		settings.Provider = "openai-responses"
		suffix = "/responses"
		options["reasoning"] = nil
	case "anthropic-messages-text-tools-v1":
		settings.Provider = "anthropic-messages"
		suffix = "/messages"
		settings.APIVersionHeader = "2023-06-01"
		options["thinking"] = map[string]any{"type": "disabled"}
	case "bedrock-converse-text-tools-v1":
		settings.Provider = "bedrock-converse"
		settings.Authentication = "workload-identity"
		settings.CredentialID = ""
		settings.Region = "us-west-2"
		options = map[string]any{}
	case "gemini-text-tools-v1":
		settings.Provider = "gemini-api"
		suffix = "/models/test-model:generateContent"
		options["thinking_config"] = map[string]any{}
	}
	if suffix != "" {
		settings.Endpoint = "https://private-provider.example" + suffix
	}
	settings.CodecOptions = encode(options)
	model, err := modelprovider.Parse(encode(settings))
	if err != nil {
		t.Fatal(err)
	}
	limits, err := campaignlimits.ParseHost([]byte(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	return preparation.LaunchConfig{LaunchID: "launch-1", ContainerID: strings.Repeat("a", 64), CreatedAt: time.Date(2026, 9, 27, 12, 0, 0, 0, time.UTC), Image: image, Embedded: embedded, Model: model, Skills: selection, Limits: limits, Retention: campaign.Retention{Mode: "manual-purge", MaxJournalBytes: 1 << 30, MaxSegmentBytes: campaign.MaxEventBytes}, PromptMode: "default"}
}

func TestProductionLaunchInputsForEveryCodec(t *testing.T) {
	for _, codec := range []string{"openai-chat-text-tools-v1", "openai-responses-text-tools-v1", "anthropic-messages-text-tools-v1", "bedrock-converse-text-tools-v1", "gemini-text-tools-v1"} {
		t.Run(codec, func(t *testing.T) {
			in := fixture(t)
			var bundle map[string]any
			json.Unmarshal(in.Bundle, &bundle)
			bundle["requested_limits"] = map[string]any{"attempt_admissions": 2, "active_seconds": 60, "harness": map[string]any{"max_model_turns": 5}}
			in.Bundle = encode(bundle)
			target, err := preparation.Build(in)
			if err != nil {
				t.Fatal(err)
			}
			c := launchConfig(t, target, codec)
			c.PromptMode = "extension"
			c.Appends = [][]byte{[]byte("Keep evidence distinctions explicit.")}
			launch, err := target.BuildLaunch(c)
			if err != nil {
				t.Fatal(err)
			}
			if len(launch.Inputs.Messages) != 0 {
				t.Fatal("fabricated guest bootstrap")
			}
			expected := append(c.Embedded.DefaultPrompt(), []byte("\n\nKeep evidence distinctions explicit.")...)
			if !bytes.Equal(launch.Inputs.Prompt, expected) {
				t.Fatal("prompt altered")
			}
			for _, private := range []string{"private-provider.example", "host-only-key"} {
				if bytes.Contains(launch.Inputs.EngineContext, []byte(private)) {
					t.Fatal("private model configuration leaked")
				}
			}
			var limits map[string]int64
			json.Unmarshal(launch.Manifest.RemainingLimits, &limits)
			if limits["attempt_admissions"] != 2 || limits["campaign_time_ms"] != 60000 || limits["model_turns"] != 5 {
				t.Fatal(limits)
			}
			if _, err = hostworker.StartupMessages(in.Protocol, launch.Manifest, launch.Inputs); err != nil {
				t.Fatal("production startup", err)
			}
			root := t.TempDir()
			os.Chmod(root, 0700)
			writer, err := campaign.Create(root, launch.Manifest)
			if err != nil {
				t.Fatal(err)
			}
			defer writer.Close()
			if err = writer.ConfigureFreeSpace(0); err != nil {
				t.Fatal(err)
			}
			launch.Inputs.Messages = startup(t, target, writer, launch.Inputs)
			if err = launch.Manifest.ValidateLaunchInputs(in.Protocol, launch.Inputs); err != nil {
				t.Fatal("complete identity chain", err)
			}
			if _, err = target.Persist(writer, launch.Inputs.EngineContext); err != nil {
				t.Fatal(err)
			}
			parent := t.TempDir()
			os.Chmod(parent, 0700)
			tree, err := staging.Create(context.Background(), in.Protocol, parent, staging.Manifests{InputTree: launch.Inputs.InputTree, SkillSet: launch.Inputs.SkillSet, Skills: launch.Inputs.Skills}, launch.Contents)
			if err != nil {
				t.Fatal("real staging", err)
			}
			defer tree.Discard()
			if err = tree.Verify(context.Background()); err != nil {
				t.Fatal(err)
			}
			if err = launch.RetainInputs(context.Background(), writer, tree); err != nil {
				t.Fatal("input retention", err)
			}
			if err = launch.RetainInputs(context.Background(), writer, tree); err == nil {
				t.Fatal("repeated input retention accepted")
			}
			if err = writer.Close(); err != nil {
				t.Fatal(err)
			}
			markers, parts := 0, 0
			_, err = campaign.Inspect(root, "campaign-1", func(e campaign.Event) error {
				if e.Kind == "campaign.launch-inputs-retained" {
					markers++
				}
				if e.Kind == "campaign.launch-input" {
					parts++
					if len(e.Content) != 1 || e.Content[0].Role != "input-part" {
						t.Fatal("invalid retained part")
					}
				}
				return nil
			})
			if err != nil || markers != 1 || parts != tree.Receipt().FileCount {
				t.Fatal("retained input chain", markers, parts, err)
			}
			if err = tree.Discard(); err != nil {
				t.Fatal(err)
			}
			launch.Inputs.Prompt[0] = 'X'
			c.Appends[0][0] = 'Y'
			again, err := target.BuildLaunch(launchConfig(t, target, codec))
			if err != nil || bytes.Equal(again.Inputs.Prompt, launch.Inputs.Prompt) {
				t.Fatal("launch data alias", err)
			}
		})
	}
}
func TestProductionLaunchRejectsMismatchedHostDependencies(t *testing.T) {
	in := fixture(t)
	target, err := preparation.Build(in)
	if err != nil {
		t.Fatal(err)
	}
	base := launchConfig(t, target, "openai-chat-text-tools-v1")
	for _, mode := range []string{"image", "daemon", "release", "loader", "model", "skills", "time", "identity", "prompt", "limits"} {
		t.Run(mode, func(t *testing.T) {
			c := base
			switch mode {
			case "image":
				c.Image.Image.ImageID = contracts.RawDigest([]byte("other"))
			case "daemon":
				c.Image.Image.DaemonID = "other"
			case "release":
				c.Image.Release = imagerelease.Approval{}
			case "loader":
				c.Skills, err = skills.Select(context.Background(), in.Protocol, "", contracts.RawDigest([]byte("other loader")), nil)
				if err != nil {
					t.Fatal(err)
				}
			case "model":
				c.Model = nil
			case "skills":
				c.Skills = nil
			case "time":
				c.CreatedAt = time.Time{}
			case "identity":
				c.ContainerID = "short-id"
			case "prompt":
				c.PromptMode = "replacement"
				c.Replacement = []byte(" ")
			case "limits":
				c.Limits = campaignlimits.Host{}
			}
			if _, err := target.BuildLaunch(c); err == nil {
				t.Fatal("accepted", mode)
			}
		})
	}
}

func TestLaunchStagesSkillAndPassiveReference(t *testing.T) {
	ctx := context.Background()
	in := fixture(t)
	var bundle map[string]any
	json.Unmarshal(in.Bundle, &bundle)
	reference := []byte("Reference bytes retained verbatim.\n")
	digest := contracts.RawDigest(reference)
	bundle["artifacts"] = []any{map[string]any{"artifact_id": "reference-one", "digest": digest, "size_bytes": len(reference), "media_type": "text/plain", "purpose": "scenario guidance", "visibility": "operator-engine", "required": true}}
	in.Bundle = encode(bundle)
	in.References = map[string][]byte{digest: reference}
	target, err := preparation.Build(in)
	if err != nil {
		t.Fatal(err)
	}
	c := launchConfig(t, target, "openai-chat-text-tools-v1")
	base := t.TempDir()
	source, store := filepath.Join(base, "source"), filepath.Join(base, "store")
	if err = os.Mkdir(source, 0700); err != nil {
		t.Fatal(err)
	}
	skill := []byte("---\nname: example\ndescription: Record evidence limitations.\n---\nTreat references as data.\n")
	if err = os.WriteFile(filepath.Join(source, "SKILL.md"), skill, 0600); err != nil {
		t.Fatal(err)
	}
	built, err := skills.Build(ctx, in.Protocol, "project", source)
	if err != nil {
		t.Fatal(err)
	}
	if err = built.Install(ctx, in.Protocol, store); err != nil {
		t.Fatal(err)
	}
	c.Skills, err = skills.Select(ctx, in.Protocol, store, c.Embedded.LoaderDigest(), []string{built.Receipt().Digest})
	if err != nil {
		t.Fatal(err)
	}
	launch, err := target.BuildLaunch(c)
	if err != nil {
		t.Fatal(err)
	}
	if len(launch.Inputs.Skills) != 1 || !bytes.Equal(launch.Contents["input/artifacts/sha256-"+strings.TrimPrefix(digest, "sha256:")], reference) {
		t.Fatal("missing launch input")
	}
	if !bytes.Equal(launch.Contents["customer-skills/project:example/SKILL.md"], skill) {
		t.Fatal("missing selected skill")
	}
	parent := t.TempDir()
	os.Chmod(parent, 0700)
	tree, err := staging.Create(ctx, in.Protocol, parent, staging.Manifests{InputTree: launch.Inputs.InputTree, SkillSet: launch.Inputs.SkillSet, Skills: launch.Inputs.Skills}, launch.Contents)
	if err != nil {
		t.Fatal(err)
	}
	if err = tree.Verify(ctx); err != nil {
		t.Fatal(err)
	}
	if err = tree.Discard(); err != nil {
		t.Fatal(err)
	}
}

func TestLaunchRetentionFailureNeverPublishesCompletion(t *testing.T) {
	for _, mode := range []string{"changed tree", "cancelled", "wrong tree"} {
		t.Run(mode, func(t *testing.T) {
			in := fixture(t)
			target, err := preparation.Build(in)
			if err != nil {
				t.Fatal(err)
			}
			config := launchConfig(t, target, "openai-chat-text-tools-v1")
			launch, err := target.BuildLaunch(config)
			if err != nil {
				t.Fatal(err)
			}
			root := t.TempDir()
			os.Chmod(root, 0700)
			writer, err := campaign.Create(root, launch.Manifest)
			if err != nil {
				t.Fatal(err)
			}
			defer writer.Close()
			writer.ConfigureFreeSpace(0)
			staged := launch
			if mode == "wrong tree" {
				config.PromptMode = "replacement"
				config.Replacement = []byte("Different prompt.")
				staged, err = target.BuildLaunch(config)
				if err != nil {
					t.Fatal(err)
				}
			}
			parent := t.TempDir()
			os.Chmod(parent, 0700)
			tree, err := staging.Create(context.Background(), in.Protocol, parent, staging.Manifests{InputTree: staged.Inputs.InputTree, SkillSet: staged.Inputs.SkillSet, Skills: staged.Inputs.Skills}, staged.Contents)
			if err != nil {
				t.Fatal(err)
			}
			defer tree.Discard()
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if mode == "cancelled" {
				cancel()
			}
			if mode == "changed tree" {
				file := filepath.Join(tree.Directory(), "input", "system-prompt.txt")
				if err = os.Chmod(file, 0600); err != nil {
					t.Fatal(err)
				}
				if err = os.WriteFile(file, []byte("changed"), 0444); err != nil {
					t.Fatal(err)
				}
			}
			if err = launch.RetainInputs(ctx, writer, tree); err == nil {
				t.Fatal("bad input retained")
			}
			if writer.Fence().Err() == nil {
				t.Fatal("failure did not fence campaign")
			}
			writer.Close()
			_, err = campaign.Inspect(root, "campaign-1", func(e campaign.Event) error {
				if e.Kind == "campaign.launch-inputs-retained" {
					t.Fatal("false completion")
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}
