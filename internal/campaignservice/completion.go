//go:build linux || darwin

package campaignservice

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/intrusiveai/operator_sandbox/contracts"
	"github.com/intrusiveai/operator_sandbox/internal/campaign"
	"github.com/intrusiveai/operator_sandbox/internal/termination"
)

type assessmentRecord struct {
	kind              string
	request, response campaign.ContentDescriptor
}
type completionState struct {
	records                         map[string]assessmentRecord
	hypotheses                      map[string]bool
	parents                         map[string]string
	objectives, scenarios, surfaces map[string]bool
	inputSizes                      map[string]int64
	binding                         map[string]any
	progressAt                      time.Time
	finalizing                      bool
	finalUntil                      time.Time
	finalRequests                   int
	finalBytes                      int64
	stopID                          string
	assessmentState                 string
}

func (s *Service) initializeCompletion(in campaign.LaunchInputs) {
	m := s.writer.Manifest()
	c := completionState{records: map[string]assessmentRecord{}, hypotheses: map[string]bool{}, parents: map[string]string{}, objectives: map[string]bool{}, scenarios: map[string]bool{}, surfaces: map[string]bool{}, inputSizes: map[string]int64{}}
	c.binding = map[string]any{"campaign_id": m.CampaignID, "launch_id": m.LaunchID, "run_revision": m.InitialRevision, "input_tree_digest": contracts.RawDigest(in.InputTree), "engine_context_digest": contracts.RawDigest(in.EngineContext), "prompt_digest": contracts.RawDigest(in.Prompt), "skill_set_digest": contracts.RawDigest(in.SkillSet), "image_digest": m.ImageDigest, "release_record_digest": m.ReleaseRecordDigest, "contract_package_version": m.Contract.Version, "contract_package_digest": m.Contract.Digest}
	var bundle struct {
		Objectives []struct {
			ID string `json:"objective_id"`
		}
		Scenarios []struct {
			ID string `json:"scenario_id"`
		}
	}
	_ = json.Unmarshal(s.target().BundleJSON(), &bundle)
	for _, o := range bundle.Objectives {
		c.objectives[o.ID] = true
	}
	for _, x := range bundle.Scenarios {
		c.scenarios[x.ID] = true
	}
	var tree struct {
		Entries []struct {
			ID   string `json:"entry_id"`
			Size int64  `json:"size_bytes"`
		}
	}
	_ = json.Unmarshal(in.InputTree, &tree)
	for _, e := range tree.Entries {
		c.inputSizes[e.ID] = e.Size
	}
	var public any
	_ = json.Unmarshal(s.target().Live().Export().PublicJSON(), &public)
	var collect func(any)
	collect = func(v any) {
		switch x := v.(type) {
		case map[string]any:
			if ref, ok := x["ref"].(string); ok {
				c.surfaces[ref] = true
			}
			for _, v := range x {
				collect(v)
			}
		case []any:
			for _, v := range x {
				collect(v)
			}
		}
	}
	collect(public)
	s.completion = c
}
func (s *Service) conclusionBinding() []byte {
	s.completion.binding["run_revision"] = s.attempts.Status().RunRevision
	return marshal(s.completion.binding)
}

// BeginFinalization closes exploration but permits bounded conclusion traffic.
// It never extends the campaign deadline and cannot reopen a terminal campaign.
func (s *Service) BeginFinalization(ctx context.Context) error {
	if err := s.acquire(ctx); err != nil {
		return err
	}
	defer s.release()
	if !s.admitted.Load() || s.writer.Fence().Err() != nil {
		return ErrService
	}
	return s.beginFinalization()
}
func (s *Service) beginFinalization() error {
	c := &s.completion
	if c.finalizing || c.stopID != "" {
		return nil
	}
	until := time.Now().Add(30 * time.Second)
	if s.deadline.Before(until) {
		until = s.deadline
	}
	if _, err := s.writer.AppendStored(campaign.Entry{RunRevision: s.attempts.Status().RunRevision, Kind: "service.finalizing", Metadata: marshal(map[string]any{"deadline": until.UTC().Format(time.RFC3339Nano), "maximum_requests": 16, "maximum_conclusion_bytes": 2 << 20})}, "", false); err != nil {
		s.Stop(err)
		return err
	}
	c.finalizing, c.finalUntil = true, until
	s.armCompletionDeadline(until)
	return nil
}

