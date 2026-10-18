//go:build linux || darwin

package main

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/intrusiveai/operator_sandbox/contracts"
	"github.com/intrusiveai/operator_sandbox/internal/campaign"
	"github.com/intrusiveai/operator_sandbox/internal/contractstore"
	"github.com/intrusiveai/operator_sandbox/internal/hostconfig"
	"github.com/intrusiveai/operator_sandbox/internal/hostrun"
	"github.com/intrusiveai/operator_sandbox/internal/imagerelease"
	"github.com/intrusiveai/operator_sandbox/internal/modelprovider"
	"github.com/intrusiveai/operator_sandbox/internal/startrequest"
	"github.com/intrusiveai/operator_sandbox/internal/workerjob"
)

const diagnosticSecret = "SENSITIVE_TEST_RESPONSE_MUST_NOT_ESCAPE"

// The helper invokes the production CLI dispatcher in independent processes. Its
// Docker mode implements only read-only image inspection and stopped metadata
// containers; any launch, pull or unexpected command fails the test.
func TestPreparationDiagnosticProcess(t *testing.T) {
	if os.Getenv("OPERATOR_DIAGNOSTIC_PROCESS") != "1" {
		return
	}
	args := os.Args
	for len(args) > 0 && args[0] != "--" {
		args = args[1:]
	}
	if len(args) < 2 {
		os.Exit(98)
	}
	args = args[1:]
	if args[0] == "cli" {
		var defaults hostconfig.Paths
		if json.Unmarshal([]byte(os.Getenv("OPERATOR_DIAGNOSTIC_PATHS")), &defaults) != nil {
			os.Exit(98)
		}
		os.Exit(runWithDefaults(context.Background(), args[1:], os.Stdout, os.Stderr, defaults))
	}
	if args[0] != "docker" {
		os.Exit(98)
	}
	fixture := os.Getenv("OPERATOR_DIAGNOSTIC_FILES")
	a := args[1:]
	f, e := os.OpenFile(filepath.Join(fixture, "calls"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	if e != nil {
		os.Exit(98)
	}
	fmt.Fprintln(f, strings.Join(a, " "))
	f.Close()
	if os.Getenv("OPERATOR_DIAGNOSTIC_FAULT") == "image" {
		fmt.Fprintln(os.Stderr, diagnosticSecret)
		os.Exit(1)
	}
	if len(a) < 2 || a[0] != "--host" {
		os.Exit(98)
	}
	a = a[2:]
	if len(a) > 1 && a[0] == "--config" {
		a = a[2:]
	}
	id := strings.Repeat("a", 64)
	imageID := contracts.RawDigest([]byte("diagnostic-test-image"))
	switch {
	case len(a) > 0 && a[0] == "info":
		fmt.Println(`"diagnostic-daemon"`)
	case len(a) > 1 && a[0] == "image" && a[1] == "inspect":
		if os.Getenv("OPERATOR_DIAGNOSTIC_FAULT") == "image-missing" {
			fmt.Fprintln(os.Stderr, "No such image: "+diagnosticSecret)
			os.Exit(1)
		}
		if os.Getenv("OPERATOR_DIAGNOSTIC_FAULT") == "image-timeout" {
			time.Sleep(10 * time.Second)
			os.Exit(1)
		}
		if strings.Contains(strings.Join(a, " "), "Config.Volumes") {
			fmt.Println("null")
		} else {
			json.NewEncoder(os.Stdout).Encode(map[string]any{"id": imageID, "os": "linux", "architecture": runtime.GOARCH, "repo_digests": []string{}})
		}
	case len(a) > 1 && a[0] == "container" && a[1] == "create":
		label := ""
		for n, v := range a {
			if v == "--label" && n+1 < len(a) {
				label = a[n+1]
			}
		}
		if label == "" || os.WriteFile(filepath.Join(fixture, "label"), []byte(label), 0600) != nil {
			os.Exit(98)
		}
		fmt.Println(id)
	case len(a) > 1 && a[0] == "container" && a[1] == "inspect":
		label, e := os.ReadFile(filepath.Join(fixture, "label"))
		if e != nil {
			os.Exit(98)
		}
		key, value, _ := strings.Cut(string(label), "=")
		json.NewEncoder(os.Stdout).Encode(map[string]any{"id": id, "image": imageID, "labels": map[string]string{key: value}, "status": "created", "running": false, "paused": false, "restarting": false, "restart_policy": "no", "auto_remove": false})
	case len(a) > 1 && a[0] == "container" && a[1] == "cp":
		name := path.Base(a[2])
		raw, e := os.ReadFile(filepath.Join(fixture, name))
		if e != nil {
			os.Exit(98)
		}
		w := tar.NewWriter(os.Stdout)
		w.WriteHeader(&tar.Header{Name: name, Mode: 0444, Size: int64(len(raw)), Typeflag: tar.TypeReg})
		w.Write(raw)
		w.Close()
	case len(a) > 1 && a[0] == "container" && a[1] == "rm":
		fmt.Println(id)
	default:
		os.Exit(98)
	}
	os.Exit(0)
}

func diagnosticCLI(t *testing.T, paths hostconfig.Paths, args ...string) (int, []byte, string) {
	t.Helper()
	executable, e := os.Executable()
	if e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, executable, append([]string{"-test.run=^TestPreparationDiagnosticProcess$", "--", "cli"}, args...)...)
	raw, _ := json.Marshal(paths)
	command.Env = append(os.Environ(), "OPERATOR_DIAGNOSTIC_PROCESS=1", "OPERATOR_DIAGNOSTIC_PATHS="+string(raw), "GORACE=atexit_sleep_ms=0")
	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	err := command.Run()
	code := 0
	if err != nil {
		if command.ProcessState == nil {
			t.Fatal(err)
		}
		code = command.ProcessState.ExitCode()
	}
	if strings.Contains(stdout.String()+stderr.String(), diagnosticSecret) {
		t.Fatal("sensitive value exposed")
	}
	return code, stdout.Bytes(), stderr.String()
}

func diagnosticFixture(t *testing.T) (hostconfig.Paths, string) {
	t.Helper()
	paths, args := runFixture(t)
	c, e := hostconfig.Load(paths.ConfigFile, paths)
	if e != nil {
		t.Fatal(e)
	}
	pin := contracts.PackageIdentity{Version: c.Config.Contract.Version, Digest: c.Config.Contract.Digest}
	installed, e := contractstore.LoadRuntime(context.Background(), c.Config.Contract.Directory, pin)
	if e != nil {
		t.Fatal(e)
	}
	p := installed.Protocol()
	dir := t.TempDir()
	os.Chmod(dir, 0700)
	put := func(name string, raw []byte) {
		t.Helper()
		if e := os.WriteFile(filepath.Join(dir, name), raw, 0600); e != nil {
			t.Fatal(e)
		}
	}
	marshal := func(v any) []byte {
		t.Helper()
		raw, e := json.Marshal(v)
		if e != nil {
			t.Fatal(e)
		}
		return raw
	}
	operations := []string{}
	for _, operation := range p.Operations() {
		operations = append(operations, operation.Name)
	}
	tools := map[string]json.RawMessage{}
	for _, codec := range []string{"anthropic-messages-text-tools-v1", "bedrock-converse-text-tools-v1", "gemini-text-tools-v1", "openai-chat-text-tools-v1", "openai-responses-text-tools-v1"} {
		raw, e := p.ModelTools(codec, operations)
		if e != nil {
			t.Fatal(e)
		}
		tools[codec] = raw
	}
	files := map[string][]byte{"default-system-prompt.txt": []byte("Run the authorized test.\n"), "skill_loader.py": []byte("# test fixture, never executed\n"), "tool-catalog.json": marshal(map[string]any{"api_version": "operator.dev/model-tool-catalog/v1alpha1", "codecs": tools})}
	manifest := map[string]any{"api_version": "operator.dev/engine-manifest/v1alpha1", "contract": pin, "runtime_profile": "operator-container/v1", "platform": "linux/" + runtime.GOARCH, "entrypoint": []string{"/usr/bin/python3", "-I", "-S", "-B", "/opt/operator/engine/bootstrap.py"}, "transports": []string{"fifo", "spool"}}
	for field, name := range map[string]string{"prompt": "default-system-prompt.txt", "skill_loader": "skill_loader.py", "tool_catalog": "tool-catalog.json"} {
		manifest[field] = map[string]any{"size_bytes": len(files[name]), "digest": contracts.RawDigest(files[name])}
	}
	files["engine-manifest.json"] = marshal(manifest)
	for name, raw := range files {
		put(name, raw)
	}
	compatibility := marshal(imagerelease.Record{APIVersion: imagerelease.CompatibilityVersion, MinimumOperatorVersion: "0.1.0", ContractPackageVersion: pin.Version, ContractPackageDigest: pin.Digest, RuntimeProfile: "operator-container/v1", Platform: "linux/" + runtime.GOARCH})
	put("compatibility.json", compatibility)
	// An unauthenticated local model profile initializes without resolving credentials
	// or contacting a provider. Execution must never be reached in these tests.
	put("model.json", []byte(`{"api_version":"operator.dev/model-provider-profile/v1alpha1","id":"test","provider":"litellm","codec_id":"openai-chat-text-tools-v1","model":"test","endpoint":"https://127.0.0.1:4000/v1/chat/completions","authentication":"none","maximum_prompt_tokens":10000,"maximum_completion_tokens":100,"maximum_response_bytes":4096,"codec_options":{"instruction_role":"developer","response_models":["test"]}}`))
	executable, e := os.Executable()
	if e != nil {
		t.Fatal(e)
	}
	quote := func(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'" }
	put("docker", []byte("#!/bin/sh\nexec "+quote(executable)+" -test.run='^TestPreparationDiagnosticProcess$' -- docker \"$@\"\n"))
	if e := os.Chmod(filepath.Join(dir, "docker"), 0700); e != nil {
		t.Fatal(e)
	}
	original, e := os.ReadFile(paths.ConfigFile)
	if e != nil {
		t.Fatal(e)
	}
	writeConfig(t, paths.ConfigFile, string(original)+fmt.Sprintf("state: {root: %q}\ndocker: {executable: %q, endpoint: %q}\nmodel: {profile_file: %q}\n", paths.StateRoot, filepath.Join(dir, "docker"), paths.DockerEndpoint, filepath.Join(dir, "model.json")))
	t.Setenv("OPERATOR_DIAGNOSTIC_FILES", dir)
	code, raw, stderr := diagnosticCLI(t, paths, append([]string{"submit"}, args...)...)
	if code != 0 {
		t.Fatal("offline submission", code, string(raw), stderr)
	}
	return paths, args[len(args)-1]
}

func TestPreparationFailureCLIThenSeparateInspection(t *testing.T) {
	for _, fault := range []string{"image", "image-missing", "image-timeout", "attach"} {
		t.Run(fault, func(t *testing.T) {
			var requests atomic.Int64
			if fault == "attach" {
				listener, e := net.Listen("tcp4", "127.0.0.1:8080")
				if e != nil {
					t.Skip("controlled attachment test requires unused localhost port 8080:", e)
				}
				server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					requests.Add(1)
					if r.Method != "POST" || r.URL.Path != "/v1/attach" {
						t.Errorf("unexpected native request %s %s", r.Method, r.URL.Path)
					}
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(409)
					fmt.Fprintf(w, `{"status":409,"session_revision":0,"body":{"code":"conflict","message":%q}}`, diagnosticSecret)
				}))
				server.Listener.Close()
				server.Listener = listener
				server.Start()
				defer server.Close()
			}
			var foreground *hostrun.PreparationDiagnostic
			for _, mode := range []string{"foreground", "supervised"} {
				t.Run(mode, func(t *testing.T) {
					t.Setenv("OPERATOR_DIAGNOSTIC_FAULT", fault)
					paths, dir := diagnosticFixture(t)
					action := "start"
					if mode == "supervised" {
						action = "prepare"
					}
					code, raw, stderr := diagnosticCLI(t, paths, "campaign", action, "--run", dir)
					var receipt startReceipt
					if e := json.Unmarshal(raw, &receipt); e != nil {
						t.Fatal(e, string(raw), stderr)
					}
					var d *hostrun.PreparationDiagnostic
					if mode == "supervised" {
						if code != 0 {
							t.Fatal("prepare", code, stderr)
						}
						code, raw, stderr = diagnosticCLI(t, paths, "_worker", "--state-root", paths.StateRoot, "--request-id", receipt.StartRequestID, "--request-digest", receipt.RequestDigest)
						var result workerjob.Result
						if e := json.Unmarshal(raw, &result); e != nil {
							t.Fatal(e, string(raw), stderr)
						}
						d = result.Diagnostic
						if result.Code != "preparation_failed" || result.CompletionRecording != "recorded" {
							t.Fatal(result)
						}
					} else if receipt.Completion != nil {
						d = receipt.Completion.Diagnostic
					}
					if code != 1 || d == nil || !strings.Contains(stderr, d.Stage) || !strings.Contains(stderr, d.Message) || !strings.Contains(stderr, "preparation_failed") || !strings.Contains(stderr, "Execution will not resume.") {
						t.Fatal(code, string(raw), stderr, d)
					}
					if strings.HasPrefix(fault, "image") {
						expected := map[string]string{"image": "Docker daemon inspection: Docker command failed (exit 1)", "image-missing": "selected image inspection: image reference is not available in the local Docker store (exit 1)", "image-timeout": "selected image inspection: Docker command timed out"}[fault]
						if d.Stage != "local Harness image inspection" || !strings.Contains(d.Message, expected) {
							t.Fatal(d)
						}
					} else if d.Stage != "Interceptor attachment" || !strings.Contains(d.Message, "HTTP 409") {
						t.Fatal(d)
					}
					if mode == "foreground" {
						foreground = d
						t.Log("foreground output:\n" + stderr)
					} else if *foreground != *d {
						t.Fatal("worker mode diagnostics differ", foreground, d)
					}
					for _, args := range [][]string{{"inspect", "--run", dir}, {"campaign", "status", "--run", dir}} {
						code, raw, stderr = diagnosticCLI(t, paths, args...)
						var observed struct {
							Start *startReceipt `json:"start"`
						}
						if e := json.Unmarshal(raw, &observed); e != nil || code != 0 || observed.Start == nil || observed.Start.Completion == nil || observed.Start.Completion.Diagnostic == nil || *observed.Start.Completion.Diagnostic != *d {
							t.Fatal("separate inspection", code, string(raw), stderr, e)
						}
					}
					saved, e := startrequest.Read(paths.StateRoot, receipt.StartRequestID)
					if e != nil || saved.Claim == nil || saved.Accepted != nil || saved.Completion.Code != "preparation_failed" {
						t.Fatal(saved, e)
					}
					if _, e := os.Stat(filepath.Join(paths.StateRoot, "campaigns", receipt.CampaignID)); !os.IsNotExist(e) {
						t.Fatal("unexpected campaign execution", e)
					}
					intent := filepath.Join(paths.StateRoot, "attachments", receipt.CampaignID, "intent.json")
					_, e = os.Stat(intent)
					if (e == nil) != (fault == "attach") {
						t.Fatal("attachment intent ordering", e)
					}
					before, _ := os.ReadFile(filepath.Join(os.Getenv("OPERATOR_DIAGNOSTIC_FILES"), "calls"))
					code, _, _ = diagnosticCLI(t, paths, "campaign", "start", "--run", dir)
					after, _ := os.ReadFile(filepath.Join(os.Getenv("OPERATOR_DIAGNOSTIC_FILES"), "calls"))
					if code != 1 || !bytes.Equal(before, after) {
						t.Fatal("failed start replayed work")
					}
					// Inspect the complete saved completion, not only its projected output.
					completion, e := os.ReadFile(filepath.Join(paths.StateRoot, "starts", receipt.StartRequestID, "completion.json"))
					if e != nil {
						t.Fatal(e)
					}
					if bytes.Contains(completion, []byte(diagnosticSecret)) {
						t.Fatal("server body retained")
					}
					if fault == "attach" && !strings.Contains(string(after), "container rm") {
						t.Fatal("metadata inspection cleanup missing")
					}
				})
			}
			if fault == "attach" && requests.Load() != 2 {
				t.Fatal("attachment replayed or skipped", requests.Load())
			}
		})
	}
}

