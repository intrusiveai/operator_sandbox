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
	"github.com/intrusiveai/operator_sandbox/internal/interceptor"
	"github.com/intrusiveai/operator_sandbox/internal/termination"
)

type Peer interface {
	Status(context.Context, string) (interceptor.Status, error)
	Execute(context.Context, interceptor.PreparedOperation) (interceptor.Response, error)
	ExecuteLifecycle(context.Context, interceptor.PreparedLifecycle) (interceptor.Response, error)
}
type Outcome struct {
	CampaignID       string `json:"campaign_id"`
	ManifestDigest   string `json:"run_manifest_digest,omitempty"`
	AttachmentDigest string `json:"attachment_digest,omitempty"`
	State            string `json:"state"`
	Reason           string `json:"reason"`
	Closure          string `json:"closure"`
	Cleanup          string `json:"cleanup"`
	CleanupConfirmed int    `json:"cleanup_confirmed"`
	CleanupRemaining int    `json:"cleanup_remaining"`
	TargetStop       string `json:"target_stop"`
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
	return finalize(ctx, a, f, peer, out)
}

func finalize(ctx context.Context, a *campaign.NativeRecovery, f facts, peer Peer, out Outcome) (result Outcome, err error) {
	id := out.CampaignID
	if f.allowStop {
		out.TargetStop = "unconfirmed"
	}
	out.CleanupRemaining = len(f.injections)
	fresh, saved, err := a.Begin()
	if err != nil {
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
	query := func() (interceptor.Status, error) {
		if ctx.Err() != nil {
			return interceptor.Status{}, ctx.Err()
		}
		if e := a.Record("status-intent", encoded(map[string]string{"campaign_id": id})); e != nil {
			return interceptor.Status{}, e
		}
		s, e := peer.Status(ctx, id)
		if e != nil {
			_ = a.Record("status-failure", encoded(map[string]string{"reason": "unavailable"}))
			return s, e
		}
		if e = a.Record("status-result", encoded(s)); e != nil {
			return s, e
		}
		if s.CampaignID != id || !s.Matches(f.instance, f.binding) || s.Phase == "transitioning" {
			return s, campaign.ErrInvalid
		}
		return s, nil
	}
	call := func(kind string, p interceptor.PreparedOperation) (interceptor.Response, error) {
		if ctx.Err() != nil {
			return interceptor.Response{}, ctx.Err()
		}
		if e := a.Record(kind+"-intent", p.Bytes()); e != nil {
			return interceptor.Response{}, e
		}
		r, e := peer.Execute(ctx, p)
		if len(r.Bytes()) == 0 {
			_ = a.Record(kind+"-failure", encoded(map[string]string{"reason": "unconfirmed"}))
			return r, campaign.ErrInvalid
		}
		if recordErr := a.Record(kind+"-result", r.Bytes()); recordErr != nil {
			return r, recordErr
		}
		return r, e
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
		r, e := call("session-status", p)
		if e != nil {
			return interceptor.SessionStatus{}, e
		}
		v, e := interceptor.DecodeSessionStatus(r, id, f.binding)
		if e != nil || v.Session.EnvironmentDigest != f.environment || v.Session.AppDigest != f.application || v.Session.CapabilityManifestDigest != f.manifest.Target.CapabilitySourceDigest || v.Session.FeedbackProfile != f.manifest.Target.NativeFeedbackProfile || v.Owner.AllowTargetStop != f.allowStop {
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
		r, e := call("close", p)
		if e == nil {
			_, e = interceptor.DecodeClosure(r, id)
		}
		if e != nil {
			out.Reason = "closure_unconfirmed"
			return out, nil
		}
		if _, e = query(); e != nil {
			return out, nil
		}
		v, e = inspect()
		if e != nil || v.Available {
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
			r, e := call("delete", p)
			if e != nil || r.SessionRevision < v.Session.Revision || !(r.Status == 204 && (len(r.Body) == 0 || string(r.Body) == "null") && r.SessionRevision > v.Session.Revision || r.Status == 404 && r.Code() == "not_found") {
				out.Cleanup = "unconfirmed"
				out.Reason = "cleanup_unconfirmed"
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
			r, callErr := peer.ExecuteLifecycle(ctx, p)
			if len(r.Bytes()) > 0 {
				e = a.Record("stop-result", r.Bytes())
			} else {
				e = campaign.ErrInvalid
				_ = a.Record("stop-failure", encoded(map[string]string{"reason": "unconfirmed"}))
			}
			if e == nil && callErr == nil {
				if _, e = interceptor.DecodeStop(r, p); e == nil {
					out.TargetStop = "confirmed"
				}
			}
		}
	}
	if out.Cleanup == "complete" && (!f.allowStop || out.TargetStop == "confirmed") {
		out.State = "complete"
	}
	return out, nil
}