// The timer observes the fence independently; no service/journal lock gates kill.
func (s *Service) armCompletionDeadline(until time.Time) {
	go func() {
		timer := time.NewTimer(time.Until(until))
		defer timer.Stop()
		select {
		case <-s.writer.Fence().Done():
		case <-timer.C:
			s.Stop(context.DeadlineExceeded)
		}
	}()
}
func (s *Service) phaseAllows(operation string, body map[string]any) bool {
	c := &s.completion
	if c.stopID != "" {
		return operation == "engine.finish"
	}
	if !c.finalizing {
		return true
	}
	switch operation {
	case "engine.finish", "engine.diagnostic_report":
		return true
	case "engine.artifact_begin":
		return body["purpose"] == "conclusion"
	case "engine.artifact_put_part", "engine.artifact_commit":
		id, _ := body["upload_id"].(string)
		u := s.artifacts.uploads[id]
		return u != nil && u.Purpose == "conclusion"
	}
	return false
}
func (s *Service) chargeFinalization(q stateRequest) error {
	c := &s.completion
	if !c.finalizing {
		return nil
	}
	if !time.Now().Before(c.finalUntil) || c.finalRequests >= 16 {
		return contracts.ErrLoopStopped
	}
	bytes := int64(0)
	if q.Operation == "engine.artifact_put_part" {
		var part struct {
			Content []byte `json:"content"`
		}
		_ = json.Unmarshal(q.Body, &part)
		bytes = int64(len(part.Content))
	}
	if q.Operation == "engine.finish" {
		var body map[string]json.RawMessage
		_ = json.Unmarshal(q.Body, &body)
		bytes = int64(len(body["assessment"]))
	}
	if bytes > (2<<20)-c.finalBytes {
		return contracts.ErrLoopStopped
	}
	if _, err := s.writer.AppendStored(campaign.Entry{RunRevision: q.Revision, Kind: "service.finalization-charge", Metadata: marshal(map[string]any{"operation_id": q.ID, "requests": c.finalRequests + 1, "conclusion_bytes": c.finalBytes + bytes})}, "", false); err != nil {
		return err
	}
	c.finalRequests++
	c.finalBytes += bytes
	return nil
}
func (s *Service) handleCompletion(ctx context.Context, raw []byte, seq int64) ([]byte, error) {
	q, saved, err := s.observeTool(raw)
	if err != nil {
		return nil, err
	}
	if saved != nil {
		return s.stateEnvelope(raw, seq, *saved)
	}
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	var reply stateReply
	if q.Operation == "engine.record_append" {
		reply, err = s.appendRecord(q, raw, seq)
	} else {
		reply, err = s.acceptFinish(q)
	}
	if err != nil {
		return nil, err
	}
	response, err := s.stateEnvelope(raw, seq, reply)
	if err != nil {
		return nil, err
	}
	if err = s.attempts.Tools().Finish(q.ID, marshal(reply), 0); err != nil {
		return nil, err
	}
	if q.Operation == "engine.finish" && reply.Error == nil {
		s.completion.stopID = q.ID
		s.completion.assessmentState, _ = reply.Result.(map[string]any)["assessment_state"].(string)
		s.stopAccepted.Store(true)
		until := time.Now().Add(5 * time.Second)
		if s.deadline.Before(until) {
			until = s.deadline
		}
		s.armCompletionDeadline(until)
	}
	return response, nil
}
func (s *Service) appendRecord(q stateRequest, raw []byte, seq int64) (stateReply, error) {
	var body struct {
		Kind   string         `json:"record_kind"`
		Record map[string]any `json:"record"`
	}
	_ = json.Unmarshal(q.Body, &body)
	if body.Kind == "progress" && !s.completion.progressAt.IsZero() && time.Since(s.completion.progressAt) < time.Second {
		return stateDenied("LIMIT_EXCEEDED"), nil
	}
	newHyp := ""
	if body.Kind == "hypothesis" {
		newHyp, _ = body.Record["hypothesis_id"].(string)
		provenance := body.Record["provenance"].(map[string]any)
		parent, _ := provenance["parent_hypothesis_id"].(string)
		for id := parent; id != ""; id = s.completion.parents[id] {
			if id == newHyp {
				return stateDenied("RECORD_REFERENCE_INVALID"), nil
			}
		}
	}
	if err := s.checkReferences(body.Record, newHyp, false); err != nil {
		return s.referenceError(err, "RECORD_REFERENCE_INVALID")
	}
	receipt := "record-" + termination.NewRequestID()
	result := stateReply{Result: map[string]any{"receipt_id": receipt, "record_kind": body.Kind, "assertion_origin": "harness", "attribution": map[string]any{"campaign_id": q.Campaign, "launch_id": q.Launch, "run_revision": q.Revision}}}
	response, err := s.stateEnvelope(raw, seq, result)
	if err != nil {
		return stateReply{}, err
	}
	refs, err := s.writer.AppendStored(campaign.Entry{RunRevision: q.Revision, Kind: "assessment.record", Metadata: marshal(map[string]any{"operation_id": q.ID, "receipt_id": receipt, "record_kind": body.Kind, "assertion_origin": "harness"}), Content: []campaign.Content{{Role: "record-request", MediaType: "application/json", Bytes: raw}, {Role: "record-response", MediaType: "application/json", Bytes: response}}}, "", false)
	if err != nil {
		return stateReply{}, err
	}
	s.completion.records[receipt] = assessmentRecord{body.Kind, refs[0], refs[1]}
	if body.Kind == "hypothesis" {
		id := body.Record["hypothesis_id"].(string)
		s.completion.hypotheses[id] = true
		parent, _ := body.Record["provenance"].(map[string]any)["parent_hypothesis_id"].(string)
		s.completion.parents[id] = parent
	}
	if body.Kind == "progress" {
		s.completion.progressAt = time.Now()
	}
	return result, nil
}

