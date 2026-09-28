//go:build linux || darwin

package nativerecovery

import (
	"context"
	"encoding/json"
	"regexp"
	"sort"

	"github.com/intrusiveai/operator_sandbox/contracts"
	"github.com/intrusiveai/operator_sandbox/internal/campaign"
	"github.com/intrusiveai/operator_sandbox/internal/interceptor"
	"github.com/intrusiveai/operator_sandbox/internal/targetprofile"
)

var identifier = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)

type facts struct {
	manifest                                                  campaign.RunManifest
	digest, instance, environment, application                string
	binding                                                   interceptor.Binding
	allowStop                                                 bool
	prepared, closeAttempted, cleanupAttempted, stopAttempted bool
	terminal                                                  *Outcome
	injections                                                []string
}

func readParts(a *campaign.NativeRecovery, descs []campaign.ContentDescriptor) ([]byte, error) {
	out := []byte{}
	for _, d := range descs {
		if len(out)+int(d.SizeBytes) > 16<<20 {
			return nil, campaign.ErrQuota
		}
		raw, e := a.ReadContent(d)
		if e != nil {
			return nil, e
		}
		out = append(out, raw...)
	}
	return out, nil
}
func role(a *campaign.NativeRecovery, e campaign.Event, name string) ([]byte, error) {
	for _, d := range e.Content {
		if d.Role == name {
			return a.ReadContent(d)
		}
	}
	return nil, campaign.ErrCorrupt
}

func loadFacts(ctx context.Context, a *campaign.NativeRecovery) (facts, error) {
	var f facts
	var hostPolicy, jsonSource json.RawMessage
	var target campaign.TargetBinding
	ids := map[string]bool{}
	// Retain only the last state of deletion attempts, to avoid replaying a lost
	// deletion. A successfully completed historical deletion may be restored later.
	deletions := map[string]string{}
	report, err := a.Inspect(ctx, func(e campaign.Event) error {
		switch e.Kind {
		case "campaign.prepared":
			if f.prepared {
				return campaign.ErrCorrupt
			}
			raw, err := readParts(a, e.Content)
			if err != nil {
				return err
			}
			var v struct {
				Instance string                 `json:"instance_id"`
				Target   campaign.TargetBinding `json:"target"`
				Policy   json.RawMessage        `json:"host_policy"`
				Source   json.RawMessage        `json:"live_source"`
			}
			if json.Unmarshal(raw, &v) != nil {
				return campaign.ErrCorrupt
			}
			f.instance, hostPolicy, jsonSource, target = v.Instance, v.Policy, v.Source, v.Target
			f.binding = interceptor.Binding{SessionID: target.SessionID, WorkerInstanceID: target.WorkerInstanceID, RunRevision: uint64(e.RunRevision)}
			f.prepared = true
		case "state.replacement-verified":
			if !f.prepared {
				return campaign.ErrCorrupt
			}
			raw, err := role(a, e, "request")
			if err != nil {
				return err
			}
			var v struct {
				Binding interceptor.Binding `json:"binding"`
				Status  interceptor.Status  `json:"status"`
			}
			if json.Unmarshal(raw, &v) != nil || v.Binding.RunRevision != f.binding.RunRevision+1 || v.Binding.SessionID == f.binding.SessionID || !v.Status.Matches(f.instance, v.Binding) || v.Status.CampaignID != e.CampaignID {
				return campaign.ErrCorrupt
			}
			f.binding = v.Binding
			deletions = map[string]string{}
		case "service.native-close-intent":
			f.closeAttempted = true
		case "service.native-cleanup-intent":
			f.cleanupAttempted = true
		case "service.target-stop-intent":
			f.stopAttempted = true
		case "service.terminal-result":
			raw, err := role(a, e, "response")
			if err != nil {
				return err
			}
			var v struct {
				Closure   string `json:"closure"`
				Cleanup   string `json:"cleanup_state"`
				Stop      string `json:"target_stop"`
				Confirmed int    `json:"cleanup_confirmed"`
				Remaining int    `json:"cleanup_remaining"`
			}
			if json.Unmarshal(raw, &v) != nil {
				return campaign.ErrCorrupt
			}
			f.terminal = &Outcome{State: "previously-finalized", Closure: v.Closure, Cleanup: v.Cleanup, TargetStop: v.Stop, CleanupConfirmed: v.Confirmed, CleanupRemaining: v.Remaining}
		default:
			var v struct {
				Step campaign.NativeStep `json:"native_step"`
			}
			if json.Unmarshal(e.Metadata, &v) != nil {
				return campaign.ErrCorrupt
			}
			s := v.Step
			if s.Operation == "injection.delete" && s.ID != "" && s.SessionID == f.binding.SessionID {
				if len(deletions) >= 65536 {
					return campaign.ErrQuota
				}
				deletions[s.ID] = s.State
			}
			if e.Kind != "native.resolved" || s.Operation != "injection.arm" || s.State != campaign.ResultCommitted || s.Outcome != "succeeded" {
				return nil
			}
			raw, err := readParts(a, s.Request)
			if err != nil {
				return err
			}
			p, err := interceptor.ParsePreparedOperation(raw)
			if err != nil || p.Request().CampaignID != e.CampaignID || p.Request().Operation != "injection.arm" {
				return campaign.ErrCorrupt
			}
			var q struct {
				Body struct {
					Definition struct {
						ID string `json:"id"`
					} `json:"definition"`
				} `json:"body"`
			}
			if json.Unmarshal(raw, &q) != nil || !identifier.MatchString(q.Body.Definition.ID) {
				return campaign.ErrCorrupt
			}
			if len(ids) >= 65536 {
				return campaign.ErrQuota
			}
			ids[q.Body.Definition.ID] = true
		}
		return nil
	})
	if err != nil {
		return f, err
	}
	if !report.JournalIntact {
		return f, campaign.ErrCorrupt
	}
	f.manifest, f.digest = report.Manifest, report.ManifestDigest
	if !f.prepared {
		return f, nil
	}
	d, err := contracts.CanonicalDigest(hostPolicy, campaign.ManifestLimit)
	if err != nil || d != report.Manifest.HostPolicyDigest || target != report.Manifest.Target || !identifier.MatchString(f.instance) {
		return f, campaign.ErrCorrupt
	}
	var hp struct {
		Profile json.RawMessage `json:"target_profile"`
	}
	var source struct {
		Environment string `json:"environment_digest"`
		Application string `json:"application_digest"`
		Digest      string `json:"digest"`
	}
	if json.Unmarshal(hostPolicy, &hp) != nil || json.Unmarshal(jsonSource, &source) != nil || source.Digest != target.CapabilitySourceDigest || source.Environment == "" || source.Application == "" {
		return f, campaign.ErrCorrupt
	}
	p, err := targetprofile.Parse(hp.Profile)
	if err != nil {
		return f, err
	}
	f.allowStop = p.Settings().AllowTargetStop
	f.environment, f.application = source.Environment, source.Application
	for _, state := range deletions {
		if state == campaign.Dispatched || state == campaign.Unknown || state == campaign.IntentCommitted {
			f.cleanupAttempted = true
		}
	}
	for id := range ids {
		f.injections = append(f.injections, id)
	}
	sort.Strings(f.injections)
	return f, nil
}
