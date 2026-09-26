//go:build linux || darwin

package dockercontrol

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/intrusiveai/operator_sandbox/contracts"
	"github.com/intrusiveai/operator_sandbox/internal/campaign"
	"github.com/intrusiveai/operator_sandbox/internal/staging"
	"github.com/intrusiveai/operator_sandbox/internal/transport"
	"github.com/intrusiveai/operator_sandbox/schemas"
)

func launchFixture(t *testing.T, mode string) (*LaunchPlan, string, *campaign.Writer) {
	t.Helper()
	ctx := context.Background()
	encode := func(v any) []byte {
		raw, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	p, err := contracts.LoadProtocol(schemas.Files)
	if err != nil {
		t.Fatal(err)
	}
	files := map[string][]byte{}
	entries, _ := fs.ReadDir(schemas.Files, ".")
	for _, entry := range entries {
		files[entry.Name()], err = schemas.Files.ReadFile(entry.Name())
		if err != nil {
			t.Fatal(err)
		}
	}
	profiles := map[string]string{"jcs-v1": "semantics/jcs", "manifest-paths-v1": "semantics/paths", "harness-loop-v1": "semantics/loop"}
	for _, name := range profiles {
		files[name] = []byte("Test only.")
	}
	pkg, pin, err := p.BuildPackageManifest("0.0.0", files, profiles)
	if err != nil {
		t.Fatal(err)
	}
	p, err = p.LoadVerifiedProtocol(pkg, files, pin)
	if err != nil {
		t.Fatal(err)
	}
	contents := map[string][]byte{}
	inventory := []any{}
	for _, item := range []struct{ name, role, media, schema string }{{"run-context.json", "engine-context", "application/json", contracts.EngineContextSchema}, {"scenario-bundle.json", "scenario-bundle", "application/json", "urn:operator:schema:scenario-bundle:v1alpha1"}, {"system-prompt.txt", "system-prompt", "text/plain", ""}} {
		raw := []byte("{}")
		contents["input/"+item.name] = raw
		entry := map[string]any{"entry_id": item.role, "root_kind": "input", "path": item.name, "role": item.role, "media_type": item.media, "size_bytes": len(raw), "digest": contracts.RawDigest(raw)}
		if item.schema != "" {
			entry["schema_id"] = item.schema
		}
		inventory = append(inventory, entry)
	}
	set := map[string]any{"api_version": "operator.dev/skill-set-manifest/v1alpha1", "loader_schema": "operator.dev/instruction-skill-loader/v1alpha1", "loader_digest": contracts.RawDigest([]byte("loader")), "skills": []any{}}
	set["loading_digest"], err = contracts.CanonicalDigest(encode(set), contracts.ControlLimit)
	if err != nil {
		t.Fatal(err)
	}
	parent := t.TempDir()
	_ = os.Chmod(parent, 0700)
	tree, err := staging.Create(ctx, p, parent, staging.Manifests{InputTree: encode(map[string]any{"api_version": "operator.dev/input-tree-manifest/v1alpha1", "entries": inventory}), SkillSet: encode(set)}, contents)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tree.Discard() })
	d := contracts.RawDigest([]byte("fixture"))
	m := campaign.RunManifest{APIVersion: campaign.ManifestVersion, CampaignID: "campaign-1", LaunchID: "launch-1", ContainerID: strings.Repeat("a", 64), CreatedAt: "2026-09-26T00:00:00Z", HostPlatform: "darwin/arm64", ImagePlatform: "linux/arm64", Transport: mode, RuntimeProfile: "operator-container/v1", ImageDigest: d, ReleaseRecordDigest: d, Contract: campaign.ContractPin{Version: pin.Version, Digest: pin.Digest, CatalogDigest: d, OperationsDigest: d}, EngineContextDigest: d, InputTreeDigest: tree.Receipt().InputTreeDigest, SkillSetDigest: tree.Receipt().SkillSetDigest, ScenarioBundleDigest: d, HostPolicyDigest: d, ModelProfileDigest: d, Target: campaign.TargetBinding{Adapter: "interceptor/v1", SessionID: "session-1", WorkerInstanceID: "worker-1", NativeFeedbackProfile: "diagnostic", CapabilitySourceDigest: d, CapabilityProjectionDigest: d}, Retention: campaign.Retention{Mode: "manual-purge", MaxJournalBytes: 128 << 20, MaxSegmentBytes: campaign.MaxEventBytes}, RemainingLimits: json.RawMessage(`{"campaign_time_ms":1000,"attempt_admissions":100,"model_tokens":1000,"model_turns":300,"artifact_bytes":10000,"artifact_objects":100,"snapshot_admissions":100,"snapshot_bytes":10000,"observation_reads":100,"observation_bytes":10000}`), HarnessLimits: json.RawMessage(`{"max_model_turns":300,"max_tool_calls":2000,"max_tool_calls_per_response":16,"max_invalid_tool_calls":50,"max_consecutive_invalid_tool_calls":5,"max_read_bytes":268435456,"max_no_progress_turns":10}`)}
	if mode == "fifo" {
		m.HostPlatform = "linux/arm64"
	}
	root := t.TempDir()
	_ = os.Chmod(root, 0700)
	w, err := campaign.Create(root, m)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = w.Close() })
	ipc := t.TempDir()
	_ = os.Chmod(ipc, 0700)
	config := transport.Config{Protocol: p, CampaignID: m.CampaignID, LaunchID: m.LaunchID, Fence: w.Fence(), CampaignDeadline: time.Now().Add(time.Minute)}
	var channel *transport.Session
	if mode == "fifo" {
		channel, err = transport.NewFIFO(ipc, config)
	} else {
		channel, err = transport.NewSpool(ipc, config)
	}
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = channel.Close() })
	plan, err := NewLaunchPlan(ctx, parent, ImagePin{Selector: "image:latest", Endpoint: "unix:///saved/docker.sock", DaemonID: "daemon-1", ImageID: d, HostPlatform: m.HostPlatform, ImagePlatform: m.ImagePlatform}, m, tree, channel)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = plan.Discard() })
	return plan, root, w
}

