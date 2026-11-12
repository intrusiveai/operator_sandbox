package interceptor

import (
	"context"
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/intrusiveai/operator_sandbox/internal/contractcheck"
)

func TestInterceptorContractModels(t *testing.T) {
	d, err := contractcheck.Load("openapi.json")
	if err != nil {
		t.Fatal(err)
	}
	for name, model := range map[string]any{
		"ContractIdentity": ContractIdentity{}, "adaptive.OperationRequest": OperationRequest{},
		"adaptive.AttemptContext": AttemptContext{}, "adaptive.ArtifactDescriptor": ArtifactDescriptor{},
		"adaptive.ObservationView": ObservationView{},
		"adaptive.FeedbackChunk":   FeedbackChunk{}, "adaptive.FeedbackReadRequest": FeedbackReadRequest{},
		"adaptive.ExecutionOwner": Owner{}, "adaptive.OperationRecord": OperationRecord{},
		"injection.Definition": Definition{}, "sandboxd.TurnRequest": TurnRequest{}, "sandboxd.Turn": Turn{},
		"snapshot.Checkpoint": Checkpoint{}, "localapi.Binding": Binding{}, "localapi.ExecutionFailure": Failure{},
		"StatusBody": Status{}, "SnapshotCreate": SnapshotCreate{},
		"snapshot.Manifest": evidenceManifest{}, "snapshot.Entry": evidenceEntry{},
		"statejournal.Operation": evidenceOperation{}, "statejournal.Transaction": evidenceTransaction{},
	} {
		t.Run(name, func(t *testing.T) {
			if err := d.Compare(reflect.TypeOf(model), "#/components/schemas/"+name, false); err != nil {
				t.Fatal(err)
			}
		})
	}
	if err := d.Compare(reflect.TypeFor[Session](), "#/components/schemas/session.Metadata", true); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"LifecycleStopRequest", "LifecycleRestoreRequest"} {
		if err := d.Compare(reflect.TypeFor[LifecycleRequest](), "#/components/schemas/"+name, false); err != nil {
			t.Fatal(err)
		}
	}
}

// Read the production Go route literals and native-operation registry, not a
// second list claiming support. Every documented route/dispatch must be covered.
func TestInterceptorContractInventory(t *testing.T) {
	d, err := contractcheck.Load("openapi.json")
	if err != nil {
		t.Fatal(err)
	}
	actual := map[string]bool{}
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}
		tree, err := parser.ParseFile(token.NewFileSet(), file, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(tree, func(node ast.Node) bool {
			if lit, ok := node.(*ast.BasicLit); ok && lit.Kind == token.STRING {
				value, _ := strconv.Unquote(lit.Value)
				if strings.HasPrefix(value, "/v1/") {
					actual[value] = true
				}
			}
			return true
		})
	}
	paths := d.Value["paths"].(map[string]any)
	for path, raw := range paths {
		if !actual[path] {
			t.Errorf("contract route %s has no Operator integration", path)
		}
		item := raw.(map[string]any)
		for method := range item {
			if method != "post" {
				t.Errorf("unsupported HTTP method %s %s", method, path)
			}
		}
	}
	for path := range actual {
		if paths[path] == nil {
			t.Errorf("Operator route %s is not in contract", path)
		}
	}
	documented := map[string]bool{}
	for _, raw := range d.At("#/components/schemas/NativeRequest")["oneOf"].([]any) {
		variant := d.Expand(raw.(map[string]any))
		header := variant["properties"].(map[string]any)["request"].(map[string]any)
		name := header["properties"].(map[string]any)["operation"].(map[string]any)["const"].(string)
		documented[name] = true
		if !operations[name] {
			t.Errorf("contract operation %s is not supported", name)
		}
		if variant["x-response-schema"] == nil {
			t.Errorf("missing operation-specific response for %s", name)
		}
	}
	for name := range operations {
		if !documented[name] {
			t.Errorf("Operator operation %s is undocumented", name)
		}
	}
}

