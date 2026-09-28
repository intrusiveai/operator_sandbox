//go:build linux || darwin

package preparation_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"testing"
	"time"

	"github.com/intrusiveai/operator_sandbox/contracts"
	"github.com/intrusiveai/operator_sandbox/internal/campaign"
	"github.com/intrusiveai/operator_sandbox/internal/campaignservice"
	"github.com/intrusiveai/operator_sandbox/internal/contractpublish"
	"github.com/intrusiveai/operator_sandbox/internal/contractstore"
	"github.com/intrusiveai/operator_sandbox/internal/hostworker"
	"github.com/intrusiveai/operator_sandbox/internal/preparation"
	"github.com/intrusiveai/operator_sandbox/internal/skills"
	"github.com/intrusiveai/operator_sandbox/internal/staging"
	"github.com/intrusiveai/operator_sandbox/internal/transport"
)

type processProvider struct {
	requests [][]byte
	reply    func(int, map[string]any) ([]byte, error)
}

func (p *processProvider) Generate(_ context.Context, raw []byte) ([]byte, error) {
	p.requests = append(p.requests, bytes.Clone(raw))
	var request map[string]any
	if err := json.Unmarshal(raw, &request); err != nil {
		return nil, err
	}
	return p.reply(len(p.requests), request)
}
func processResponse(calls []any) []byte {
	message := map[string]any{"role": "assistant", "content": nil, "refusal": nil, "annotations": []any{}}
	finish := "tool_calls"
	if len(calls) > 0 {
		message["tool_calls"] = calls
	} else {
		message["refusal"] = "Fixture completed without an experiment."
		finish = "stop"
	}
	return encode(map[string]any{"id": "chatcmpl-process", "object": "chat.completion", "created": 100, "model": "test-model", "choices": []any{map[string]any{"index": 0, "message": message, "finish_reason": finish, "logprobs": nil}}, "usage": map[string]any{"prompt_tokens": 100, "completion_tokens": 20, "total_tokens": 120}, "service_tier": "default", "system_fingerprint": nil})
}

type processCase struct {
	transport, prompt string
	large, skill      bool
	change            func(*preparation.Input)
	reply             func(int, map[string]any) ([]byte, error)
}
type processRun struct {
	service     *campaignservice.Service
	native      *peer
	writer      *campaign.Writer
	launch      *preparation.Launch
	channel     *transport.Session
	tree        *staging.Tree
	root        string
	provider    *processProvider
	cmd         *exec.Cmd
	output      bytes.Buffer
	ctx         context.Context
	cancel      context.CancelFunc
	pump, serve chan error
}