var errReference = errors.New("invalid campaign assessment reference")

func (s *Service) referenceError(err error, code string) (stateReply, error) {
	if errors.Is(err, errReference) || errors.Is(err, campaign.ErrInvalid) {
		return stateDenied(code), nil
	}
	return stateReply{}, err
}
func (s *Service) conclusionUpload(receipt string) (*artifactUpload, error) {
	for _, u := range s.artifacts.uploads {
		if u.Receipt == receipt && receipt != "" && u.Purpose == "conclusion" {
			if u.Commit == nil {
				return nil, referenceIssue("artifact_not_committed", "/artifact_receipt", receipt, nil)
			}
			return u, nil
		}
	}
	return nil, referenceIssue("artifact_receipt_invalid", "/artifact_receipt", receipt, nil)
}

// acceptFinish validates a draft and creates the bound retained conclusion itself.
// No guest artifact/record chain is required or synthesized.
func (s *Service) acceptFinish(q stateRequest) (stateReply, error) {
	if s.completion.stopID != "" {
		return stateDenied("STATE_CHANGED"), nil
	}
	var body map[string]json.RawMessage
	_ = json.Unmarshal(q.Body, &body)
	var finish string
	_ = json.Unmarshal(body["finish_reason"], &finish)
	state := "unavailable"
	var content []byte
	if _, unavailable := body["unavailable"]; !unavailable {
		draft := []byte(body["assessment"])
		if receiptRaw, ok := body["artifact_receipt"]; ok {
			var receipt string
			_ = json.Unmarshal(receiptRaw, &receipt)
			u, err := s.conclusionUpload(receipt)
			if err != nil {
				return s.rejectConclusion(q, err)
			}
			draft, err = s.readUpload(u)
			if err != nil {
				return s.rejectConclusion(q, referenceIssue("evidence_unavailable", "/artifact_receipt", receipt, err))
			}
			if contracts.RawDigest(draft) != u.Artifact.Digest {
				return s.rejectConclusion(q, referenceIssue("artifact_integrity_failed", "/artifact_receipt", receipt, nil))
			}
		}
		conclusion, err := s.target().Protocol().NormalizeAssessment(draft, s.conclusionBinding(), finish)
		if err != nil {
			var issue *contracts.ConclusionError
			if !errors.As(err, &issue) {
				path := "/assessment"
				var argument *contracts.ArgumentError
				if errors.As(err, &argument) {
					path = argument.FieldPath
				}
				err = &contracts.ConclusionError{Reason: "invalid_structure", FieldPath: path, Cause: err}
			}
			return s.rejectConclusion(q, err)
		}
		if err = s.checkConclusionReferences(conclusion); err != nil {
			return s.rejectConclusion(q, err)
		}
		content = marshal(conclusion)
		if _, uploaded := body["artifact_receipt"]; !uploaded {
			if _, err = s.retainContent(q.ID, "conclusion", draft, "application/json", "raw"); err != nil {
				var argument *contracts.ArgumentError
				if errors.As(err, &argument) {
					return s.rejectConclusion(q, &contracts.ConclusionError{Reason: "assessment_budget_exceeded", FieldPath: "/assessment"})
				}
				return stateReply{}, s.retentionFailure(q.ID, "assessment", err)
			}
		}
		state = "committed"
	}
	if err := s.beginFinalization(); err != nil {
		return stateReply{}, err
	}
	reply := stateReply{Result: map[string]any{"status": "accepted", "stop_receipt": "stop-" + termination.NewRequestID(), "execution_admission": "closed", "assessment_state": state, "exit_required": true, "exit_within_ms": 5000, "finalization_status": "pending"}}
	metadata := map[string]any{"operation_id": q.ID, "finish_reason": finish, "conclusion_state": state, "assessment_state": state, "execution_admission": "closed", "finalization_status": "pending"}
	if reason, ok := body["unavailable"]; ok {
		metadata["unavailable"] = json.RawMessage(reason)
	}
	entry := campaign.Entry{RunRevision: q.Revision, Kind: "service.stop-accepted", Metadata: marshal(metadata), Content: []campaign.Content{{Role: "stop-result", MediaType: "application/json", Bytes: marshal(reply)}}}
	if content != nil {
		entry.Content = append(entry.Content, campaign.Content{Role: "conclusion", MediaType: "application/json", Bytes: content})
	}
	if _, err := s.writer.AppendStored(entry, "", false); err != nil {
		return stateReply{}, err
	}
	return reply, nil
}
