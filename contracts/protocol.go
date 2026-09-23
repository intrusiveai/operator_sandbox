package contracts

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"io/fs"
	"sort"
	"strconv"
	"time"
)

// ErrProtocol reports an inconsistent message without exposing submitted values.
var ErrProtocol = errors.New("contract message consistency check failed")

const SpoolAckLimit = 1024

// Operation is installed dispatch metadata, not authority supplied by the guest.
type Operation struct {
	Name             string   `json:"name"`
	RequestSchema    string   `json:"request_schema"`
	ResultSchema     string   `json:"result_schema"`
	ErrorSchema      string   `json:"error_schema"`
	ErrorCodes       []string `json:"error_codes"`
	LifecycleStates  []string `json:"lifecycle_states"`
	FinalizationRule string   `json:"finalization_rule"`
	TimeoutMS        int64    `json:"timeout_ms"`
	MaxRequestBytes  int      `json:"max_request_bytes"`
	MaxResultBytes   int      `json:"max_result_bytes"`
	Effect           string   `json:"effect"`
	ReceiptPolicy    string   `json:"receipt_policy"`
}

// Protocol checks ordinary message structure and stateless correlations. Admission,
// sequence tracking, deduplication and durable effects belong to the runtime.
type Protocol struct {
	catalog    *Catalog
	operations map[string]Operation
}

// ValidateAck checks syntax and size. Launch matching and monotonic positions
// require the transport's saved state and are not inferred from a single ACK.
func (p *Protocol) ValidateAck(raw []byte) (map[string]any, error) {
	value, err := p.catalog.Validate(EngineSpoolAckSchema, raw, SpoolAckLimit)
	if err != nil {
		return nil, err
	}
	return value.(map[string]any), nil
}

func LoadProtocol(files fs.FS) (*Protocol, error) {
	catalog, err := LoadCatalog(files)
	if err != nil {
		return nil, err
	}
	raw, err := fs.ReadFile(files, "operations.json")
	if err != nil {
		return nil, ErrCatalog
	}
	value, err := catalog.Validate(OperationRegistrySchema, raw, OrdinaryLimit)
	if err != nil {
		return nil, ErrCatalog
	}
	result := &Protocol{catalog: catalog, operations: map[string]Operation{}}
	previous := ""
	for _, entry := range value.(map[string]any)["operations"].([]any) {
		fields := entry.(map[string]any)
		op := Operation{
			Name:             fields["name"].(string),
			RequestSchema:    fields["request_schema"].(string),
			ResultSchema:     fields["result_schema"].(string),
			ErrorSchema:      fields["error_schema"].(string),
			FinalizationRule: fields["finalization_rule"].(string),
			TimeoutMS:        number(fields["timeout_ms"]),
			MaxRequestBytes:  int(number(fields["max_request_bytes"])),
			MaxResultBytes:   int(number(fields["max_result_bytes"])),
			Effect:           fields["effect"].(string),
			ReceiptPolicy:    fields["receipt_policy"].(string),
		}
		for _, code := range fields["error_codes"].([]any) {
			op.ErrorCodes = append(op.ErrorCodes, code.(string))
		}
		for _, state := range fields["lifecycle_states"].([]any) {
			op.LifecycleStates = append(op.LifecycleStates, state.(string))
		}
		if op.Name <= previous {
			return nil, ErrCatalog
		}
		previous = op.Name
		for _, id := range []string{op.RequestSchema, op.ResultSchema, op.ErrorSchema} {
			if _, found := catalog.schemas[id]; !found {
				return nil, ErrCatalog
			}
		}
		result.operations[op.Name] = op
	}
	return result, nil
}

// Operations returns a copy of installed metadata; callers cannot mutate policy.
func (p *Protocol) Operations() []Operation {
	result := make([]Operation, 0, len(p.operations))
	for _, op := range p.operations {
		op.ErrorCodes = append([]string(nil), op.ErrorCodes...)
		op.LifecycleStates = append([]string(nil), op.LifecycleStates...)
		result = append(result, op)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Name < result[j].Name })
	return result
}

