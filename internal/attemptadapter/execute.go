package attemptadapter

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"sync"

	"github.com/intrusive-ai/operator-sandbox/contracts"
	"github.com/intrusive-ai/operator-sandbox/internal/campaign"
	"github.com/intrusive-ai/operator-sandbox/internal/feedback"
	"github.com/intrusive-ai/operator-sandbox/internal/interceptor"
	"github.com/intrusive-ai/operator-sandbox/internal/nativeexec"
)

type InjectionHandle struct {
	CampaignID  string `json:"campaign_id"`
	SessionID   string `json:"session_id"`
	AttemptID   string `json:"attempt_id"`
	ReceiptID   string `json:"receipt_id"`
	ActionID    string `json:"action_id"`
	InjectionID string `json:"injection_id"`
	Deleted     bool   `json:"deleted"`
}
type Result struct {
	// These values must be committed together with receipt contents before any
	// harness reply. Native steps are already durable; this is not publication.
	GuestJSON  []byte
	Receipt    *feedback.Receipt
	Injections []InjectionHandle
	StepIDs    []string
}
type Execution struct {
	mu       sync.Mutex
	started  bool
	plan     *Plan
	attempts *campaign.Attempts
	executor *nativeexec.Executor
	adapter  *stepAdapter
}
type stepAdapter struct {
	plan    *Plan
	active  interceptor.PreparedOperation
	command command
	source  feedback.Source
	entry   *interceptor.FeedbackEntry
	turn    interceptor.Turn
}