func TestImmediateDiagnosticWhenCompletionCannotBeSaved(t *testing.T) {
	dir, paths, deps, _ := startFixture(t)
	d := hostrun.PreparationDetails(hostrun.PreparationFailure("local Harness image and compatibility validation", imagerelease.ErrCompatibility))
	deps.run = func(context.Context, string, string, string) (workerjob.Result, error) {
		return workerjob.Result{Code: "preparation_failed", Diagnostic: d, CompletionRecording: "unconfirmed"}, imagerelease.ErrCompatibility
	}
	code, _, stderr := invokeStart(t, context.Background(), "start", []string{"--run", dir}, paths, deps)
	if code != 1 || !strings.Contains(stderr, "incompatible") || !strings.Contains(stderr, "saving the start completion is unconfirmed") {
		t.Fatal(code, stderr)
	}
	var worker bytes.Buffer
	printWorkerFailure(&worker, "preparation_failed", d, "unconfirmed")
	if stderr != worker.String() {
		t.Fatal("entrypoint formatting differs", stderr, worker.String())
	}
}

// Admission must reach the local target even when no approval cache exists and
// a release-service proxy would reject every request. Invalid local metadata must
// fail earlier, with the cause retained for a later CLI process.
func TestLocalCompatibilityWithoutReleaseService(t *testing.T) {
	var networkCalls atomic.Int64
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { networkCalls.Add(1); w.WriteHeader(503) }))
	defer proxy.Close()
	t.Setenv("HTTPS_PROXY", proxy.URL)
	t.Setenv("https_proxy", proxy.URL)
	t.Setenv("NO_PROXY", "")
	t.Setenv("no_proxy", "")
	for _, fault := range []string{"missing", "malformed", "minimum version", "contract", "platform", "resource digest"} {
		t.Run(fault, func(t *testing.T) {
			t.Setenv("OPERATOR_DIAGNOSTIC_FAULT", fault)
			paths, dir := diagnosticFixture(t)
			file := filepath.Join(os.Getenv("OPERATOR_DIAGNOSTIC_FILES"), "compatibility.json")
			raw, e := os.ReadFile(file)
			if e != nil {
				t.Fatal(e)
			}
			var record imagerelease.Record
			if e = json.Unmarshal(raw, &record); e != nil {
				t.Fatal(e)
			}
			want := "incompatible"
			switch fault {
			case "missing":
				e = os.Remove(file)
				want = "embedded image file read: Docker command failed (exit 98)"
			case "malformed":
				e = os.WriteFile(file, []byte(`{"unexpected":"`+diagnosticSecret+`"}`), 0600)
				want = "invalid embedded Harness compatibility record"
			case "minimum version":
				record.MinimumOperatorVersion = "999.0.0"
			case "contract":
				record.ContractPackageDigest = contracts.RawDigest([]byte("wrong package"))
			case "platform":
				if runtime.GOARCH == "arm64" {
					record.Platform = "linux/amd64"
				} else {
					record.Platform = "linux/arm64"
				}
			case "resource digest":
				e = os.WriteFile(filepath.Join(filepath.Dir(file), "default-system-prompt.txt"), []byte("changed prompt"), 0600)
				want = "embedded engine metadata"
			}
			if e != nil {
				t.Fatal(e)
			}
			if fault == "minimum version" || fault == "contract" || fault == "platform" {
				raw, _ = json.Marshal(record)
				if e = os.WriteFile(file, raw, 0600); e != nil {
					t.Fatal(e)
				}
			}
			code, raw, stderr := diagnosticCLI(t, paths, "campaign", "start", "--run", dir)
			var receipt startReceipt
			if e = json.Unmarshal(raw, &receipt); e != nil || code != 1 || receipt.Completion == nil || receipt.Completion.Diagnostic == nil || !strings.Contains(stderr, want) {
				t.Fatal(code, string(raw), stderr, e)
			}
			code, raw, stderr = diagnosticCLI(t, paths, "inspect", "--run", dir)
			var status struct {
				Start *startReceipt `json:"start"`
			}
			if e = json.Unmarshal(raw, &status); e != nil || code != 0 || status.Start == nil || status.Start.Completion == nil || status.Start.Completion.Diagnostic == nil || *status.Start.Completion.Diagnostic != *receipt.Completion.Diagnostic {
				t.Fatal("lost diagnostic", code, string(raw), stderr, e)
			}
			if _, e = os.Stat(filepath.Join(paths.StateRoot, "attachments", receipt.CampaignID)); !os.IsNotExist(e) {
				t.Fatal("attached incompatible image", e)
			}
			if _, e = os.Stat(filepath.Join(paths.StateRoot, "cache")); !os.IsNotExist(e) {
				t.Fatal("required or created a release cache", e)
			}
			calls, e := os.ReadFile(filepath.Join(os.Getenv("OPERATOR_DIAGNOSTIC_FILES"), "calls"))
			if e != nil || !bytes.Contains(calls, []byte("container rm")) {
				t.Fatal("inspection cleanup not confirmed", e)
			}
		})
	}
	// The original test checks both entrypoints and reaches an actual controlled
	// local attachment rejection after complete compatible-image validation.
	t.Run("compatible image", TestPreparationFailureCLIThenSeparateInspection)
	if networkCalls.Load() != 0 {
		t.Fatal("compatibility admission contacted network proxy", networkCalls.Load())
	}
}