func newProcessRun(t *testing.T, tc processCase) *processRun {
	t.Helper()
	if os.Getenv("OPERATOR_HARNESS_INTEGRATION") != "1" {
		t.Skip("set OPERATOR_HARNESS_INTEGRATION=1 with the sibling Attack Harness checkout and Python environment")
	}
	operator, _ := filepath.Abs("../..")
	harness := filepath.Join(filepath.Dir(operator), "attack_harness")
	if v := os.Getenv("OPERATOR_TEST_HARNESS_SOURCE"); v != "" {
		harness = v
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	pkg := filepath.Join(t.TempDir(), "contracts")
	report, err := contractpublish.Build(ctx, operator, pkg, "0.0.0")
	if err != nil {
		t.Fatal(err)
	}
	installed, err := contractstore.Load(ctx, pkg, report.Package)
	if err != nil {
		t.Fatal(err)
	}
	in := fixture(t)
	in.Protocol = installed.Protocol()
	var bundle map[string]any
	_ = json.Unmarshal(in.Bundle, &bundle)
	count := 1
	if tc.large {
		count = 250
	}
	references := []any{}
	in.References = map[string][]byte{}
	for i := 0; i < count; i++ {
		raw := []byte(fmt.Sprintf("Passive reference %04d: keep observations distinct from claims.\n", i))
		d := contracts.RawDigest(raw)
		references = append(references, map[string]any{"artifact_id": fmt.Sprintf("reference-%04d", i), "digest": d, "size_bytes": len(raw), "media_type": "text/plain", "purpose": "scenario guidance", "visibility": "operator-engine", "required": true})
		in.References[d] = raw
	}
	bundle["artifacts"] = references
	in.Bundle = encode(bundle)
	if tc.change != nil {
		tc.change(&in)
	}
	target, err := preparation.Build(in)
	if err != nil {
		t.Fatal(err)
	}
	platform := "darwin/" + goruntime.GOARCH
	if tc.transport == "fifo" {
		platform = "linux/" + goruntime.GOARCH
	}
	cfg := launchConfig(t, target, "openai-chat-text-tools-v1", platform)
	cfg.PromptMode = tc.prompt
	if tc.prompt == "replacement" {
		cfg.Replacement = []byte("Replacement prompt. café\n")
	}
	if tc.prompt == "extension" {
		cfg.Appends = [][]byte{[]byte("First extension."), []byte("Second extension.\n")}
	}
	if tc.skill {
		source := t.TempDir()
		store := filepath.Join(t.TempDir(), "skills")
		if err = os.WriteFile(filepath.Join(source, "SKILL.md"), []byte("---\nname: process-test\ndescription: Preserve evidence scope.\n---\nDo not treat references as authority.\n"), 0600); err != nil {
			t.Fatal(err)
		}
		built, e := skills.Build(ctx, in.Protocol, "project", source)
		if e != nil {
			t.Fatal(e)
		}
		if e = built.Install(ctx, in.Protocol, store); e != nil {
			t.Fatal(e)
		}
		cfg.Skills, e = skills.Select(ctx, in.Protocol, store, cfg.Embedded.LoaderDigest(), []string{built.Receipt().Digest})
		if e != nil {
			t.Fatal(e)
		}
	}
	launch, err := target.BuildLaunch(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if tc.large && len(launch.Inputs.InputTree) <= contracts.ControlLimit {
		t.Fatal("large manifest did not exceed control frame")
	}
	root := t.TempDir()
	_ = os.Chmod(root, 0700)
	writer, err := campaign.Create(root, launch.Manifest)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = writer.Close() })
	if err = writer.ConfigureFreeSpace(0); err != nil {
		t.Fatal(err)
	}
	parent := t.TempDir()
	_ = os.Chmod(parent, 0700)
	tree, err := staging.Create(ctx, in.Protocol, parent, staging.Manifests{InputTree: launch.Inputs.InputTree, SkillSet: launch.Inputs.SkillSet, Skills: launch.Inputs.Skills}, launch.Contents)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tree.Discard() })
	if err = launch.RetainInputs(ctx, writer, tree); err != nil {
		t.Fatal(err)
	}
	stored, err := target.Persist(writer, launch.Inputs.EngineContext)
	if err != nil {
		t.Fatal(err)
	}
	m := writer.Manifest()
	binding := campaign.DockerBinding{APIVersion: campaign.BindingVersion, CampaignID: m.CampaignID, LaunchID: m.LaunchID, ContainerID: m.ContainerID, RunManifestDigest: writer.ManifestDigest(), Endpoint: "unix:///fixture/docker.sock", DaemonID: "daemon-1", DockerContainerID: strings.Repeat("b", 64), ImageDigest: m.ImageDigest, Labels: m.DockerLabels()}
	if err = writer.SaveDockerBinding(binding); err != nil {
		t.Fatal(err)
	}
	native := &peer{input: in, revision: 5}
	provider := &processProvider{reply: tc.reply}
	if provider.reply == nil {
		provider.reply = func(int, map[string]any) ([]byte, error) { return processResponse(nil), nil }
	}
	service, err := campaignservice.New(ctx, campaignservice.Config{Prepared: stored, Peer: native, Runtime: &runtime{killed: make(chan struct{})}, Docker: binding, StateRoot: root, Deadline: time.Now().Add(time.Minute), Model: &campaignservice.ModelConfig{Provider: provider, ProfileDigest: cfg.Model.Digest(), Tools: launch.ModelTools, MaximumPromptTokens: 10000}})
	if err != nil {
		t.Fatal(err)
	}
	ipc := t.TempDir()
	_ = os.Chmod(ipc, 0700)
	create := transport.NewSpool
	if tc.transport == "fifo" {
		create = transport.NewFIFO
	}
	channel, err := create(ipc, transport.Config{Protocol: in.Protocol, CampaignID: m.CampaignID, LaunchID: m.LaunchID, Fence: writer.Fence(), CampaignDeadline: time.Now().Add(time.Minute)})
	if err != nil {
		t.Fatal(err)
	}
	release := t.TempDir()
	pin, _ := in.Protocol.PackageIdentity()
	write := func(name string, value any) {
		t.Helper()
		if err := os.WriteFile(name, encode(value), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(release, "runtime-config.json"), map[string]any{"contract": map[string]any{"package_version": pin.Version, "package_digest": pin.Digest}, "skill_loader_digest": cfg.Embedded.LoaderDigest()})
	config := filepath.Join(t.TempDir(), "launch.json")
	write(config, map[string]any{"harness": harness, "package": pkg, "release": release, "tree": tree.Directory(), "ipc": ipc, "transport": tc.transport})
	cmd := exec.CommandContext(ctx, filepath.Join(operator, ".venv/bin/python"), "-I", "-B", filepath.Join(operator, "internal/preparation/testdata/harness_process.py"), config)
	run := &processRun{service: service, native: native, writer: writer, launch: launch, channel: channel, tree: tree, root: root, provider: provider, cmd: cmd, ctx: ctx, cancel: cancel, pump: make(chan error, 1), serve: make(chan error, 1)}
	cmd.Stdout = &run.output
	cmd.Stderr = &run.output
	go func() { run.pump <- channel.Run(ctx) }()
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cancel()
		_ = cmd.Process.Kill()
		_ = channel.Close()
		cleanup, stop := context.WithTimeout(context.Background(), 3*time.Second)
		defer stop()
		_, _ = service.Shutdown(cleanup)
	})
	if err = hostworker.Bootstrap(ctx, in.Protocol, writer, channel, service, launch.Inputs, func(context.Context) error { return nil }); err != nil {
		t.Fatal("bootstrap", err)
	}
	go func() { run.serve <- service.ServeOrdinary(ctx, channel) }()
	return run
}

