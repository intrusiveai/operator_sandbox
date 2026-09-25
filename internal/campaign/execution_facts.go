//go:build linux || darwin

package campaign

import (
	"encoding/json"
	"sort"

	"github.com/intrusive-ai/operator-sandbox/internal/interceptor"
)

// Lineage resolves only successfully committed native registrations/invocations
// in the selected session. It never reconstructs lineage from harness assertions.
func (a *Attempts) Lineage(session, parent string) (*interceptor.AttemptContext, []string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	var found *interceptor.AttemptContext
	turns := []string{}
	for _, step := range a.nativeSteps {
		if step.SessionID != session || step.State != ResultCommitted || step.Outcome != "succeeded" {
			continue
		}
		if step.Operation != "attempt.register" && step.Operation != "application.invoke" {
			continue
		}
		raw, err := a.NativeSteps().read(step.Response)
		if err != nil {
			return nil, nil, err
		}
		response, err := interceptor.ParseResponse(raw)
		if err != nil {
			return nil, nil, a.failure(ErrCorrupt)
		}
		if step.Operation == "attempt.register" {
			var c interceptor.AttemptContext
			if interceptor.DecodeTypedBody(response.Body, &c, interceptor.JSONLimit) != nil || c.CampaignID != a.w.manifest.CampaignID || c.Digest != interceptor.AttemptContextDigest(c) {
				return nil, nil, a.failure(ErrCorrupt)
			}
			if parent != "" && c.AttemptID == parent {
				copy := c
				found = &copy
			}
		} else {
			var turn interceptor.Turn
			if interceptor.DecodeTypedBody(response.Body, &turn, interceptor.JSONLimit) != nil || !validID(turn.ID) {
				return nil, nil, a.failure(ErrCorrupt)
			}
			turns = append(turns, turn.ID)
		}
	}
	sort.Strings(turns)
	return found, turns, nil
}

// CleanupCandidates is host-only and requires terminal fencing. Successfully
// armed IDs remain candidates across restores, including historical deletions:
// only the current target can establish whether a checkpoint resurrected them.
func (a *Attempts) CleanupCandidates(maximum int) ([]string, int, error) {
	if a.w.Fence().Err() == nil || maximum < 1 || maximum > 64 {
		return nil, 0, ErrInvalid
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	set := map[string]bool{}
	for _, step := range a.nativeSteps {
		if step.Operation != "injection.arm" || step.Outcome != "succeeded" || step.State != ResultCommitted {
			continue
		}
		raw, err := a.NativeSteps().read(step.Request)
		if err != nil {
			return nil, 0, err
		}
		var envelope struct {
			Body struct {
				Definition interceptor.Definition `json:"definition"`
			} `json:"body"`
		}
		if json.Unmarshal(raw, &envelope) != nil || !validID(envelope.Body.Definition.ID) {
			return nil, 0, a.failure(ErrCorrupt)
		}
		set[envelope.Body.Definition.ID] = true
	}
	ids := []string{}
	for id := range set {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	total := len(ids)
	if len(ids) > maximum {
		ids = ids[:maximum]
	}
	return ids, total, nil
}
