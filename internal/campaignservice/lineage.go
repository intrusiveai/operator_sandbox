//go:build linux || darwin

package campaignservice

import (
	"encoding/json"

	"github.com/intrusive-ai/operator-sandbox/internal/attemptadapter"
)

type nativeLineage struct {
	Parents map[string]attemptadapter.Parent `json:"parents"`
	Turns   []string                         `json:"turns"`
}

func cloneLineage(v nativeLineage, session string) nativeLineage {
	// Deep-copy pointer-bearing native contexts before retaining a checkpoint view.
	var out nativeLineage
	_ = json.Unmarshal(marshal(v), &out)
	if out.Parents == nil {
		out.Parents = map[string]attemptadapter.Parent{}
	}
	if session != "" {
		for id, p := range out.Parents {
			p.SessionID = session
			out.Parents[id] = p
		}
	}
	return out
}
func (s *Service) rememberAttempt(request, response []byte) error {
	var q struct {
		Body struct {
			AttemptID  string `json:"attempt_id"`
			Generation uint64 `json:"generation"`
		} `json:"body"`
	}
	var r struct {
		Result struct {
			Status string `json:"status"`
		} `json:"result"`
	}
	_ = json.Unmarshal(request, &q)
	_ = json.Unmarshal(response, &r)
	if r.Result.Status != "completed" {
		return nil
	}
	if _, exists := s.history[q.Body.AttemptID]; exists {
		return nil
	} // Historical duplicate after restore.
	session := s.target().Live().Binding().SessionID
	parent, turns, err := s.attempts.Lineage(session, q.Body.AttemptID)
	if err != nil {
		return err
	}
	if parent == nil {
		return ErrService
	}
	fact := attemptadapter.Parent{SessionID: session, Context: *parent, CampaignGeneration: q.Body.Generation}
	s.lineage.Parents[q.Body.AttemptID] = fact
	s.history[q.Body.AttemptID] = fact
	// Native ledger supplies all current-session turns; restored turns stay available.
	seen := map[string]bool{}
	for _, id := range s.lineage.Turns {
		seen[id] = true
	}
	for _, id := range turns {
		if !seen[id] {
			s.lineage.Turns = append(s.lineage.Turns, id)
			seen[id] = true
		}
	}
	return nil
}