func TestInterceptorContractRequests(t *testing.T) {
	d, err := contractcheck.Load("openapi.json")
	if err != nil {
		t.Fatal(err)
	}
	var attempt AttemptContext
	raw, err := os.ReadFile("testdata/attempt-context.json")
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(raw, &attempt); err != nil {
		t.Fatal(err)
	}
	bodies := map[string]any{
		"session.owner":  map[string]string{"action": "close_execution", "session_id": "sess-1", "campaign_id": "campaign-1"},
		"session.status": map[string]any{}, "operation.status": map[string]string{"operation_id": "original-1"},
		"capabilities.read": map[string]any{},
		"artifact.register": struct {
			Descriptor ArtifactDescriptor `json:"descriptor"`
			Content    []byte             `json:"content"`
		}{ArtifactDescriptor{Digest: rawDigest([]byte("input")), SizeBytes: 5, MediaType: "text/plain"}, []byte("input")},
		"artifact.read":            map[string]string{"digest": rawDigest([]byte("input"))},
		"attempt.register":         attempt,
		"injection.arm":            map[string]any{"definition": Definition{ID: "injection-1", Surface: "model_tool_result", Mode: "simulation", Scope: "once", Enabled: true, Selector: Selector{ToolName: "tool", CallOrdinal: "any"}, Placement: Placement{Operation: "replace", Pointer: ""}, Payload: json.RawMessage(`"test"`)}},
		"injection.delete":         map[string]string{"injection_id": "injection-1"},
		"application.invoke":       TurnRequest{Operation: "invoke", Input: []byte(`{"query":"test"}`), MediaType: "application/json"},
		"observation.read":         map[string]any{"turn_id": "turn-1"},
		"observation.content.read": FeedbackReadRequest{ReceiptID: "feedback-1", EntryID: "entry-1", Offset: 0, MaxBytes: 1024},
		"snapshot.create":          SnapshotCreate{Label: "fixture", Description: "fixture", MaximumCommittedBytes: 1024},
	}
	for name, body := range bodies {
		t.Run(name, func(t *testing.T) {
			q := query()
			q.Operation = name
			q.AttemptID, q.AttemptContextDigest = attempt.AttemptID, attempt.Digest
			p, err := PrepareOperation(q, encoded(t, body))
			if err != nil {
				t.Fatal(err)
			}
			if err = d.Validate("#/components/schemas/NativeRequest", p.Bytes()); err != nil {
				t.Fatal(err)
			}
			c := &Client{http: &http.Client{Transport: roundTrip(func(req *http.Request) (*http.Response, error) {
				raw, _ := io.ReadAll(req.Body)
				if req.Method != "POST" || req.URL.Path != "/v1/operations" || string(raw) != string(p.Bytes()) {
					t.Fatal("wrong production dispatch")
				}
				response := wireResponse(t, 409, map[string]string{"code": "fixture_rejection"})
				data, _ := io.ReadAll(response.Body)
				if err := d.Validate("#/components/schemas/ErrorResponse", data); err != nil {
					t.Fatal(err)
				}
				response.Body = io.NopCloser(strings.NewReader(string(data)))
				return response, nil
			})}}
			r, err := c.Execute(context.Background(), p)
			if err != nil || r.Code() != "fixture_rejection" {
				t.Fatal(r, err)
			}
		})
	}
	for _, operation := range []string{"session.stop", "snapshot.restore"} {
		q := lifecycleQuery()
		q.Operation = operation
		if operation == "session.stop" {
			q.CheckpointID = ""
		}
		p, err := PrepareLifecycle(q, time.Now().Add(time.Minute))
		if err != nil {
			t.Fatal(err)
		}
		if err = d.Validate("#/paths/~1v1~1lifecycle/post/requestBody/content/application~1json/schema", p.Bytes()); err != nil {
			t.Fatal(err)
		}
	}
}

func TestInterceptorContractNativeResponseFixtures(t *testing.T) {
	d, err := contractcheck.Load("openapi.json")
	if err != nil {
		t.Fatal(err)
	}
	for file, schema := range map[string]string{"attempt-context.json": "adaptive.AttemptContext", "observation-view.json": "adaptive.ObservationView", "checkpoint-v1.json": "snapshot.Checkpoint", "checkpoint-legacy-v1.json": "snapshot.Checkpoint"} {
		raw, err := os.ReadFile("testdata/" + file)
		if err != nil {
			t.Fatal(err)
		}
		if err = d.Validate("#/components/schemas/"+schema, raw); err != nil {
			t.Fatalf("%s: %v", file, err)
		}
	}
}

