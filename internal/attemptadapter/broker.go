package attemptadapter

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"sync"
	"time"

	"github.com/intrusiveai/operator_sandbox/contracts"
	"github.com/intrusiveai/operator_sandbox/internal/campaign"
	"github.com/intrusiveai/operator_sandbox/internal/feedback"
	"github.com/intrusiveai/operator_sandbox/internal/interceptor"
	"github.com/intrusiveai/operator_sandbox/internal/nativeexec"
)

// BrokerConfig is installed host wiring. Prepare resolves verified artifacts,
// policy, lineage and live native revision and compiles a plan with this deadline.
// Cleanup must check current native capability/policy before supplying its target.
// These callbacks never come from a guest or a submitted scenario bundle.
type BrokerConfig struct {
	Catalog   *contracts.Catalog
	Protocol  *contracts.Protocol
	Writer    *campaign.Writer
	Attempts  *campaign.Attempts
	Deadline  time.Time
	Admitted  func() bool
	Prepare   func(context.Context, []byte, time.Time) (*Plan, *nativeexec.Guard, error)
	Cleanup   func(context.Context, time.Time) (CleanupTarget, error)
	ReadKinds func() []string
}
type CleanupTarget struct {
	Guard           *nativeexec.Guard
	Binding         interceptor.Binding
	SessionRevision uint64
}
type Broker struct {
	mu       sync.Mutex
	config   BrokerConfig
	manifest campaign.RunManifest
}

var ErrOperation = errors.New("operation belongs to another host broker route")

type wireRequest struct {
	Campaign  string          `json:"campaign_id"`
	Launch    string          `json:"launch_id"`
	Revision  int64           `json:"run_revision"`
	ID        string          `json:"operation_id"`
	Operation string          `json:"operation"`
	Timeout   int64           `json:"timeout_ms"`
	Body      json.RawMessage `json:"body"`
}
type Fault struct {
	Code        string `json:"code"`
	Message     string `json:"message"`
	Effect      string `json:"effect_state"`
	Disposition string `json:"disposition"`
}
type reply struct {
	Result json.RawMessage `json:"result,omitempty"`
	Error  *Fault          `json:"error,omitempty"`
}

func denied(code string) reply {
	return reply{Error: &Fault{code, "The operation was not admitted.", "none", "correct-and-resubmit"}}
}
func terminal(code, effect string) reply {
	return reply{Error: &Fault{code, "Campaign execution is closed.", effect, "terminate"}}
}
func NewBroker(c BrokerConfig) (*Broker, error) {
	if c.Catalog == nil || c.Protocol == nil || c.Writer == nil || c.Attempts == nil || !c.Attempts.BelongsTo(c.Writer) || c.Admitted == nil || c.Prepare == nil || c.Cleanup == nil || c.ReadKinds == nil || !c.Deadline.After(time.Now()) {
		return nil, ErrAttempt
	}
	return &Broker{config: c, manifest: c.Writer.Manifest()}, nil
}
func (b *Broker) stop(err error) error { b.config.Writer.Fence().Stop(err); return err }
func (b *Broker) envelope(raw []byte, seq int64, r reply) ([]byte, error) {
	var out map[string]any
	_ = json.Unmarshal(raw, &out)
	delete(out, "body")
	delete(out, "timeout_ms")
	out["kind"] = "response"
	out["seq"] = seq
	if r.Error != nil {
		out["error"] = r.Error
	} else {
		out["result"] = r.Result
	}
	encoded, err := json.Marshal(out)
	if err == nil {
		_, err = b.config.Protocol.ValidateResponse(raw, encoded)
	}
	if err != nil {
		return nil, b.stop(err)
	}
	return encoded, nil
}

