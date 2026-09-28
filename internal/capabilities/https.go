package capabilities

import (
	"bytes"
	"encoding/json"
	"sort"

	"github.com/intrusiveai/operator_sandbox/contracts"
	"github.com/intrusiveai/operator_sandbox/internal/httpstarget"
	"github.com/intrusiveai/operator_sandbox/internal/interceptor"
	"github.com/intrusiveai/operator_sandbox/internal/nativedelivery"
)

const HTTPSDeclarationVersion = "operator.dev/https-capabilities/v1alpha1"

type httpsDeclaration struct {
	APIVersion    string                `json:"api_version"`
	TargetID      string                `json:"target_id"`
	MappingDigest string                `json:"mapping_digest"`
	Operations    []OperationCapability `json:"operations"`
}

// FromHTTPS exports only the operation packaging contract and private mapping
// digest. It never exports endpoint, credential references or fixed request data.
func FromHTTPS(c *contracts.Catalog, m *httpstarget.Mapping, target string) (*Export, error) {
	if m == nil {
		return nil, ErrProjection
	}
	source := httpsDeclaration{APIVersion: HTTPSDeclarationVersion, TargetID: target, MappingDigest: m.Digest()}
	for _, op := range m.Settings().Operations {
		input := nativedelivery.Body{MediaType: op.InputMediaType(), Encoding: "utf8", MaxBytes: op.MaximumInputBytes, Basis: "provider-declared"}
		if input.MediaType == "application/json" {
			input.Encoding = "json"
			input.Schema = map[string]any{"description": "Any JSON value."}
		}
		output := nativedelivery.Body{MediaType: "text/plain", Encoding: "utf8", MaxBytes: op.MaximumResponseBytes, Basis: "unknown"}
		// JSON selection can yield any JSON value or text; no asserted output schema.
		if op.Response.Format == "json" {
			output.MediaType = "application/json"
			output.Encoding = "json"
		}
		source.Operations = append(source.Operations, OperationCapability{ID: op.ID, MaximumInputBytes: op.MaximumInputBytes, Delivery: &nativedelivery.Contract{Description: "Administrator-declared HTTPS application operation.", Input: input, Output: output}})
	}
	sort.Slice(source.Operations, func(i, j int) bool { return source.Operations[i].ID < source.Operations[j].ID })
	raw, _ := json.Marshal(source)
	raw, _ = contracts.Canonicalize(raw, contracts.OrdinaryLimit)
	return fromHTTPSDeclaration(c, raw, target)
}
func fromHTTPSDeclaration(c *contracts.Catalog, raw []byte, target string) (*Export, error) {
	var s httpsDeclaration
	if c == nil || interceptor.DecodeTypedBody(raw, &s, contracts.OrdinaryLimit) != nil || s.APIVersion != HTTPSDeclarationVersion || s.TargetID != target || !logicalID.MatchString(target) || !digestPattern.MatchString(s.MappingDigest) || len(s.Operations) == 0 || len(s.Operations) > 64 {
		return nil, ErrProjection
	}
	ops := []any{}
	last := ""
	for _, o := range s.Operations {
		if !logicalID.MatchString(o.ID) || o.ID <= last || o.Method != "" || o.Path != "" || o.Delivery == nil || nativedelivery.Validate(o.Delivery) != nil || o.MaximumInputBytes != o.Delivery.Input.MaxBytes || o.MaximumInputBytes > 1<<20 {
			return nil, ErrProjection
		}
		last = o.ID
		ops = append(ops, map[string]any{"ref": "operation:" + o.ID, "operation_id": o.ID, "maximum_input_bytes": o.MaximumInputBytes, "delivery_status": "described", "delivery": o.Delivery})
	}
	canonical, e := contracts.Canonicalize(raw, contracts.OrdinaryLimit)
	if e != nil || !bytes.Equal(canonical, raw) {
		return nil, ErrProjection
	}
	p := map[string]any{"target_id": target, "adapter": httpstarget.Adapter, "operations": ops, "actions": []any{}, "services": []any{}, "file_namespaces": []any{}, "features": []string{}, "feedback_profiles": []string{"black-box", "diagnostic"}, "evidence_classes": []any{
		map[string]any{"ref": "evidence:operation_error", "feedback_kind": "operation_error", "profiles": []string{"diagnostic"}},
		map[string]any{"ref": "evidence:target_output", "feedback_kind": "target_output", "profiles": []string{"black-box", "diagnostic"}},
	}}
	b, _ := json.Marshal(p)
	digest, e := contracts.CanonicalDigest(b, contracts.OrdinaryLimit)
	if e != nil {
		return nil, e
	}
	result := map[string]any{"schema_version": "operator.dev/target-capability-manifest/v1alpha1", "source": map[string]any{"adapter": httpstarget.Adapter, "schema_version": HTTPSDeclarationVersion, "capability_source_digest": contracts.RawDigest(raw), "raw_digest": contracts.RawDigest(raw)}, "capabilities": p, "capability_projection_digest": digest}
	public, _ := json.Marshal(result)
	if _, e = c.Validate(contracts.TargetCapabilityManifestSchema, public, contracts.OrdinaryLimit); e != nil {
		return nil, e
	}
	return &Export{https: &s, raw: bytes.Clone(raw), public: public, projection: p, projectionDigest: digest}, nil
}
func (e *Export) Adapter() string { return e.projection["adapter"].(string) }

// BindHTTPS is local execution attribution, not a remote session or readiness claim.
func BindHTTPS(c *contracts.Catalog, m *httpstarget.Mapping, target, campaign, worker string) (*Live, error) {
	if !logicalID.MatchString(campaign) || !logicalID.MatchString(worker) {
		return nil, ErrBinding
	}
	e, err := FromHTTPS(c, m, target)
	if err != nil {
		return nil, err
	}
	return &Live{export: e, campaign: campaign, instance: "https-local", binding: interceptor.Binding{SessionID: "https-" + contracts.RawDigest([]byte(campaign))[7:], WorkerInstanceID: worker, RunRevision: 1}, profile: "diagnostic"}, nil
}