// Interceptor persists an empty GatewayResponse when admitting an operation.
// Lookup returns that record while dispatch is still running. This must remain
// distinguishable from a completed response, including a 202 duplicate reply.
func TestInterceptorContractPendingOperation(t *testing.T) {
	d, err := contractcheck.Load("openapi.json")
	if err != nil {
		t.Fatal(err)
	}
	q := query()
	q.Operation = "injection.delete"
	p, err := PrepareOperation(q, []byte(`{"injection_id":"injection-1"}`))
	if err != nil {
		t.Fatal(err)
	}
	for _, state := range []string{"running", "completed", "unknown"} {
		t.Run(state, func(t *testing.T) {
			record := operationRecord(t, p, state)
			if state == "completed" {
				record.Response.Body = json.RawMessage("null")
			}
			raw := encoded(t, Response{Status: 200, Body: encoded(t, record), SessionRevision: 4})
			if err := d.Validate("#/components/schemas/OperationStatusResponse", raw); err != nil {
				t.Fatal(err)
			}
			r, err := decodeResponse(raw)
			if err != nil {
				t.Fatal(err)
			}
			got, err := DecodeOperationRecord(r, p)
			if err != nil || got.State != state {
				t.Fatal(got, err)
			}
		})
	}
}

func TestInterceptorContractPendingResponseIsNotCompletion(t *testing.T) {
	d, err := contractcheck.Load("openapi.json")
	if err != nil {
		t.Fatal(err)
	}
	p := savedOperation(t)
	for _, state := range []string{"completed", "unknown"} {
		record := operationRecord(t, p, state)
		record.Response = Response{}
		r := Response{Status: 200, Body: encoded(t, record), SessionRevision: 4}
		if err = d.Validate("#/components/schemas/OperationStatusResponse", encoded(t, r)); err == nil {
			t.Fatal("placeholder accepted in", state)
		}
		if _, err = DecodeOperationRecord(r, p); err == nil {
			t.Fatal("decoder accepted placeholder in", state)
		}
	}
	for _, schema := range []string{"NativeSuccessResponse", "adaptive.GatewayResponse"} {
		if err = d.Validate("#/components/schemas/"+schema, encoded(t, Response{})); err == nil {
			t.Fatal("placeholder became HTTP response", schema)
		}
	}
}

func completeNativeMetadata(m map[string]any) {
	for k, v := range map[string]any{
		"schema_version": 1, "app_image": "fixture:local", "generation": 1,
		"created_at": "2026-01-01T00:00:00Z", "updated_at": "2026-01-01T00:00:00Z",
		"limits":           map[string]any{"max_turns": 100, "max_tokens": 1000, "max_wall_seconds": 60, "max_snapshots": 10, "max_blob_bytes": 1048576, "max_pids": 64, "memory_bytes": 1048576, "cpus": "1"},
		"consumed":         map[string]int{"turns": 0, "tokens": 0, "snapshot_attempts": 0, "snapshot_bytes": 0},
		"bridge_transport": "file-spool", "taints": []string{},
	} {
		m[k] = v
	}
}

