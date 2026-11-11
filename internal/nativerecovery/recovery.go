//go:build linux || darwin

// Package nativerecovery finalizes a dead campaign without reconstructing a
// harness, execution writer, model route or mutable target preparation.
package nativerecovery

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/intrusiveai/operator_sandbox/internal/campaign"
	"github.com/intrusiveai/operator_sandbox/internal/diagnostictext"
	"github.com/intrusiveai/operator_sandbox/internal/interceptor"
	"github.com/intrusiveai/operator_sandbox/internal/termination"
)

type Peer interface {
	CheckContract(context.Context) error
	Status(context.Context, string) (interceptor.Status, error)
	Execute(context.Context, interceptor.PreparedOperation) (interceptor.Response, error)
	ExecuteLifecycle(context.Context, interceptor.PreparedLifecycle) (interceptor.Response, error)
}
type Outcome struct {
	Failures         []diagnostictext.Failure `json:"failures,omitempty"`
	CampaignID       string                   `json:"campaign_id"`
	ManifestDigest   string                   `json:"run_manifest_digest,omitempty"`
	AttachmentDigest string                   `json:"attachment_digest,omitempty"`
	State            string                   `json:"state"`
	Reason           string                   `json:"reason"`
	Closure          string                   `json:"closure"`
	Cleanup          string                   `json:"cleanup"`
	CleanupConfirmed int                      `json:"cleanup_confirmed"`
	CleanupRemaining int                      `json:"cleanup_remaining"`
	TargetStop       string                   `json:"target_stop"`
}

func encoded(v any) []byte { raw, _ := json.Marshal(v); return raw }

