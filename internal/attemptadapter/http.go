//go:build linux || darwin

package attemptadapter

import (
	"context"
	"encoding/json"
	"slices"
	"time"

	"github.com/intrusiveai/operator_sandbox/contracts"
	"github.com/intrusiveai/operator_sandbox/internal/campaign"
	"github.com/intrusiveai/operator_sandbox/internal/capabilities"
	"github.com/intrusiveai/operator_sandbox/internal/feedback"
	"github.com/intrusiveai/operator_sandbox/internal/httptarget"
	"github.com/intrusiveai/operator_sandbox/internal/interceptor"
)

type httpExecution struct {
	mapping *httptarget.Mapping
	client  *httptarget.Client
	payload []byte
	media   string
}

func contextVersion(h *httpExecution) string {
	if h != nil {
		return "operator.dev/http-attempt-context/v1alpha1"
	}
	return "interceptor.dev/attempt-context/v1alpha1"
}
func CompileHTTP(c *contracts.Catalog, raw []byte, in Inputs, m *httptarget.Mapping, client *httptarget.Client) (*Plan, error) {
	if m == nil || client == nil || client.MappingDigest() != m.Digest() || in.Live == nil || in.Live.Export().Adapter() != httptarget.Adapter {
		return nil, ErrAttempt
	}
	expected, e := capabilities.FromHTTP(c, m, in.Live.Export().TargetID())
	if e != nil || expected.SourceDigest() != in.Live.Export().SourceDigest() {
		return nil, ErrPolicy
	}
	return compile(c, raw, in, &httpExecution{mapping: m, client: client})
}
func compileHTTPPlan(p *Plan, in Inputs, artifacts map[string][]byte) (*Plan, error) {
	r := p.request
	if len(r.PreActions) > 0 || r.Invocation.CallerPrincipalID != "" || !slices.Contains(in.Policy.scopes.OperationIDs, r.Invocation.OperationID) {
		return nil, ErrPolicy
	}
	desc := r.Payload
	if r.Invocation.InputSource == "carrier" {
		if r.Carrier == nil {
			return nil, ErrAttempt
		}
		desc = *r.Carrier
	}
	if r.Invocation.MediaType != desc.MediaType {
		return nil, ErrAttempt
	}
	payload := artifacts[desc.Digest]
	if _, _, err := p.httpTarget.mapping.Request(r.Invocation.OperationID, payload, desc.MediaType); err != nil {
		return nil, ErrAttempt
	}
	p.httpTarget.payload = slices.Clone(payload)
	p.httpTarget.media = desc.MediaType
	p.record, _ = json.Marshal(map[string]any{"adapter": httptarget.Adapter, "request_id": r.RequestID, "context": p.context, "operation_id": r.Invocation.OperationID, "input_digest": desc.Digest, "mapping_digest": p.httpTarget.mapping.Digest(), "capability_source_digest": p.sourceDigest, "capability_projection_digest": p.projectionDigest})
	return p, nil
}
func (p *Plan) runHTTP(ctx context.Context, a *campaign.Attempts) (Result, error) {
	saved, _, err := a.Lookup(p.request.RequestID)
	digest, e := contracts.CanonicalDigest(p.record, contracts.OrdinaryLimit)
	if err != nil || e != nil || saved.PlanDigest != digest || !saved.Dispatched || saved.State != "admitted" || saved.AttemptID != p.request.AttemptID || saved.Target.Adapter != httptarget.Adapter || saved.Target.SessionID != p.binding.SessionID || saved.Target.CapabilitySourceDigest != p.sourceDigest || saved.Target.CapabilityProjectionDigest != p.projectionDigest {
		return Result{}, ErrAttempt
	}
	ctx, cancel := context.WithDeadline(ctx, p.deadline)
	defer cancel()
	out := p.httpTarget.client.Execute(ctx, p.request.Invocation.OperationID, p.httpTarget.payload, p.httpTarget.media)
	receiptID := opaque("receipt", p.context.CampaignID, p.request.AttemptID)
	source := feedback.Source{CampaignID: p.context.CampaignID, SessionID: p.binding.SessionID, AttemptID: p.request.AttemptID, AttemptContextDigest: p.context.Digest, TurnID: opaque("http-request", p.context.CampaignID, p.request.AttemptID), RunRevision: p.binding.RunRevision, SessionRevision: 1}
	receipt, err := feedback.HTTP(p.catalog, p.policy, source, receiptID, out.Content, out.MediaType, out.Code, out.Status == "completed", out.Truncated, p.feedbackBytes)
	if err != nil {
		return Result{}, err
	}
	invocation := "failed"
	stage := "observation"
	retry := "do-not-retry"
	if out.Status == "completed" {
		invocation = "succeeded"
		stage = "complete"
	} else if out.Status == "unknown" {
		invocation = "unknown"
		retry = "host-reconciliation-required"
	} else if out.Contact == "none" {
		invocation = "not-dispatched"
		stage = "delivery"
	}
	errors := []any{}
	if out.Code != "" {
		// The wire result uses the shared error vocabulary. Adapter-specific
		// diagnostics remain in profile-filtered operation_error feedback.
		code := "OBSERVATION_UNAVAILABLE"
		if out.Status == "unknown" {
			code = "OUTCOME_UNKNOWN"
		} else if out.Contact == "none" || out.Code == "HTTP_STATUS_FAILED" || out.Code == "HTTP_REDIRECT_REJECTED" {
			code = "DELIVERY_FAILED"
		}
		errors = append(errors, map[string]string{"code": code, "instance_path": "", "message": "The HTTP operation did not produce complete permitted feedback."})
	}
	body := map[string]any{"api_version": "operator.dev/engine-attempt-result/v1alpha2", "kind": "EngineAttemptResult", "request_id": p.request.RequestID, "attempt_id": p.request.AttemptID, "receipt_id": receiptID, "status": out.Status, "stage": stage, "target_contact": out.Contact, "invocation_state": invocation, "cleanup_state": "not-needed", "retry_disposition": retry, "errors": errors, "feedback": json.RawMessage(receipt.ManifestJSON())}
	if ctx.Err() == context.DeadlineExceeded || ctx.Err() == nil && !time.Now().Before(p.deadline) {
		timing := campaign.AttemptTimeout{NativeOperation: "http.invoke", AllowanceMS: p.allowanceMS, ElapsedMS: time.Since(p.started).Milliseconds(), DeadlineSource: p.deadlineSource, Observation: "not-started"}
		if timing.Valid() {
			body["timeout"] = timing
		}
	}
	raw, err := json.Marshal(body)
	if err == nil {
		_, err = p.catalog.Validate(contracts.EngineAttemptResultSchema, raw, contracts.OrdinaryLimit)
	}
	return Result{Integration: out.Failure, GuestJSON: raw, Receipt: receipt}, err
}

func (p *Plan) HTTPParent() Parent {
	raw, _ := json.Marshal(p.context)
	var ctx interceptor.AttemptContext
	_ = json.Unmarshal(raw, &ctx)
	return Parent{SessionID: p.binding.SessionID, Context: ctx, CampaignGeneration: ctx.Generation}
}