// Calls real public client methods, validates their HTTP requests and documented
// replies, and then lets the production decoders enforce their semantic checks.
func TestInterceptorContractHTTPRoutes(t *testing.T) {
	d, err := contractcheck.Load("openapi.json")
	if err != nil {
		t.Fatal(err)
	}
	attachment := attachmentBody(t)
	completeNativeMetadata(attachment["session"].(map[string]any))
	_, cp := checkpointFixture(t, false)
	seen := map[string]int{}
	c := &Client{http: &http.Client{Transport: roundTrip(func(req *http.Request) (*http.Response, error) {
		path := req.URL.Path
		seen[path]++
		root := "#/paths/" + contractcheck.Escape(path) + "/post"
		if req.Method != "POST" {
			t.Fatal("method", req.Method)
		}
		raw, err := io.ReadAll(req.Body)
		if err != nil {
			t.Fatal(err)
		}
		if err = d.Validate(root+"/requestBody/content/application~1json/schema", raw); err != nil {
			t.Fatal(path, err)
		}
		var body any
		switch path {
		case "/v1/contract-version":
			body = ContractIdentity{SupportedContractVersion, SupportedContractSHA256}
		case "/v1/attach":
			body = attachment
		case "/v1/status":
			body = Status{InstanceID: "instance-1", CampaignID: "campaign-1", Active: Binding{"sess-1", "worker-2", 2}, Sessions: map[string]Binding{"sess-1": {"sess-1", "worker-2", 2}}, Phase: "ready", StoreAvailable: true, EvidenceMaxBytes: 4 << 30}
		case "/v1/snapshots/list":
			body = map[string]any{"campaign_id": cp.CampaignID, "checkpoints": []Checkpoint{cp}, "total": 1}
		case "/v1/snapshots/inspect":
			body = cp
		case "/v1/lifecycle":
			var q LifecycleRequest
			if json.Unmarshal(raw, &q) != nil {
				t.Fatal("request")
			}
			if q.Operation == "session.stop" {
				body = StopResult{q.SessionID, "stopped"}
			} else {
				p := preparedLifecycle(t, q)
				_, _, restored := restoreFixture(t, p)
				completeNativeMetadata(restored["session"].(map[string]any))
				body = restored
			}
		case "/v1/evidence":
			if d.At(root + "/responses/200/content/application~1x-tar/schema")["format"] != "binary" {
				t.Fatal("evidence media contract changed")
			}
			if err = d.Validate(root+"/responses/200/headers/X-Content-SHA256/schema", encoded(t, rawDigest([]byte("archive")))); err != nil {
				t.Fatal(err)
			}
			if err = d.Validate(root+"/responses/200/headers/X-Evidence-Max-Bytes/schema", []byte("4294967296")); err != nil {
				t.Fatal(err)
			}
			return evidenceResponse([]byte("archive")), nil
		default:
			t.Fatal("untested route", path)
		}
		response := wireResponse(t, 200, body)
		data, _ := io.ReadAll(response.Body)
		if err = d.Validate(root+"/responses/200/content/application~1json/schema", data); err != nil {
			t.Fatal(path, err)
		}
		response.Body = io.NopCloser(strings.NewReader(string(data)))
		return response, nil
	})}}
	ctx := context.Background()
	if err = c.CheckContract(ctx); err != nil {
		t.Fatal(err)
	}
	a, err := c.Attach(ctx, "campaign-1", "worker-2", true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = c.Status(ctx, "campaign-1"); err != nil {
		t.Fatal(err)
	}
	if _, err = c.ListSnapshots(ctx, SnapshotListRequest{CampaignID: cp.CampaignID, Limit: 10}); err != nil {
		t.Fatal(err)
	}
	if _, err = c.InspectSnapshot(ctx, cp.CampaignID, cp.SourceSessionID, cp.ID); err != nil {
		t.Fatal(err)
	}
	for _, op := range []string{"snapshot.restore", "session.stop"} {
		q := lifecycleQuery()
		q.Operation = op
		if op == "session.stop" {
			q.CheckpointID = ""
		}
		p := preparedLifecycle(t, q)
		r, err := c.ExecuteLifecycle(ctx, p)
		if err != nil {
			t.Fatal(err)
		}
		if op == "session.stop" {
			_, err = DecodeStop(r, p)
		} else {
			_, err = DecodeRestore(r, p, a.Binding, a.Session)
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	download, err := c.DownloadEvidence(ctx, evidenceQuery(), privateEvidenceDir(t))
	if err != nil {
		t.Fatal(err)
	}
	reader, err := download.Reader()
	if err != nil {
		t.Fatal(err)
	}
	content, err := io.ReadAll(reader)
	if err != nil || string(content) != "archive" {
		t.Fatal("evidence transfer", err)
	}
	if err = download.Close(); err != nil {
		t.Fatal(err)
	}
	if len(seen) != 7 || seen["/v1/lifecycle"] != 2 {
		t.Fatal("missing route coverage", seen)
	}
	// /v1/operations is exercised separately with every native operation variant.
}

func TestInterceptorContractClosureAndCheckpointReceipts(t *testing.T) {
	d, err := contractcheck.Load("openapi.json")
	if err != nil {
		t.Fatal(err)
	}
	q := query()
	q.Operation = ""
	closeRequest, err := PrepareClose(q)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	closed := Response{Status: 200, SessionRevision: 5, Body: encoded(t, Owner{Principal: "operator", CampaignID: q.CampaignID, WorkerInstanceID: "worker-older", RunRevision: 1, BoundAt: now.Add(-time.Second), ClosedAt: &now, ClosureReason: "requested"})}
	if err = d.NativeExchange(closeRequest.Bytes(), encoded(t, closed)); err != nil {
		t.Fatal(err)
	}
	if _, err = DecodeClosure(closed, q.CampaignID); err != nil {
		t.Fatal(err)
	}
	raw, cp := checkpointFixture(t, false)
	q.SessionID = cp.SourceSessionID
	request, err := PrepareSnapshotCreate(q, SnapshotCreate{Label: cp.Label, Description: cp.Description, MaximumCommittedBytes: cp.CanonicalSizeBytes + 1})
	if err != nil {
		t.Fatal(err)
	}
	result := Response{Status: 201, SessionRevision: 5, Body: raw}
	if err = d.NativeExchange(request.Bytes(), encoded(t, result)); err != nil {
		t.Fatal(err)
	}
	if _, err = DecodeSnapshotCreated(result, request, Session{ID: cp.SourceSessionID, CampaignID: q.CampaignID, EnvironmentDigest: cp.EnvironmentDigest, AppDigest: cp.AppDigest}); err != nil {
		t.Fatal(err)
	}
}
