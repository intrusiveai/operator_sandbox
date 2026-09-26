//go:build linux || darwin

package preparation

import (
	"github.com/intrusiveai/operator_sandbox/internal/attemptadapter"
	"github.com/intrusiveai/operator_sandbox/internal/capabilities"
	"github.com/intrusiveai/operator_sandbox/internal/interceptor"
)

// Replacement preserves immutable launch inputs and policy. The caller must first
// verify the native restore receipt, then durably adopt this live binding.
func (t *Target) Replacement(r interceptor.RestoreResult, status interceptor.Status) (*Target, error) {
	if r.Binding.RunRevision != t.live.Binding().RunRevision+1 || r.Binding.SessionID == t.live.Binding().SessionID {
		return nil, ErrPreparation
	}
	a := interceptor.Attachment{CampaignID: t.live.CampaignID(), Binding: r.Binding, Session: r.Session, RawSession: r.RawSession, Capabilities: t.live.Export().NativeJSON()}
	live, err := capabilities.BindLive(t.protocol.Catalog(), a, status, t.instance, t.profile.Settings().TargetID)
	if err != nil {
		return nil, err
	}
	policy, err := t.profile.Resolve(live)
	if err != nil {
		return nil, err
	}
	compat, err := capabilities.Check(t.bundle, t.authoring, live, policy.CapabilityPolicy())
	if err != nil {
		return nil, err
	}
	next := *t
	next.live, next.policy, next.compatibility = live, policy, compat
	return &next, nil
}

// InputsFor reads the original campaign artifacts with a verified descendant's
// live routing. It never changes the immutable bootstrap context or manifest.
func (s *Stored) InputsFor(target *Target) (attemptadapter.Inputs, error) {
	if target == nil || target.bundle != s.target.bundle || target.profile != s.target.profile || target.protocol != s.target.protocol || target.artifacts == nil {
		return attemptadapter.Inputs{}, ErrPreparation
	}
	in, err := s.Inputs()
	in.Live, in.Policy, in.Compatibility = target.live, target.policy, target.compatibility
	return in, err
}

func (s *Stored) ArtifactUsage() (objects, bytes int64) {
	for _, a := range s.target.artifacts {
		objects++
		bytes += int64(len(a.Bytes))
	}
	return
}
