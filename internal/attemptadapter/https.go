//go:build linux || darwin

package attemptadapter

import (
	"context"
	"encoding/json"
	"github.com/intrusiveai/operator_sandbox/contracts"
	"github.com/intrusiveai/operator_sandbox/internal/campaign"
	"github.com/intrusiveai/operator_sandbox/internal/capabilities"
	"github.com/intrusiveai/operator_sandbox/internal/feedback"
	"github.com/intrusiveai/operator_sandbox/internal/httpstarget"
	"github.com/intrusiveai/operator_sandbox/internal/interceptor"
	"slices"
)

type httpsExecution struct {
	mapping *httpstarget.Mapping
	client  *httpstarget.Client
	payload []byte
	media   string
}

func contextVersion(h *httpsExecution) string {
	if h != nil {
		return "operator.dev/https-attempt-context/v1alpha1"
	}
	return "interceptor.dev/attempt-context/v1alpha1"
}
func CompileHTTPS(c *contracts.Catalog, raw []byte, in Inputs, m *httpstarget.Mapping, client *httpstarget.Client) (*Plan, error) {
	if m == nil || client == nil || client.MappingDigest() != m.Digest() || in.Live == nil || in.Live.Export().Adapter() != httpstarget.Adapter {
		return nil, ErrAttempt
	}
	expected, e := capabilities.FromHTTPS(c, m, in.Live.Export().TargetID())
	if e != nil || expected.SourceDigest() != in.Live.Export().SourceDigest() {
		return nil, ErrPolicy
	}
	return compile(c, raw, in, &httpsExecution{mapping: m, client: client})
}
func compileHTTPSPlan(p *Plan, in Inputs, artifacts map[string][]byte) (*Plan, error) {
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
	if _, _, err := p.https.mapping.Request(r.Invocation.OperationID, payload, desc.MediaType); err != nil {
		return nil, ErrAttempt
	}
	p.https.payload = slices.Clone(payload)
	p.https.media = desc.MediaType
	p.record, _ = json.Marshal(map[string]any{"adapter": httpstarget.Adapter, "request_id": r.RequestID, "context": p.context, "operation_id": r.Invocation.OperationID, "input_digest": desc.Digest, "mapping_digest": p.https.mapping.Digest(), "capability_source_digest": p.sourceDigest, "capability_projection_digest": p.projectionDigest})
	return p, nil
}
func (p *Plan) runHTTPS(ctx context.Context, a *campaign.Attempts) (Result, error) {
	saved, _, err := a.Lookup(p.request.RequestID)
	digest, e := contracts.CanonicalDigest(p.record, contracts.OrdinaryLimit)
	if err != nil || e != nil || saved.PlanDigest != digest || !saved.Dispatched || saved.State != "admitted" || saved.AttemptID != p.request.AttemptID || saved.Target.Adapter != httpstarget.Adapter || saved.Target.SessionID != p.binding.SessionID || saved.Target.CapabilitySourceDigest != p.sourceDigest || saved.Target.CapabilityProjectionDigest != p.projectionDigest {
		return Result{}, ErrAttempt
	}
	ctx, cancel := context.WithDeadline(ctx, p.deadline)
	defer cancel()
	out := p.https.client.Execute(ctx, p.request.Invocation.OperationID, p.https.payload, p.https.media)
	receiptID := opaque("receipt", p.context.CampaignID, p.request.AttemptID)
	source := feedback.Source{CampaignID: p.context.CampaignID, SessionID: p.binding.SessionID, AttemptID: p.request.AttemptID, AttemptContextDigest: p.context.Digest, TurnID: opaque("https-request", p.context.CampaignID, p.request.AttemptID), RunRevision: p.binding.RunRevision, SessionRevision: 1}
	receipt, err := feedback.HTTPS(p.catalog, p.policy, source, receiptID, out.Content, out.MediaType, out.Code, out.Status == "completed", out.Truncated, p.feedbackBytes)
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
		errors = append(errors, map[string]string{"code": out.Code, "instance_path": "", "message": "The HTTPS operation did not produce complete permitted feedback."})
	}
	raw, err := json.Marshal(map[string]any{"api_version": "operator.dev/engine-attempt-result/v1alpha2", "kind": "EngineAttemptResult", "request_id": p.request.RequestID, "attempt_id": p.request.AttemptID, "receipt_id": receiptID, "status": out.Status, "stage": stage, "target_contact": out.Contact, "invocation_state": invocation, "cleanup_state": "not-needed", "retry_disposition": retry, "errors": errors, "feedback": json.RawMessage(receipt.ManifestJSON())})
	if err == nil {
		_, err = p.catalog.Validate(contracts.EngineAttemptResultSchema, raw, contracts.OrdinaryLimit)
	}
	return Result{GuestJSON: raw, Receipt: receipt}, err
}

func (p *Plan) HTTPSParent() Parent {
	raw, _ := json.Marshal(p.context)
	var ctx interceptor.AttemptContext
	_ = json.Unmarshal(raw, &ctx)
	return Parent{SessionID: p.binding.SessionID, Context: ctx, CampaignGeneration: ctx.Generation}
}