func (p *Plan) NewExecution(attempts *campaign.Attempts, guard *nativeexec.Guard) (*Execution, error) {
	if attempts == nil {
		return nil, ErrAttempt
	}
	saved, _, err := attempts.Lookup(p.request.RequestID)
	digest, digestErr := contracts.CanonicalDigest(p.record, contracts.OrdinaryLimit)
	if err != nil || digestErr != nil || saved.PlanDigest != digest || !saved.Dispatched || saved.State != "admitted" || saved.AttemptID != p.request.AttemptID {
		return nil, ErrAttempt
	}
	if saved.Target.Adapter != "interceptor/v1" || saved.Target.SessionID != p.binding.SessionID || saved.Target.CapabilitySourceDigest != p.sourceDigest || saved.Target.CapabilityProjectionDigest != p.projectionDigest || saved.Target.NativeFeedbackProfile != p.context.FeedbackProfile || saved.SubmittedRevision != int64(p.binding.RunRevision) {
		return nil, ErrAttempt
	}
	// Observe resolves this existing identity before any execution checks. A
	// mismatched body cannot borrow an admitted plan under the same request ID.
	if _, replay, err := attempts.Observe(campaign.AttemptInput{CampaignID: p.context.CampaignID, WorkerInstanceID: p.binding.WorkerInstanceID, RunRevision: int64(p.binding.RunRevision), Body: p.rawRequest}); err != nil || !replay {
		return nil, ErrAttempt
	}
	a := &stepAdapter{plan: p}
	executor, err := nativeexec.New(attempts.NativeSteps(), guard, a)
	if err != nil {
		return nil, err
	}
	return &Execution{plan: p, attempts: attempts, executor: executor, adapter: a}, nil
}
func (a *stepAdapter) Authorize(p interceptor.PreparedOperation) error {
	if len(a.active.Bytes()) == 0 || !bytes.Equal(a.active.Bytes(), p.Bytes()) {
		return ErrPolicy
	}
	return nil
}
func (a *stepAdapter) Interpret(p interceptor.PreparedOperation, r interceptor.Response) (nativeexec.Outcome, error) {
	if a.Authorize(p) != nil {
		return nativeexec.Unknown, ErrResult
	}
	if r.Status >= 400 && r.Status < 500 {
		return nativeexec.Failed, nil
	}
	q := p.Request()
	if r.SessionRevision < q.ExpectedSessionRevision {
		return nativeexec.Unknown, ErrResult
	}
	decode := func(dst any) bool { return interceptor.DecodeTypedBody(r.Body, dst, interceptor.JSONLimit) == nil }
	valid := false
	switch a.command.Operation {
	case "artifact.register":
		var expected struct {
			Descriptor interceptor.ArtifactDescriptor `json:"descriptor"`
		}
		_ = json.Unmarshal(a.command.Body, &expected)
		var actual interceptor.ArtifactDescriptor
		valid = (r.Status == 200 || r.Status == 201) && decode(&actual) && actual == expected.Descriptor
	case "attempt.register":
		var actual interceptor.AttemptContext
		valid = r.Status == 201 && decode(&actual) && reflect.DeepEqual(actual, a.plan.context) && actual.Digest == interceptor.AttemptContextDigest(actual)
	case "injection.arm":
		var actual, expected struct {
			Definition interceptor.Definition `json:"definition"`
		}
		_ = json.Unmarshal(a.command.Body, &expected)
		valid = r.Status == 201 && decode(&actual) && reflect.DeepEqual(actual, expected)
	case "application.invoke":
		var actual interceptor.Turn
		valid = r.Status == 200 && decode(&actual) && identifier.MatchString(actual.ID) && actual.Operation == a.plan.request.Invocation.OperationID && actual.AttemptID == q.AttemptID && actual.PayloadDigest == a.plan.request.Payload.Digest && !actual.Started.IsZero() && !actual.Finished.Before(actual.Started) && slices.Contains([]string{"complete", "error"}, actual.Status)
		if valid && actual.OutputDigest != "" && contracts.RawDigest(actual.Body) != actual.OutputDigest {
			valid = false
		}
		if valid && actual.Status == "error" {
			return nativeexec.Failed, nil
		}
	case "observation.read":
		view, err := feedback.VerifyView(r.Body, a.source, a.plan.policy)
		valid = r.Status == 200 && r.SessionRevision == a.source.SessionRevision && err == nil && view.Operation.State == "SUCCEEDED" && view.Operation.ResponseCode == a.turn.Code && reflect.DeepEqual(view.Operation.ExitCode, a.turn.ExitCode)
	case "observation.content.read":
		var actual interceptor.FeedbackChunk
		var query interceptor.FeedbackReadRequest
		_ = json.Unmarshal(a.command.Body, &query)
		valid = r.Status == 200 && r.SessionRevision == a.source.SessionRevision && decode(&actual) && a.entry != nil && actual.ReceiptID == query.ReceiptID && actual.Entry.ID == query.EntryID && actual.Offset == query.Offset && actual.RawLength == len(actual.Content) && actual.RawLength <= query.MaxBytes
		if valid {
			expected := *a.entry
			if actual.Entry.Availability == "unavailable" {
				expected.Availability = "unavailable"
				expected.Reason = actual.Entry.Reason
				valid = reflect.DeepEqual(actual.Entry, expected) && actual.RawLength == 0 && !actual.EOF
			} else {
				valid = reflect.DeepEqual(actual.Entry, expected) && actual.Offset+int64(actual.RawLength) <= expected.Artifact.SizeBytes && actual.EOF == (actual.Offset+int64(actual.RawLength) == expected.Artifact.SizeBytes) && (actual.RawLength > 0 || actual.EOF)
			}
		}
	case "injection.delete":
		valid = r.Status == 204 && (len(r.Body) == 0 || bytes.Equal(r.Body, []byte("null")))
	}
	if a.command.Operation != "observation.read" && a.command.Operation != "observation.content.read" && !(a.command.Operation == "artifact.register" && r.Status == 200) && r.SessionRevision <= q.ExpectedSessionRevision {
		valid = false
	}
	if !valid {
		return nativeexec.Unknown, ErrResult
	}
	return nativeexec.Succeeded, nil
}

