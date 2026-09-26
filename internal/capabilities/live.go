package capabilities

import (
	"errors"
	"regexp"

	"github.com/intrusiveai/operator_sandbox/contracts"
	"github.com/intrusiveai/operator_sandbox/internal/interceptor"
)

var ErrBinding = errors.New("capabilities do not match the ready Interceptor binding")
var logicalID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)

// Live is a verified observation of a ready native target. Recheck readiness and
// policy at launch/dispatch; this value is not a lease or permission to execute.
type Live struct {
	export             *Export
	campaign, instance string
	binding            interceptor.Binding
	profile            string
}

func (l *Live) Export() *Export { return l.export }

// BindLive ties verified capabilities to independently obtained attach/status
// results and the caller's pinned process instance. Worker IDs remain attribution.
func BindLive(catalog *contracts.Catalog, a interceptor.Attachment, s interceptor.Status, expectedInstance, targetID string) (*Live, error) {
	if !logicalID.MatchString(expectedInstance) || !logicalID.MatchString(a.CampaignID) || !logicalID.MatchString(a.Binding.SessionID) || !logicalID.MatchString(a.Binding.WorkerInstanceID) || a.Binding.RunRevision == 0 || a.Binding.RunRevision > contracts.MaxSafeInteger || !s.Ready() || !s.Matches(expectedInstance, a.Binding) || s.CampaignID != a.CampaignID || a.Session.CampaignID != a.CampaignID || a.Session.ID != a.Binding.SessionID || a.Session.Phase != "running" || a.Session.OperationAPIVersion != interceptor.OperationVersion {
		return nil, ErrBinding
	}
	e, err := FromNative(catalog, a.Capabilities, targetID)
	if err != nil {
		return nil, err
	}
	if e.native.Digest != a.Session.CapabilityManifestDigest || e.native.EnvironmentDigest != a.Session.EnvironmentDigest || e.native.ApplicationDigest != a.Session.AppDigest || !contains(e.native.FeedbackProfiles, a.Session.FeedbackProfile) {
		return nil, ErrBinding
	}
	return &Live{export: e, campaign: a.CampaignID, instance: expectedInstance, binding: a.Binding, profile: a.Session.FeedbackProfile}, nil
}