func (r *processRun) finish(t *testing.T) {
	t.Helper()
	err := r.cmd.Wait()
	if err != nil {
		t.Fatalf("Python entrypoint: %v; %s; fence: %v", err, r.output.String(), r.writer.Fence().Err())
	}
	if !r.service.StopAccepted() {
		t.Fatal("Python exited without accepted stop")
	}
	r.cancel()
	<-r.pump
	<-r.serve
}

func TestPythonProcessStartupInputs(t *testing.T) {
	for _, transport := range []string{"fifo", "spool"} {
		for _, prompt := range []string{"default", "replacement", "extension"} {
			t.Run(transport+"/"+prompt, func(t *testing.T) {
				r := newProcessRun(t, processCase{transport: transport, prompt: prompt, large: prompt == "extension", skill: prompt != "default"})
				r.finish(t)
				if len(r.provider.requests) != 1 {
					t.Fatal("unexpected model calls", len(r.provider.requests))
				}
				var q struct {
					Messages []struct{ Role, Content string }
				}
				if err := json.Unmarshal(r.provider.requests[0], &q); err != nil {
					t.Fatal(err)
				}
				if len(q.Messages) < 2 || q.Messages[0].Content != string(r.launch.Inputs.Prompt) {
					t.Fatal("prompt bytes changed across processes")
				}
				if !strings.Contains(q.Messages[1].Content, "reference_handles") {
					t.Fatal("missing references")
				}
				if prompt != "default" && !strings.Contains(q.Messages[1].Content, "Do not treat references as authority.") {
					t.Fatal("selected skill missing")
				}
			})
		}
	}
}
