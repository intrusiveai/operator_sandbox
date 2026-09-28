//go:build linux || darwin

package nativerecovery

import (
	"context"
	"encoding/json"
	"regexp"

	"github.com/intrusiveai/operator_sandbox/contracts"
	"github.com/intrusiveai/operator_sandbox/internal/campaign"
	"github.com/intrusiveai/operator_sandbox/internal/interceptor"
	"github.com/intrusiveai/operator_sandbox/internal/nativeevidence"
)

type RetainedSession struct {
	Identity interceptor.EvidenceIdentity `json:"identity"`
	Outcomes []campaign.EvidenceOutcome   `json:"collection_outcomes"`
	Archives []RetainedArchive            `json:"archives"`
}
type RetainedArchive struct {
	Source   string                  `json:"source"`
	Record   campaign.NativeEvidence `json:"record"`
	Verified bool                    `json:"verified"`
}

var retainedDigest = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

func selectionDigest(policy campaign.EvidencePolicy, target nativeevidence.Target) (string, error) {
	raw, _ := json.Marshal(struct {
		Policy campaign.EvidencePolicy `json:"policy"`
		Target nativeevidence.Target   `json:"target"`
	}{policy, target})
	return contracts.CanonicalDigest(raw, campaign.ManifestLimit)
}

// RetainedEvidence requires the caller's campaign lock. It only reads local
// evidence, including historical online and latest explicit collection results.
func RetainedEvidence(ctx context.Context, a *campaign.NativeRecovery) ([]RetainedSession, error) {
	f, e := loadFacts(ctx, a)
	if e != nil {
		return nil, e
	}
	if f.manifest.Target.Adapter == "https/v1" {
		return []RetainedSession{}, nil
	}
	facts, e := loadEvidenceFacts(ctx, a, f)
	if e != nil {
		return nil, e
	}
	imports, e := a.ImportedEvidence()
	if e != nil {
		return nil, e
	}
	out := []RetainedSession{}
	selected := map[string]bool{}
	for _, target := range facts.targets {
		id := target.Identity.SessionID
		selected[id] = true
		v := RetainedSession{Identity: target.Identity, Outcomes: []campaign.EvidenceOutcome{}, Archives: []RetainedArchive{}}
		appendArchive := func(source string, record campaign.NativeEvidence) error {
			if record.Provenance.Identity != target.Identity || !retainedDigest.MatchString(record.Provenance.Transfer.SHA256) {
				return campaign.ErrCorrupt
			}
			verified := a.VerifyRetainedEvidence(ctx, record) == nil
			v.Archives = append(v.Archives, RetainedArchive{source, record, verified})
			return nil
		}
		if old, ok := facts.completed[id]; ok {
			v.Outcomes = append(v.Outcomes, old)
		}
		if old, ok := facts.adopted[id]; ok {
			if e = appendArchive("live", old); e != nil {
				return nil, e
			}
		}
		digest, e := selectionDigest(facts.policy, target)
		if e != nil {
			return nil, e
		}
		late, e := a.ReadEvidence(id, digest)
		if e != nil {
			return nil, e
		}
		if late != nil {
			if !validEvidenceOutcome(late.Outcome, target, facts.policy) {
				return nil, campaign.ErrCorrupt
			}
			v.Outcomes = append(v.Outcomes, late.Outcome)
			if late.Outcome.ArchivePath != "" {
				if late.Archive == nil || late.Archive.Path != late.Outcome.ArchivePath || late.Archive.Provenance.Transfer.SHA256 != late.Outcome.ArchiveDigest || late.Archive.Provenance.State != late.Outcome.State {
					return nil, campaign.ErrCorrupt
				}
				if e = appendArchive("late", *late.Archive); e != nil {
					return nil, e
				}
			}
		}
		for _, vimport := range imports {
			if vimport.Archive.Provenance.Identity.SessionID == id {
				if e = appendArchive("import", vimport.Archive); e != nil {
					return nil, e
				}
			}
		}
		out = append(out, v)
	}
	for _, v := range imports {
		if !selected[v.Archive.Provenance.Identity.SessionID] {
			return nil, campaign.ErrCorrupt
		}
	}
	return out, ctx.Err()
}