func (p *Protocol) ValidateRequest(raw []byte) (map[string]any, error) {
	value, err := p.catalog.Validate(EnginePipeRequestSchema, raw, OrdinaryLimit)
	if err != nil {
		return nil, err
	}
	message := value.(map[string]any)
	op, found := p.operations[message["operation"].(string)]
	if !found {
		return nil, ErrCatalog
	}
	if len(raw) > op.MaxRequestBytes {
		return nil, ErrLimit
	}
	// Validate against the installed registry as well as the generated envelope.
	if err = p.catalog.schemas[op.RequestSchema].Validate(message["body"]); err != nil {
		return nil, ErrSchema
	}
	body := message["body"].(map[string]any)
	valid := true
	switch op.Name {
	case "engine.attempt_execute":
		valid = same(body["request_id"], message["operation_id"])
	case "engine.artifact_begin":
		artifact := body["artifact"].(map[string]any)
		if body["purpose"] == "conclusion" {
			valid = number(artifact["size_bytes"]) <= 1<<20 && artifact["media_type"] == "application/json"
		}
	case "engine.artifact_put_part":
		data, ok := part(body["content"])
		valid = ok && len(data) > 0 && number(body["offset"])+int64(len(data)) <= 16<<20
	case "engine.snapshot_request":
		valid = descriptionOK(body)
	}
	if !valid {
		return nil, ErrProtocol
	}
	return message, nil
}

// ValidateResponse binds a response to its original request, including the old
// revision on a restore response. Request/response sequences are independent.
func (p *Protocol) ValidateResponse(request, response []byte) (map[string]any, error) {
	req, err := p.ValidateRequest(request)
	if err != nil {
		return nil, err
	}
	value, err := p.catalog.Validate(EnginePipeResponseSchema, response, OrdinaryLimit)
	if err != nil {
		return nil, err
	}
	message := value.(map[string]any)
	for _, key := range []string{"campaign_id", "launch_id", "run_revision", "call_id", "operation_id", "operation"} {
		if !same(req[key], message[key]) {
			return nil, ErrProtocol
		}
	}
	op := p.operations[req["operation"].(string)]
	if len(response) > op.MaxResultBytes {
		return nil, ErrLimit
	}
	if failure, found := message["error"]; found {
		if p.catalog.schemas[op.ErrorSchema].Validate(failure) != nil {
			return nil, ErrSchema
		}
		code := failure.(map[string]any)["code"].(string)
		for _, allowed := range op.ErrorCodes {
			if code == allowed {
				return message, nil
			}
		}
		return nil, ErrProtocol
	}
	result := message["result"].(map[string]any)
	if p.catalog.schemas[op.ResultSchema].Validate(result) != nil {
		return nil, ErrSchema
	}
	body := req["body"].(map[string]any)
	valid := true
	switch op.Name {
	case "engine.artifact_begin":
		valid = same(result["purpose"], body["purpose"]) && sameArtifact(result["artifact"], body["artifact"])
	case "engine.artifact_put_part":
		data, _ := part(body["content"])
		valid = same(result["upload_id"], body["upload_id"]) && same(result["offset"], body["offset"]) && number(result["raw_length"]) == int64(len(data)) && number(result["next_offset"]) == number(body["offset"])+int64(len(data))
	case "engine.artifact_commit":
		valid = same(result["upload_id"], body["upload_id"])
		artifact := result["artifact"].(map[string]any)
		if result["purpose"] == "conclusion" {
			valid = valid && number(artifact["size_bytes"]) <= 1<<20 && artifact["media_type"] == "application/json"
		}
	case "engine.attempt_execute":
		if id, ok := result["request_id"]; ok {
			valid = same(id, req["operation_id"])
		}
		if id, ok := result["attempt_id"]; ok {
			valid = valid && same(id, body["attempt_id"])
		}
	case "engine.injection_delete":
		valid = same(result["attempt_receipt_id"], body["attempt_receipt_id"]) && same(result["action_id"], body["action_id"])
	case "engine.observation_read":
		data, ok := part(result["content"])
		valid = ok && same(result["receipt_id"], body["receipt_id"]) && same(result["entry_id"], body["entry_id"]) && same(result["offset"], body["offset"]) && number(result["raw_length"]) == int64(len(data)) && int64(len(data)) <= number(body["max_bytes"])
		if result["availability"] == "available" {
			end := number(result["offset"]) + int64(len(data))
			size := number(result["artifact"].(map[string]any)["size_bytes"])
			valid = valid && end <= size && result["eof"] == (end == size) && (len(data) > 0 || end == size)
		}
	case "engine.snapshot_request", "engine.snapshot_inspect", "engine.restore_request":
		snapshot := result["snapshot"].(map[string]any)
		valid = metadataOK(snapshot, req["campaign_id"])
		if op.Name == "engine.snapshot_request" {
			valid = valid && snapshot["label"] == optionalText(body, "label") && snapshot["description"] == optionalText(body, "description")
		} else {
			valid = valid && same(snapshot["source_session"], body["source_session"]) && same(snapshot["checkpoint_id"], body["checkpoint_id"])
		}
		if op.Name == "engine.restore_request" {
			// An exact duplicate can return a transition from an earlier binding.
			// The ledger must establish the original effect revision before applying it.
			valid = valid && number(result["run_revision"]) > number(result["previous_run_revision"])
		}
	case "engine.snapshot_list":
		valid = listOK(body, result, req["campaign_id"])
	case "engine.request_stop":
		valid = same(result["conclusion_state"], body["conclusion"].(map[string]any)["state"])
	}
	if !valid {
		return nil, ErrProtocol
	}
	return message, nil
}