type launchDocker struct {
	t                *testing.T
	p                *LaunchPlan
	created, started int
	state            string
	createErr        bool
	volumes          string
	daemon           string
}

func (f *launchDocker) run(ctx context.Context, endpoint string, args ...string) ([]byte, error) {
	if endpoint != f.p.image.Endpoint {
		f.t.Fatal("endpoint changed")
	}
	switch args[0] {
	case "info":
		return json.Marshal(f.daemon)
	case "image":
		if args[5] == "{{json .Config.Volumes}}" {
			return []byte(f.volumes), nil
		}
		return json.Marshal(map[string]any{"id": f.p.image.ImageID, "os": "linux", "architecture": "arm64", "repo_digests": []string{}})
	case "container":
		switch args[1] {
		case "create":
			f.created++
			if !reflect.DeepEqual(args, f.p.args) {
				f.t.Fatal("changed plan")
			}
			if f.createErr {
				return nil, errors.New("lost reply")
			}
			f.state = "created"
			return []byte(strings.Repeat("b", 64)), nil
		case "start":
			f.started++
			f.state = "running"
			return nil, nil
		case "inspect":
			return json.Marshal(identity{ID: strings.Repeat("b", 64), Image: f.p.image.ImageID, Labels: f.p.manifest.DockerLabels(), Status: f.state, Running: f.state == "running", RestartPolicy: "no"})
		}
	}
	f.t.Fatal("unexpected Docker command", args)
	return nil, ErrLaunch
}
func TestLaunchRequiresPersistedBindingAndCannotRestart(t *testing.T) {
	p, root, w := launchFixture(t, "spool")
	f := &launchDocker{t: t, p: p, daemon: p.image.DaemonID, volumes: "null"}
	c := &Client{run: f.run}
	b, err := c.Create(context.Background(), p)
	if err != nil {
		t.Fatal(err)
	}
	if err = c.StartCreated(context.Background(), root, b, p); err == nil || f.started != 0 {
		t.Fatal("started without binding")
	}
	if err = w.SaveDockerBinding(b); err != nil {
		t.Fatal(err)
	}
	if err = c.StartCreated(context.Background(), root, b, p); err != nil {
		t.Fatal(err)
	}
	if err = c.StartCreated(context.Background(), root, b, p); err == nil || f.started != 1 {
		t.Fatal("restarted existing container")
	}
}
func TestLaunchRejectsMutationAndUncertainCreation(t *testing.T) {
	for _, kind := range []string{"mount changed", "policy changed", "daemon changed", "image volume", "lost reply"} {
		t.Run(kind, func(t *testing.T) {
			p, _, _ := launchFixture(t, "spool")
			f := &launchDocker{t: t, p: p, daemon: p.image.DaemonID, volumes: "{}"}
			c := &Client{run: f.run}
			switch kind {
			case "mount changed":
				path := filepath.Join(p.tree.Directory(), "input", "system-prompt.txt")
				_ = os.Chmod(path, 0644)
				_ = os.WriteFile(path, []byte("changed"), 0444)
			case "policy changed":
				_ = os.WriteFile(filepath.Join(p.directory, "startup-seccomp.json"), []byte(`{}`), 0600)
			case "daemon changed":
				f.daemon = "other"
			case "image volume":
				f.volumes = `{"/escape":{}}`
			case "lost reply":
				f.createErr = true
			}
			b, err := c.Create(context.Background(), p)
			if err == nil || b.DockerContainerID != "" || f.started != 0 {
				t.Fatal("failed open", b, err)
			}
			want := 0
			if kind == "lost reply" {
				want = 1
			}
			if f.created != want {
				t.Fatal("unexpected create/retry", f.created)
			}
		})
	}
}
func TestLaunchPolicyMountsAndSyscalls(t *testing.T) {
	for _, mode := range []string{"fifo", "spool"} {
		t.Run(mode, func(t *testing.T) {
			p, _, _ := launchFixture(t, mode)
			joined := strings.Join(p.args, " ")
			for _, must := range []string{"--pull=never", "--network=none", "--read-only", "--cap-drop=ALL", "--security-opt=no-new-privileges:true", "--restart=no", "--no-healthcheck", "--pids-limit=16", "nodev,nosuid,noexec", "bind-recursive=disabled", "bind-propagation=rprivate", "--entrypoint=/usr/bin/python3", p.image.ImageID + " -I -S -B /opt/operator/engine/bootstrap.py"} {
				if !strings.Contains(joined, must) {
					t.Error("missing", must)
				}
			}
			mounts := 0
			writable := 0
			for i, arg := range p.args {
				if arg == "--mount" {
					mounts++
					if !strings.HasSuffix(p.args[i+1], ",readonly") {
						writable++
						if !strings.Contains(p.args[i+1], "-out,") {
							t.Error("unexpected writable bind", p.args[i+1])
						}
					}
				}
			}
			if mode == "fifo" && (mounts != 4 || writable != 0) || mode == "spool" && (mounts != 7 || writable != 2) {
				t.Fatal(mounts, writable)
			}
		})
	}
	var policy struct {
		DefaultAction string
		Syscalls      []struct {
			Names  []string
			Action string
		}
	}
	if json.Unmarshal(startupSeccomp, &policy) != nil || policy.DefaultAction != "SCMP_ACT_ERRNO" {
		t.Fatal("policy")
	}
	for _, rule := range policy.Syscalls {
		for _, name := range rule.Names {
			for _, denied := range []string{"socket", "socketpair", "connect", "clone", "clone3", "fork", "vfork", "mount", "unshare", "setns", "ptrace", "bpf", "perf_event_open"} {
				if name == denied {
					t.Fatal("allowed", name)
				}
			}
		}
	}
}
func TestDockerEventsNeverReconnectOrIgnoreExit(t *testing.T) {
	id := strings.Repeat("b", 64)
	for _, stream := range []string{"", `{"id":"wrong","action":"start"}`, `{"id":"` + id + `","action":"die"}`, `{"id":"` + id + `","action":"start"}` + "\n", strings.Repeat("x", outputLimit+1)} {
		if err := readEvents(context.Background(), strings.NewReader(stream), id); !errors.Is(err, ErrEvents) {
			t.Fatal(err)
		}
	}
}