func TestModelFailureForegroundAndWorkerOutput(t *testing.T) {
	dir, paths, deps, _ := startFixture(t)
	model := modelprovider.Diagnostic{Provider: "openai-chat", Model: "test-model", OperationID: "model-generate-0000000000000001", HTTPStatus: 401, ProviderCode: "invalid_api_key"}.WithFailure("http_rejection", "http_rejected")
	model.ProviderMessage = "The parameter temperature is unsupported."
	model.ProviderType = "invalid_request_error"
	model.ProviderParameter = "temperature"
	model.Omissions.RequestID = "not_provided"
	d := &hostrun.PreparationDiagnostic{Stage: "model request (http_rejection)", Message: model.Summary(), Model: model}
	deps.run = func(context.Context, string, string, string) (workerjob.Result, error) {
		return workerjob.Result{Code: "execution_failed", Diagnostic: d, CompletionRecording: "recorded"}, modelprovider.FailureError(model)
	}
	code, _, stderr := invokeStart(t, context.Background(), "start", []string{"--run", dir}, paths, deps)
	if code != 1 {
		t.Fatal(code, stderr)
	}
	for _, want := range []string{"Campaign execution failed during model request (http_rejection) (execution_failed)", "openai-chat returned HTTP 401 (invalid_api_key)", "Operation: model-generate-0000000000000001", "Model: test-model", `Provider message: "The parameter temperature is unsupported."`, `Provider type: "invalid_request_error"`, `Provider parameter: "temperature"`, "Provider request ID omission: not_provided", "Execution will not resume."} {
		if !strings.Contains(stderr, want) {
			t.Fatalf("missing %q: %s", want, stderr)
		}
	}
	var worker bytes.Buffer
	printWorkerFailure(&worker, "execution_failed", d, "recorded")
	if worker.String() != stderr {
		t.Fatal("foreground/supervised mismatch", worker.String(), stderr)
	}
	t.Log(stderr)
}

