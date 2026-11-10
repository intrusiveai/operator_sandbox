package capabilities

import (
	"encoding/json"

	"github.com/intrusiveai/operator_sandbox/internal/interceptor"
)

// ExecutionFacts is an independent copy of verified native facts. It describes
// the target; it does not authorize a selector or grant permission to dispatch.
type ExecutionFacts struct {
	Operations        []OperationCapability
	NativeOperations  []string
	InjectionProfiles []InjectionProfile
	Services          []ServiceCapability
	FileNamespaces    []FileNamespaceCapability
	Features          []string
	EvidenceKinds     []string
}

func (e *Export) ExecutionFacts() ExecutionFacts {
	source := ExecutionFacts{
		Operations: e.native.Operations, NativeOperations: e.native.CrossVMOperations,
		InjectionProfiles: e.native.InjectionProfiles, Services: e.native.Services,
		FileNamespaces: e.native.FileNamespaces,
	}
	if e.https != nil {
		source = ExecutionFacts{Operations: e.https.Operations}
	}
	// Use the verified projection's availability decisions for both adapters.
	source.Features = e.projection["features"].([]string)
	for _, item := range records(e.projection["evidence_classes"]) {
		source.EvidenceKinds = append(source.EvidenceKinds, item["feedback_kind"].(string))
	}
	raw, _ := json.Marshal(source)
	var copy ExecutionFacts
	_ = json.Unmarshal(raw, &copy)
	return copy
}

func (l *Live) Binding() interceptor.Binding { return l.binding }
func (l *Live) CampaignID() string           { return l.campaign }
func (l *Live) NativeProfile() string        { return l.profile }

// ExecutionPolicy checks that the admission still describes this exact live
// session/export and returns a detached policy copy. Dispatch needs a fresh
// readiness check as well; this record is not a lease.
func (c *Compatibility) ExecutionPolicy(l *Live) (Policy, error) {
	var record struct {
		Campaign string              `json:"campaign_id"`
		Instance string              `json:"instance_id"`
		Binding  interceptor.Binding `json:"binding"`
		Live     map[string]string   `json:"live"`
		Policy   Policy              `json:"policy"`
	}
	if c == nil || l == nil || json.Unmarshal(c.record, &record) != nil || record.Campaign != l.campaign || record.Instance != l.instance || record.Binding != l.binding || record.Live["source_digest"] != l.export.SourceDigest() || record.Live["projection_digest"] != l.export.ProjectionDigest() {
		return Policy{}, ErrBinding
	}
	return record.Policy, nil
}
