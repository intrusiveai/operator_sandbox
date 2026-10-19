package contracts

import (
	"encoding/base64"
	"encoding/json"
)

const DirectInputLimit = 32768
const DirectInputDepth = 16

// ArgumentError describes a deterministic rule without including submitted prose.
// Identifiers and dynamic JSON paths must still be sanitized by the output boundary.
type ArgumentError struct {
	Reason, FieldPath, Correction string
	Cause                         error
}

func (e *ArgumentError) Error() string { return e.Reason + ": " + e.Correction }
func (e *ArgumentError) Unwrap() error {
	if e.Cause != nil {
		return e.Cause
	}
	return ErrProtocol
}
func argumentIssue(reason, path, correction string) error {
	return &ArgumentError{Reason: reason, FieldPath: path, Correction: correction}
}

type ExecutionInput struct {
	Bytes                                        []byte
	MediaType, Canonicalization, ArtifactReceipt string
}

// ResolveDirectInput validates the direct representation. Artifact receipt lookup
// and the smaller target-specific byte/media limits belong to the host.
func (p *Protocol) ResolveDirectInput(value map[string]any) (ExecutionInput, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return ExecutionInput{}, ErrJSON
	}
	if _, err = p.catalog.Validate(ExecutionInputSchema, raw, OrdinaryLimit); err != nil {
		return ExecutionInput{}, err
	}
	if receipt, ok := value["artifact_receipt"].(string); ok {
		return ExecutionInput{ArtifactReceipt: receipt}, nil
	}
	result := ExecutionInput{Canonicalization: "raw"}
	switch value["encoding"] {
	case "json":
		if jsonDepth(value["value"]) > DirectInputDepth {
			return result, argumentIssue("input_depth_exceeded", "/value", "Use JSON with at most 16 nested containers or publish an artifact.")
		}
		result.Bytes, err = canonicalValue(value["value"], OrdinaryLimit)
		result.MediaType = "application/json"
		result.Canonicalization = "jcs-v1"
	case "utf8":
		result.Bytes = []byte(value["text"].(string))
		result.MediaType = "text/plain"
	case "base64":
		data := value["data"].(string)
		result.Bytes, err = base64.StdEncoding.Strict().DecodeString(data)
		if err != nil || base64.StdEncoding.EncodeToString(result.Bytes) != data {
			return result, argumentIssue("invalid_base64", "/data", "Supply canonical padded base64 or use JSON/UTF-8 input.")
		}
	}
	if err != nil {
		return result, err
	}
	if media, ok := value["media_type"].(string); ok {
		result.MediaType = media
	}
	if len(result.Bytes) > DirectInputLimit {
		return result, argumentIssue("direct_input_too_large", "", "Publish this input as an artifact and pass its returned artifact_receipt; direct input is limited to 32768 bytes.")
	}
	return result, nil
}
func jsonDepth(v any) int {
	depth := 0
	switch x := v.(type) {
	case map[string]any:
		depth = 1
		for _, c := range x {
			if d := 1 + jsonDepth(c); d > depth {
				depth = d
			}
		}
	case []any:
		depth = 1
		for _, c := range x {
			if d := 1 + jsonDepth(c); d > depth {
				depth = d
			}
		}
	}
	return depth
}
func (p *Protocol) checkSimpleAttempt(body map[string]any) error {
	for _, key := range []string{"input", "injection_input"} {
		if value, ok := body[key].(map[string]any); ok {
			if _, err := p.ResolveDirectInput(value); err != nil {
				if issue, ok := err.(*ArgumentError); ok {
					copy := *issue
					copy.FieldPath = "/" + key + issue.FieldPath
					return &copy
				}
				return err
			}
		}
	}
	return nil
}

// NormalizeAssessment resolves only documented defaults. Host-generated binding
// and finish reason are supplied separately; no claims or evidence are inferred.
func (p *Protocol) NormalizeAssessment(raw, binding []byte, finish string) (map[string]any, error) {
	value, err := p.catalog.Validate(AssessmentSchema, raw, ConclusionLimit)
	if err != nil {
		return nil, err
	}
	b, err := p.catalog.Validate(ConclusionBindingSchema, binding, ControlLimit)
	if err != nil {
		return nil, err
	}
	c := value.(map[string]any)
	normalizeAssessmentCollections(c)
	if _, ok := c["status"]; !ok {
		c["status"] = "partial"
	}
	c["api_version"] = "operator.dev/engine-conclusion/v1alpha2"
	c["kind"] = "EngineConclusion"
	c["binding"] = b
	c["finish_reason"] = finish
	normalized, err := json.Marshal(c)
	if err != nil {
		return nil, ErrJSON
	}
	c, err = p.ValidateConclusion(normalized)
	if err != nil {
		return nil, err
	}
	for _, field := range []string{"objectives", "hypotheses"} {
		for i, item := range c[field].([]any) {
			entry := item.(map[string]any)
			if entry["outcome"] == "supported" && len(entry["attempt_receipt_refs"].([]any)) == 0 {
				return nil, conclusionIssue("evidence_required", "/"+field+"/"+jsonIndex(i)+"/attempt_receipt_refs", "")
			}
		}
	}
	return c, nil
}
func jsonIndex(i int) string { b, _ := json.Marshal(i); return string(b) }
func normalizeAssessmentCollections(c map[string]any) {
	for _, key := range []string{"objectives", "hypotheses", "claims", "coverage", "uncertainties", "record_refs"} {
		if _, ok := c[key]; !ok {
			c[key] = []any{}
		}
	}
	for _, field := range []string{"objectives", "hypotheses", "claims", "coverage", "uncertainties"} {
		for _, item := range c[field].([]any) {
			entry := item.(map[string]any)
			fields := map[string][]string{"objectives": {"attempt_receipt_refs", "record_refs"}, "hypotheses": {"objective_refs", "attempt_receipt_refs", "record_refs"}, "claims": {"attempt_receipt_refs", "observation_refs", "uncertainty_refs"}, "coverage": {"attempt_receipt_refs"}, "uncertainties": {"observation_refs", "input_refs", "record_refs"}}[field]
			for _, key := range fields {
				if _, ok := entry[key]; !ok {
					entry[key] = []any{}
				}
			}
		}
	}
}