func number(value any) int64 {
	n, _ := strconv.ParseFloat(string(value.(json.Number)), 64)
	return int64(n) // Already validated as a safe integer.
}
func same(left, right any) bool {
	if _, ok := left.(json.Number); ok {
		_, ok = right.(json.Number)
		return ok && number(left) == number(right)
	}
	return left == right // Scalar correlation fields only.
}
func optionalText(object map[string]any, key string) string {
	if value, ok := object[key]; ok {
		return value.(string)
	}
	return ""
}
func descriptionOK(object map[string]any) bool {
	return len(optionalText(object, "description")) <= 4096
}
func part(value any) ([]byte, bool) {
	encoded := value.(string)
	decoded, err := base64.StdEncoding.Strict().DecodeString(encoded)
	return decoded, err == nil && len(decoded) <= 262144 && base64.StdEncoding.EncodeToString(decoded) == encoded
}
func sameArtifact(left, right any) bool {
	l, r := left.(map[string]any), right.(map[string]any)
	for _, key := range []string{"digest", "size_bytes", "media_type", "canonicalization"} {
		if !same(l[key], r[key]) {
			return false
		}
	}
	return true
}
func metadataOK(snapshot map[string]any, campaign any) bool {
	_, err := time.Parse(time.RFC3339Nano, snapshot["created_at"].(string))
	return err == nil && snapshot["created_at"].(string)[:4] != "0000" && same(snapshot["campaign_id"], campaign) && descriptionOK(snapshot)
}
func listOK(body, result map[string]any, campaign any) bool {
	offset, limit := int64(0), int64(50)
	if value, ok := body["offset"]; ok {
		offset = number(value)
	}
	if value, ok := body["limit"]; ok {
		limit = number(value)
	}
	items := result["snapshots"].([]any)
	if !same(result["campaign_id"], campaign) || number(result["offset"]) != offset || int64(len(items)) > limit {
		return false
	}
	seen := map[string]bool{}
	for _, item := range items {
		snapshot := item.(map[string]any)
		key := snapshot["source_session"].(string) + "/" + snapshot["checkpoint_id"].(string)
		if seen[key] || !metadataOK(snapshot, campaign) {
			return false
		}
		seen[key] = true
		if source, ok := body["source_session"]; ok && !same(source, snapshot["source_session"]) {
			return false
		}
	}
	end, total := offset+int64(len(items)), number(result["total"])
	if len(items) > 0 && end > total {
		return false
	}
	next, hasNext := result["next_offset"]
	if end < total {
		return len(items) > 0 && hasNext && number(next) == end
	}
	return !hasNext
}