// Run requires the installation lease and a completed Docker absence check.
// One automatic pass is durably claimed; repeated starts only read its outcome.
// Unknown restore bindings or prior cleanup dispatches never authorize replay.
func Run(ctx context.Context, root, id string, containerAbsent bool, peer Peer) (out Outcome, err error) {
	out = Outcome{CampaignID: id, State: "unconfirmed", Reason: "evidence_unavailable", Closure: "unconfirmed", Cleanup: "unconfirmed", TargetStop: "not-permitted"}
	if !containerAbsent || peer == nil {
		return out, campaign.ErrInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	a, err := campaign.OpenNativeRecovery(root, id)
	if err != nil {
		return out, err
	}
	defer a.Close()
	f, err := loadFacts(ctx, a)
	if err != nil {
		return out, err
	}
	out.ManifestDigest = f.digest
	if !f.prepared {
		out.State = "not-prepared"
		out.Reason = "no_native_preparation"
		return out, nil
	}
	if f.terminal != nil {
		out = *f.terminal
		out.CampaignID = id
		out.ManifestDigest = f.digest
		return out, nil
	}
	if f.manifest.Target.Adapter == "https/v1" {
		out.State = "finalized"
		out.Reason = "local_execution_ended"
		out.Closure = "not-applicable"
		out.Cleanup = "not-needed"
		out.TargetStop = "not-applicable"
		fresh, saved, e := a.Begin()
		if e != nil {
			return out, e
		}
		if !fresh {
			if len(saved) == 0 {
				out.State = "unconfirmed"
				out.Reason = "prior_recovery_unconfirmed"
				return out, nil
			}
			var old Outcome
			if json.Unmarshal(saved, &old) != nil || old.CampaignID != id || old.ManifestDigest != f.digest {
				return out, campaign.ErrCorrupt
			}
			return old, nil
		}
		return out, a.Finish(out)
	}
	return finalize(ctx, a, f, peer, out)
}

func finalize(ctx context.Context, a *campaign.NativeRecovery, f facts, peer Peer, out Outcome) (result Outcome, err error) {
	id := out.CampaignID
	if f.allowStop {
		out.TargetStop = "unconfirmed"
	}
	out.CleanupRemaining = len(f.injections)
	fresh, saved, err := a.BeginChecked(func() error { return peer.CheckContract(ctx) })
	if err != nil {
		var compatibility *interceptor.CompatibilityError
		if errors.As(err, &compatibility) {
			out.Reason = "interceptor_" + compatibility.Reason
		}
		return out, err
	}
	if !fresh {
		if len(saved) == 0 {
			out.Reason = "prior_recovery_unconfirmed"
			return out, nil
		}
		var previous Outcome
		if interceptor.DecodeTypedBody(saved, &previous, campaign.ManifestLimit) != nil || previous.CampaignID != id || previous.ManifestDigest != out.ManifestDigest || previous.AttachmentDigest != out.AttachmentDigest {
			return out, campaign.ErrCorrupt
		}
		return previous, nil
	}
	defer func() { err = errors.Join(err, a.Finish(out)) }()
	out.Reason = "native_status_unconfirmed"
	retainFailure := func(operation string, e error) {
		if e == nil || len(out.Failures) >= 16 {
			return
		}
		if f := diagnostictext.FailureFromError(diagnostictext.Context(e, "Interceptor", operation, "")); f != nil {
			out.Failures = append(out.Failures, *f)
		}
	}
	query := func() (interceptor.Status, error) {
		if ctx.Err() != nil {
			return interceptor.Status{}, ctx.Err()
		}
		if e := a.Record("status-intent", encoded(map[string]string{"campaign_id": id})); e != nil {
			return interceptor.Status{}, e
		}
		s, e := peer.Status(ctx, id)
		if e != nil {
			retainFailure("status", e)
			_ = a.Record("status-failure", encoded(out.Failures))
			return s, e
		}
		if e = a.Record("status-result", encoded(s)); e != nil {
			return s, e
		}
		if s.CampaignID != id || !s.Matches(f.instance, f.binding) || s.Phase == "transitioning" {
			e = interceptor.StatusError(s, nil, "recovery-status")
			retainFailure("status", e)
			return s, e
		}
		return s, nil
	}
	call := func(kind string, p interceptor.PreparedOperation, decode func(interceptor.Response) (interceptor.Response, error)) (interceptor.Response, error) {
		if ctx.Err() != nil {
			return interceptor.Response{}, ctx.Err()
		}
		if e := a.Record(kind+"-intent", p.Bytes()); e != nil {
			return interceptor.Response{}, e
		}
		return interceptor.Call(p.Request().Operation, p.Request().RequestID,
			func() (interceptor.Response, error) { return peer.Execute(ctx, p) },
			func(r interceptor.Response) error {
				if len(r.Bytes()) == 0 {
					return nil
				}
				return a.Record(kind+"-result", r.Bytes())
			},
			decode)
	}
	deadline, _ := ctx.Deadline()
	request := func(operation string, revision uint64) interceptor.OperationRequest {
		key := "recovery-" + termination.NewRequestID()
		return interceptor.OperationRequest{CampaignID: id, SessionID: f.binding.SessionID, WorkerInstanceID: f.binding.WorkerInstanceID, RunRevision: f.binding.RunRevision, RequestID: key, OperationID: key, Operation: operation, ExpectedSessionRevision: revision, Deadline: deadline}
	}
	inspect := func() (interceptor.SessionStatus, error) {
		p, e := interceptor.PrepareOperation(request("session.status", 0), []byte(`{}`))
		if e != nil {
			return interceptor.SessionStatus{}, e
		}
		var v interceptor.SessionStatus
		_, e = call("session-status", p, func(r interceptor.Response) (interceptor.Response, error) {
			var err error
			v, err = interceptor.DecodeSessionStatus(r, id, f.binding)
			return r, err
		})
		if e != nil {
			return v, e
		}
		if v.Session.EnvironmentDigest != f.environment || v.Session.AppDigest != f.application || v.Session.CapabilityManifestDigest != f.manifest.Target.CapabilitySourceDigest || v.Session.FeedbackProfile != f.manifest.Target.NativeFeedbackProfile || v.Owner.AllowTargetStop != f.allowStop {
			return v, campaign.ErrInvalid
		}
		return v, nil
	}
	s, e := query()
	if e != nil {
		return out, nil
	}
	if s.Phase == "stopped" && s.Closed {
		out.Closure = "confirmed"
		out.TargetStop = "confirmed"
		out.Reason = "already_stopped"
		out.State = "partial"
		return out, nil
	}
	v, e := inspect()
	if e != nil {
		retainFailure("session.status", e)
		return out, nil
	}
	if v.Available {
		if f.closeAttempted {
			out.Reason = "prior_closure_unconfirmed"
			return out, nil
		}
		p, e := interceptor.PrepareClose(request("", v.Session.Revision))
		if e != nil {
			return out, e
		}
		_, e = call("close", p, func(r interceptor.Response) (interceptor.Response, error) {
			_, err := interceptor.DecodeClosure(r, id)
			return r, err
		})
		if e != nil {
			retainFailure("session.owner", e)
			out.Reason = "closure_unconfirmed"
			return out, nil
		}
		if _, e = query(); e != nil {
			return out, nil
		}
		v, e = inspect()
		if e != nil || v.Available {
			if e == nil {
				e = diagnostictext.New("Execution remains available after closure confirmation.")
			}
			retainFailure("session.status", e)
			out.Reason = "closure_unconfirmed"
			return out, nil
		}
	}
	out.Closure = "confirmed"
	out.State = "partial"
	out.Reason = ""
	out.Cleanup = "complete"
	if f.cleanupAttempted {
		out.Cleanup = "not-retried"
		out.Reason = "prior_cleanup_dispatched"
	} else {
		for _, injection := range f.injections[:min(len(f.injections), 64)] {
			p, e := interceptor.PrepareOperation(request("injection.delete", v.Session.Revision), encoded(map[string]string{"injection_id": injection}))
			if e != nil {
				return out, e
			}
			r, e := call("delete", p, func(r interceptor.Response) (interceptor.Response, error) {
				return interceptor.DecodeDeletion(r, v.Session.Revision)
			})

			if e != nil {
				out.Cleanup = "unconfirmed"
				out.Reason = "cleanup_unconfirmed"
				retainFailure("injection.delete", e)
				break
			}
			v.Session.Revision = r.SessionRevision
			out.CleanupConfirmed++
			out.CleanupRemaining--
		}
		if out.Cleanup == "complete" && out.CleanupRemaining > 0 {
			out.Cleanup = "bounded-remainder"
		}
	}
	if f.allowStop && !f.stopAttempted && ctx.Err() == nil {
		p, e := interceptor.PrepareLifecycle(interceptor.LifecycleRequest{CampaignID: id, SessionID: f.binding.SessionID, WorkerInstanceID: f.binding.WorkerInstanceID, RunRevision: f.binding.RunRevision, OperationID: "recovery-stop-" + termination.NewRequestID(), Operation: "session.stop"}, deadline)
		if e == nil {
			e = a.Record("stop-intent", p.Bytes())
		}
		if e == nil {
			_, e = interceptor.Call("session.stop", p.Request().OperationID,
				func() (interceptor.Response, error) { return peer.ExecuteLifecycle(ctx, p) },
				func(r interceptor.Response) error {
					if len(r.Bytes()) == 0 {
						return nil
					}
					return a.Record("stop-result", r.Bytes())
				},
				func(r interceptor.Response) (interceptor.StopResult, error) { return interceptor.DecodeStop(r, p) })
			if e == nil {
				out.TargetStop = "confirmed"
			} else {
				retainFailure("session.stop", e)
			}
		}
	}
	if out.Cleanup == "complete" && (!f.allowStop || out.TargetStop == "confirmed") {
		out.State = "complete"
	}
	return out, nil
}