func TestConclusionFailureForegroundAndWorkerOutput(t *testing.T) {
	dir, paths, deps, _ := startFixture(t)
	detail := campaign.NewConclusionDiagnostic("conclusion-record", &contracts.ConclusionError{Reason: "unresolved_hypothesis", FieldPath: "/hypotheses/0/provenance/parent_hypothesis_id", Identifier: "hyp-missing"})
	d := &hostrun.PreparationDiagnostic{Stage: "conclusion validation (" + detail.Stage + ")", Message: detail.Message(), Conclusion: detail}
	deps.run = func(context.Context, string, string, string) (workerjob.Result, error) {
		return workerjob.Result{Code: "execution_failed", Diagnostic: d, CompletionRecording: "recorded"}, detail
	}
	code, _, stderr := invokeStart(t, context.Background(), "start", []string{"--run", dir}, paths, deps)
	if code != 1 {
		t.Fatal(code, stderr)
	}
	for _, want := range []string{"Campaign execution failed during conclusion validation (references) (execution_failed)", detail.Message(), "Operation: conclusion-record", "Validation rule: unresolved_hypothesis", "Execution will not resume."} {
		if !strings.Contains(stderr, want) {
			t.Fatalf("missing %q: %s", want, stderr)
		}
	}
	var supervised bytes.Buffer
	printWorkerFailure(&supervised, "execution_failed", d, "recorded")
	if supervised.String() != stderr {
		t.Fatal("foreground/supervised mismatch")
	}
	t.Log(stderr)
}