// Run is single-use, including after failure. It executes only this plan through
// durable native steps; malformed/failed/uncertain results stop the sequence.
// It never retries or resumes a lost host process. Known-created injection handles
// remain in Result for the terminal cleanup controller or later retained cleanup.
func (e *Execution) Run(ctx context.Context) (result Result, runErr error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.started {
		return result, ErrAttempt
	}
	e.started = true
	p := e.plan
	receiptID := opaque("receipt", p.context.CampaignID, p.request.AttemptID)
	revision := p.revision
	index := 0
	stage := "delivery"
	invocation := "not-dispatched"
	cleanup := "not-needed"
	contact := "none"
	lastOutcome := ""
	result.Injections = []InjectionHandle{}
	result.StepIDs = []string{}
	defer func() {
		if runErr != nil {
			e.attempts.NativeSteps().Fence().Stop(runErr)
		}
		status, retry, code, message := "completed", "do-not-retry", "", ""
		if runErr != nil {
			// Journal failures can occur after dispatch but before a result record.
			// Only the executor's explicit known-failure outcome proves otherwise.
			status, retry, code, message = "unknown", "host-reconciliation-required", "OUTCOME_UNKNOWN", "Target outcome could not be verified; campaign execution is closed."
			if errors.Is(runErr, nativeexec.ErrFailed) {
				status, retry, code, message = "failed", "do-not-retry", "DELIVERY_FAILED", "Target execution failed; campaign execution is closed."
			}
			if stage == "observation" && status == "failed" {
				code, message = "OBSERVATION_UNAVAILABLE", "Feedback collection failed; campaign execution is closed."
			}
			if stage == "cleanup" && status == "failed" {
				code, message = "CLEANUP_FAILED", "Injection cleanup failed; campaign execution is closed."
			}
		} else {
			stage = "complete"
		}
		body := map[string]any{"api_version": "operator.dev/engine-attempt-result/v1alpha2", "kind": "EngineAttemptResult", "request_id": p.request.RequestID, "attempt_id": p.request.AttemptID, "receipt_id": receiptID, "status": status, "stage": stage, "target_contact": contact, "invocation_state": invocation, "cleanup_state": cleanup, "retry_disposition": retry, "errors": []any{}}
		if code != "" {
			body["errors"] = []any{map[string]string{"code": code, "instance_path": "", "message": message}}
		}
		if result.Receipt != nil {
			body["feedback"] = json.RawMessage(result.Receipt.ManifestJSON())
		}
		raw, err := json.Marshal(body)
		if err == nil {
			_, err = p.catalog.Validate(contracts.EngineAttemptResultSchema, raw, contracts.OrdinaryLimit)
		}
		if err != nil {
			runErr = err
			e.attempts.NativeSteps().Fence().Stop(err)
			return
		}
		result.GuestJSON = raw
	}()
	send := func(c command) (interceptor.Response, error) {
		lastOutcome = ""
		index++
		id := opaque("step", p.context.CampaignID, p.request.AttemptID, fmt.Sprint(index))
		q := interceptor.OperationRequest{RequestID: id, OperationID: id, Operation: c.Operation, CampaignID: p.context.CampaignID, SessionID: p.binding.SessionID, WorkerInstanceID: p.binding.WorkerInstanceID, RunRevision: p.binding.RunRevision, ExpectedSessionRevision: revision, AttemptID: p.request.AttemptID, AttemptContextDigest: p.context.Digest, Deadline: p.deadline}
		prepared, err := interceptor.PrepareOperation(q, c.Body)
		if err != nil {
			return interceptor.Response{}, err
		}
		e.adapter.active, e.adapter.command = prepared, c
		r, err := e.executor.Execute(ctx, p.request.RequestID, prepared)
		lastOutcome = r.Step.Outcome
		if r.Step.ID != "" {
			result.StepIDs = append(result.StepIDs, r.Step.ID)
		}
		if r.Step.State == campaign.ResultCommitted && r.Step.Outcome != string(nativeexec.NotDispatched) {
			contact = "attempted"
		}
		if err != nil {
			if lastOutcome != string(nativeexec.Failed) && lastOutcome != string(nativeexec.NotDispatched) {
				contact = "unknown"
			}
			return interceptor.Response{}, err
		}
		if r.Native == nil {
			return interceptor.Response{}, ErrResult
		}
		revision = r.Native.SessionRevision
		return *r.Native, nil
	}
	var turn interceptor.Turn
	for _, cmd := range p.commands {
		r, err := send(cmd)
		if err != nil {
			if cmd.Operation == "application.invoke" {
				invocation = "unknown"
				if lastOutcome == string(nativeexec.Failed) {
					invocation = "failed"
				}
				if lastOutcome == string(nativeexec.NotDispatched) {
					invocation = "not-dispatched"
				}
			}
			return result, err
		}
		if cmd.Operation == "injection.arm" {
			var response struct {
				Definition interceptor.Definition `json:"definition"`
			}
			_ = json.Unmarshal(r.Body, &response)
			d := response.Definition
			result.Injections = append(result.Injections, InjectionHandle{CampaignID: p.context.CampaignID, SessionID: p.binding.SessionID, AttemptID: p.request.AttemptID, ReceiptID: receiptID, ActionID: p.actions[d.ID], InjectionID: d.ID})
			cleanup = "not-requested"
		}
		if cmd.Operation == "application.invoke" {
			invocation = "succeeded"
			_ = json.Unmarshal(r.Body, &turn)
		}
	}
	stage = "observation"
	source := feedback.Source{CampaignID: p.context.CampaignID, SessionID: p.binding.SessionID, AttemptID: p.request.AttemptID, AttemptContextDigest: p.context.Digest, TurnID: turn.ID, RunRevision: p.binding.RunRevision, SessionRevision: revision}
	e.adapter.source = source
	e.adapter.turn = turn
	var observation []byte
	content := map[string][]byte{}
	remaining := p.feedbackBytes
	if p.policy.Collect() {
		body, _ := json.Marshal(map[string]string{"turn_id": turn.ID})
		r, err := send(command{"observation.read", body})
		if err != nil {
			return result, err
		}
		observation = r.Body
		v, err := feedback.VerifyView(observation, source, p.policy)
		if err != nil {
			return result, err
		}
		for _, entry := range v.Entries {
			if !slices.Contains(p.policy.NativeSelection().Kinds, entry.Kind) || (entry.Visibility != interceptor.TargetVisible && entry.Visibility != interceptor.HarnessVisible) || entry.Availability != "available" || entry.Artifact == nil || entry.Artifact.SizeBytes > remaining {
				continue
			}
			remaining -= entry.Artifact.SizeBytes
			assembly, err := feedback.NewAssembly(v.ReceiptID, entry)
			if err != nil {
				return result, err
			}
			e.adapter.entry = &entry
			for {
				query, err := assembly.Next(feedback.MaxChunk)
				if err != nil {
					return result, err
				}
				body, _ := json.Marshal(query)
				r, err := send(command{"observation.content.read", body})
				if err != nil {
					return result, err
				}
				var chunk interceptor.FeedbackChunk
				_ = json.Unmarshal(r.Body, &chunk)
				if chunk.Entry.Availability == "unavailable" {
					break
				}
				if err = assembly.Accept(query, r.Body); err != nil {
					return result, err
				}
				if chunk.EOF {
					content[entry.ID], err = assembly.Bytes()
					if err != nil {
						return result, err
					}
					break
				}
			}
		}
	}
	var err error
	result.Receipt, err = feedback.Project(p.catalog, p.policy, source, receiptID, observation, content, p.actions, p.feedbackBytes)
	if err != nil {
		return result, err
	}
	if p.request.Cleanup.Delete && len(p.cleanup) > 0 {
		stage = "cleanup"
		for i, cmd := range p.cleanup {
			if _, err = send(cmd); err != nil {
				cleanup = "unknown"
				if lastOutcome == string(nativeexec.Failed) || lastOutcome == string(nativeexec.NotDispatched) {
					cleanup = "failed"
				}
				return result, err
			}
			result.Injections[i].Deleted = true
		}
		cleanup = "completed"
	}
	return result, nil
}