// Handle is called only for messages captured by the admitted transport. It owns
// ordinary-operation serialization; the transport still owns sequence tracking,
// absolute receive deadlines and enqueue. Errors mean no reply is safe to send.
func (b *Broker) Handle(ctx context.Context, raw []byte, responseSequence int64) ([]byte, error) {
	if responseSequence < 0 || responseSequence > contracts.MaxSafeInteger {
		return nil, b.stop(contracts.ErrProtocol)
	}
	if _, err := b.config.Protocol.ValidateRequest(raw); err != nil {
		return nil, b.stop(err)
	}
	var q wireRequest
	canonical, err := contracts.Canonicalize(raw, contracts.OrdinaryLimit)
	if err != nil || json.Unmarshal(canonical, &q) != nil {
		return nil, b.stop(ErrAttempt)
	}
	if q.Campaign != b.manifest.CampaignID || q.Launch != b.manifest.LaunchID {
		return nil, b.stop(ErrPolicy)
	}
	if q.Operation != "engine.attempt_execute" && q.Operation != "engine.observation_read" && q.Operation != "engine.injection_delete" {
		return nil, ErrOperation
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.config.Admitted() {
		return nil, ErrPolicy
	}
	if err := b.config.Writer.Fence().Err(); err != nil {
		return nil, err
	}
	// Clamp before converting milliseconds to a duration; wire integers can be large.
	ceiling := int64(30000)
	for _, op := range b.config.Protocol.Operations() {
		if op.Name == q.Operation {
			ceiling = op.TimeoutMS
		}
	}
	deadline := time.Now().Add(time.Duration(min(q.Timeout, ceiling)) * time.Millisecond)
	if b.config.Deadline.Before(deadline) {
		deadline = b.config.Deadline
	}
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	if !deadline.After(time.Now()) {
		return nil, b.stop(context.DeadlineExceeded)
	}
	ctx, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()
	var r reply
	err = nil
	switch q.Operation {
	case "engine.attempt_execute":
		r, err = b.attempt(ctx, q, deadline)
	case "engine.observation_read", "engine.injection_delete":
		r, err = b.tool(ctx, q, deadline)
	}
	if err != nil {
		return nil, err
	}
	if ctx.Err() != nil {
		return nil, b.stop(ctx.Err())
	}
	return b.envelope(raw, responseSequence, r)
}
func (b *Broker) auditDenial(q wireRequest, r reply) error {
	metadata, _ := json.Marshal(map[string]any{"operation_id": q.ID, "operation": q.Operation, "run_revision": q.Revision, "error": r.Error})
	_, err := b.config.Writer.Append(campaign.Entry{RunRevision: b.config.Attempts.Status().RunRevision, Kind: "broker.denied", Metadata: metadata})
	if err != nil {
		return b.stop(err)
	}
	return nil
}
func (b *Broker) reject(q wireRequest, code string) (reply, error) {
	r := denied(code)
	return r, b.auditDenial(q, r)
}
func (b *Broker) attempt(ctx context.Context, q wireRequest, deadline time.Time) (reply, error) {
	a := b.config.Attempts
	target := a.Target()
	saved, replay, err := a.Observe(campaign.AttemptInput{CampaignID: q.Campaign, WorkerInstanceID: target.WorkerInstanceID, RunRevision: q.Revision, Body: q.Body})
	if err != nil {
		if b.config.Writer.Fence().Err() != nil {
			return reply{}, err
		}
		code := "IDEMPOTENCY_CONFLICT"
		if q.Revision != a.Status().RunRevision {
			code = "STATE_CHANGED"
		}
		return b.reject(q, code)
	}
	if replay {
		saved, result, err := a.Lookup(q.ID)
		if err != nil {
			return reply{}, err
		}
		if result == nil {
			return reply{}, b.stop(nativeexec.ErrPending)
		}
		if saved.State == "rejected" {
			var f Fault
			if json.Unmarshal(result, &f) != nil {
				return reply{}, b.stop(campaign.ErrCorrupt)
			}
			return reply{Error: &f}, nil
		}
		// An immutable old result is not permission to expose newly forbidden entries.
		var v struct {
			Feedback *feedback.Manifest `json:"feedback"`
		}
		_ = json.Unmarshal(result, &v)
		if v.Feedback != nil {
			for _, e := range v.Feedback.Entries {
				if !slices.Contains(b.config.ReadKinds(), e.Kind) {
					return b.reject(q, "POLICY_DENIED")
				}
			}
		}
		return reply{Result: result}, nil
	}
	reject := func(code string) (reply, error) {
		r := denied(code)
		body, _ := json.Marshal(r.Error)
		err := a.Resolve(q.ID, campaign.AttemptCompletion{Outcome: "rejected", Result: body, Receipts: json.RawMessage(`{}`)})
		return r, err
	}
	plan, guard, err := b.config.Prepare(ctx, q.Body, deadline)
	if err != nil {
		if errors.Is(err, ErrPolicy) {
			return reject("POLICY_DENIED")
		}
		return reject("INVALID_ARGUMENTS")
	}
	if plan == nil || guard == nil || plan.RequestID() != q.ID || plan.deadline.After(deadline) || !plan.deadline.After(time.Now()) || plan.binding.SessionID != saved.Target.SessionID || plan.binding.RunRevision != uint64(q.Revision) {
		return reply{}, b.stop(ErrAttempt)
	}
	if fresh, err := a.Admit(q.ID, plan.RecordJSON()); err != nil {
		if errors.Is(err, contracts.ErrLimit) {
			return reject("LIMIT_EXCEEDED")
		}
		return reply{}, err
	} else if !fresh {
		return reply{}, b.stop(nativeexec.ErrPending)
	}
	if err := a.ReservePublication(q.ID, plan.FeedbackAllowance()); err != nil {
		return reply{}, err
	}
	if fresh, err := a.MarkDispatched(q.ID); err != nil {
		return reply{}, err
	} else if !fresh {
		return reply{}, b.stop(nativeexec.ErrPending)
	}
	execution, err := plan.NewExecution(a, guard)
	if err != nil {
		return reply{}, b.stop(err)
	}
	result, runErr := execution.Run(ctx)
	if len(result.GuestJSON) == 0 {
		return reply{}, b.stop(runErr)
	}
	var outcome struct {
		Status string `json:"status"`
	}
	_ = json.Unmarshal(result.GuestJSON, &outcome)
	state := map[string]string{"completed": "succeeded", "failed": "failed", "unknown": "unknown"}[outcome.Status]
	if err = a.Publish(b.config.Catalog, q.ID, campaign.AttemptCompletion{Outcome: state, Result: result.GuestJSON}, campaign.Publication{Receipt: result.Receipt, Injections: result.Injections}); err != nil {
		return reply{}, b.stop(err)
	}
	return reply{Result: result.GuestJSON}, nil
}
func (b *Broker) tool(ctx context.Context, q wireRequest, deadline time.Time) (reply, error) {
	t := b.config.Attempts.Tools()
	target := b.config.Attempts.Target()
	_, replay, err := t.Observe(campaign.ToolInput{CampaignID: q.Campaign, OperationID: q.ID, Operation: q.Operation, WorkerInstanceID: target.WorkerInstanceID, RunRevision: q.Revision, Body: q.Body})
	if err != nil {
		if b.config.Writer.Fence().Err() != nil {
			return reply{}, err
		}
		code := "IDEMPOTENCY_CONFLICT"
		if errors.Is(err, contracts.ErrLimit) {
			code = "LIMIT_EXCEEDED"
		} else if !errors.Is(err, campaign.ErrConflict) && q.Revision != b.config.Attempts.Status().RunRevision {
			code = "STATE_CHANGED"
		}
		return b.reject(q, code)
	}
	if replay {
		_, raw, err := t.Lookup(q.ID)
		if err != nil {
			return reply{}, err
		}
		if raw == nil {
			return reply{}, b.stop(nativeexec.ErrPending)
		}
		var r reply
		if json.Unmarshal(raw, &r) != nil {
			return reply{}, b.stop(campaign.ErrCorrupt)
		}
		if q.Operation == "engine.observation_read" && r.Error == nil {
			if _, _, r := b.readRecord(q); r.Error != nil {
				return r, b.auditDenial(q, r)
			}
		}
		return r, nil
	}
	var r reply
	actual := int64(0)
	if q.Operation == "engine.observation_read" {
		r, actual, err = b.read(q)
	} else {
		r, err = b.cleanup(ctx, q, deadline)
	}
	if err != nil {
		return reply{}, err
	}
	payload, _ := json.Marshal(r)
	// Validate before retaining or delivering any result, including error variants.
	request := map[string]any{"api_version": "operator.dev/engine-pipe/v1alpha1", "kind": "request", "seq": 1, "campaign_id": q.Campaign, "launch_id": q.Launch, "run_revision": q.Revision, "call_id": "validation", "operation_id": q.ID, "operation": q.Operation, "timeout_ms": q.Timeout, "body": q.Body}
	check, _ := json.Marshal(request)
	if _, err = b.envelope(check, 1, r); err != nil {
		return reply{}, err
	}
	if err = t.Finish(q.ID, payload, actual); err != nil {
		return reply{}, err
	}
	return r, nil
}

type readQuery struct {
	Receipt string `json:"receipt_id"`
	Entry   string `json:"entry_id"`
	Offset  int64  `json:"offset"`
	Maximum int    `json:"max_bytes"`
}

func (b *Broker) readRecord(q wireRequest) (*feedback.Retained, campaign.PublicationIndex, reply) {
	var read readQuery
	_ = json.Unmarshal(q.Body, &read)
	index, err := b.config.Attempts.FindPublication(read.Receipt)
	if err != nil {
		if b.config.Writer.Fence().Err() != nil {
			return nil, index, terminal("OBSERVATION_INTEGRITY_FAILED", "none")
		}
		return nil, index, denied("OBSERVATION_NOT_FOUND")
	}
	if len(index.Feedback) == 0 {
		return nil, index, denied("OBSERVATION_NOT_FOUND")
	}
	record, err := feedback.OpenRecord(b.config.Catalog, index.Feedback)
	if err != nil {
		b.stop(err)
		return nil, index, terminal("OBSERVATION_INTEGRITY_FAILED", "none")
	}
	for _, entry := range record.Entries() {
		if entry.ID == read.Entry {
			if !slices.Contains(b.config.ReadKinds(), entry.Kind) {
				return nil, index, denied("OBSERVATION_NOT_PERMITTED")
			}
			if entry.Artifact != nil && read.Offset > entry.Artifact.SizeBytes {
				return nil, index, denied("OBSERVATION_RANGE_INVALID")
			}
			return record, index, reply{}
		}
	}
	return nil, index, denied("OBSERVATION_NOT_FOUND")
}
func (b *Broker) read(q wireRequest) (reply, int64, error) {
	var read readQuery
	_ = json.Unmarshal(q.Body, &read)
	if err := b.config.Attempts.Tools().ReserveRead(q.ID, int64(read.Maximum)); err != nil {
		if errors.Is(err, campaign.ErrFeedbackBudget) {
			return denied("FEEDBACK_BUDGET_EXCEEDED"), 0, nil
		}
		return reply{}, 0, err
	}
	record, index, r := b.readRecord(q)
	if r.Error != nil {
		return r, 0, nil
	}
	raw, err := record.Read(q.Campaign, read.Receipt, read.Entry, read.Offset, read.Maximum, true, b.config.ReadKinds(), func(id string, _ feedback.Artifact) ([]byte, error) {
		return b.config.Attempts.ReadPublicationObject(index, id)
	})
	if err != nil {
		return reply{}, 0, b.stop(err)
	}
	var result feedback.ReadResult
	_ = json.Unmarshal(raw, &result)
	return reply{Result: raw}, int64(result.RawLength), nil
}

// Rebind runs after verified restore, under the same gate as ordinary operations.
// The host source callbacks must subsequently supply the replacement binding.
func (b *Broker) Rebind(revision int64, target campaign.TargetBinding) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.config.Attempts.Rebind(revision, target)
}
