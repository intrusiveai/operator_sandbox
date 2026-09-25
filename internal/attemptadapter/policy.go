// Package attemptadapter translates one validated harness attempt into a fixed
// Interceptor plan. Only installed host policy supplies scopes and routes.
package attemptadapter

import (
	"encoding/json"
	"errors"
	"net/textproto"
	"path"
	"slices"
	"strings"

	"github.com/intrusive-ai/operator-sandbox/internal/capabilities"
	"github.com/intrusive-ai/operator-sandbox/internal/interceptor"
)

var ErrPolicy = errors.New("attempt is outside installed target scope")
var ErrAttempt = errors.New("attempt arguments, artifacts or lineage are invalid")
var ErrResult = errors.New("native result does not match the authorized attempt step")

// Route is one concrete host-reviewed injection target. Empty fields are exact
// empty values, not wildcards. Request predicates may narrow this route further.
// The administration/configuration layer resolves broader user scopes into these
// records; no model or submitted bundle can grant a route.
type Route struct {
	Surface     string   `json:"surface"`
	Target      Target   `json:"target"`
	Scopes      []string `json:"scopes"`
	Placements  []string `json:"placements"`
	Pointers    []string `json:"pointers"`
	MergeFields []string `json:"merge_fields,omitempty"`
}
type Target struct {
	ToolName      string `json:"tool_name,omitempty"`
	Service       string `json:"service,omitempty"`
	Method        string `json:"method,omitempty"`
	Path          string `json:"path,omitempty"`
	Collection    string `json:"collection,omitempty"`
	Key           string `json:"key,omitempty"`
	FileNamespace string `json:"file_namespace,omitempty"`
	RelativePath  string `json:"relative_path,omitempty"`
}
type Scopes struct {
	OperationIDs            []string `json:"operation_ids"`
	CallerPrincipalIDs      []string `json:"caller_principal_ids"`
	Routes                  []Route  `json:"routes"`
	AllowRetainedInjections bool     `json:"allow_retained_injections"`
}
type Policy struct {
	scopes           Scopes
	facts            capabilities.ExecutionFacts
	capabilityPolicy capabilities.Policy
	sourceDigest     string
	raw              []byte
}

// Resolve checks concrete routes against verified native capabilities and derives
// the capability policy used by bundle admission. Evidence permission is explicit.
func Resolve(e *capabilities.Export, scopes Scopes, evidenceKinds []string, ceiling string) (*Policy, error) {
	if e == nil {
		return nil, ErrPolicy
	}
	raw, err := json.Marshal(scopes)
	if err != nil {
		return nil, ErrPolicy
	}
	var copy Scopes
	_ = json.Unmarshal(raw, &copy)
	facts := e.ExecutionFacts()
	p := &Policy{scopes: copy, facts: facts, sourceDigest: e.SourceDigest()}
	cp := capabilities.Policy{AllowedRefs: []string{}, SelectableActionRefs: []string{}, FeedbackCeiling: ceiling, FeedbackKinds: append([]string{}, evidenceKinds...)}
	if ceiling != "" && !slices.Contains([]string{"black-box", "diagnostic", "oracle-assisted"}, ceiling) {
		return nil, ErrPolicy
	}
	for _, id := range copy.OperationIDs {
		found := false
		for _, op := range facts.Operations {
			if op.ID == id && op.Delivery != nil {
				found = true
			}
		}
		if !found || slices.Contains(cp.AllowedRefs, "operation:"+id) {
			return nil, ErrPolicy
		}
		cp.AllowedRefs = append(cp.AllowedRefs, "operation:"+id)
	}
	for _, r := range copy.Routes {
		if !validRoute(facts, r) {
			return nil, ErrPolicy
		}
		ref := "action:injection:" + r.Surface
		if !slices.Contains(cp.AllowedRefs, ref) {
			cp.AllowedRefs = append(cp.AllowedRefs, ref)
			cp.SelectableActionRefs = append(cp.SelectableActionRefs, ref)
		}
		if r.Target.Service != "" {
			cp.AllowedRefs = appendUnique(cp.AllowedRefs, "service:"+r.Target.Service)
		}
		if r.Target.FileNamespace != "" {
			cp.AllowedRefs = appendUnique(cp.AllowedRefs, "file_namespace:"+r.Target.FileNamespace)
		}
	}
	for _, k := range evidenceKinds {
		if !slices.Contains([]string{"target_output", "operation_error", "injection_delivery", "oracle_outcome"}, k) || slices.Contains(cp.AllowedRefs, "evidence:"+k) {
			return nil, ErrPolicy
		}
		cp.AllowedRefs = append(cp.AllowedRefs, "evidence:"+k)
	}
	for _, id := range copy.CallerPrincipalIDs {
		if !identifier.MatchString(id) {
			return nil, ErrPolicy
		}
	}
	p.capabilityPolicy = cp
	p.raw, _ = json.Marshal(map[string]any{"scopes": copy, "capabilities": cp, "source_digest": p.sourceDigest})
	return p, nil
}
func (p *Policy) CapabilityPolicy() capabilities.Policy {
	var c capabilities.Policy
	b, _ := json.Marshal(p.capabilityPolicy)
	_ = json.Unmarshal(b, &c)
	return c
}
func (p *Policy) RecordJSON() []byte { return slices.Clone(p.raw) }
func appendUnique(a []string, s string) []string {
	if !slices.Contains(a, s) {
		return append(a, s)
	}
	return a
}
func nativePlacement(s string) string {
	return map[string]string{"replace": "replace", "prepend_text": "prepend", "append_text": "append", "insert_array": "insert", "merge_object": "merge"}[s]
}
func cleanRelative(s string) bool {
	return s != "" && s != "." && s != ".." && !strings.HasPrefix(s, "/") && !strings.HasPrefix(s, "../") && path.Clean(s) == s && !strings.ContainsAny(s, "\\\x00\r\n")
}
func cleanAbsolute(s string) bool {
	return strings.HasPrefix(s, "/") && path.Clean(s) == s && !strings.ContainsAny(s, "\\%?#\x00\r\n")
}
func validRoute(f capabilities.ExecutionFacts, r Route) bool {
	if len(r.Scopes) == 0 || len(r.Placements) == 0 || len(r.Pointers) == 0 {
		return false
	}
	var profile *capabilities.InjectionProfile
	for i := range f.InjectionProfiles {
		if f.InjectionProfiles[i].Surface == r.Surface {
			profile = &f.InjectionProfiles[i]
		}
	}
	if profile == nil || !slices.Contains(f.NativeOperations, "injection.arm") || !slices.Contains(f.NativeOperations, "injection.delete") {
		return false
	}
	for _, s := range r.Scopes {
		if !slices.Contains(profile.Scopes, s) {
			return false
		}
	}
	for _, s := range r.Placements {
		if !slices.Contains(profile.Placements, nativePlacement(s)) {
			return false
		}
	}
	for _, pointer := range r.Pointers {
		if !validPointer(pointer) {
			return false
		}
	}
	t := r.Target
	switch r.Surface {
	case "model_tool_result":
		return t.ToolName != "" && t == (Target{ToolName: t.ToolName})
	case "mcp_tool_result":
		if t != (Target{Service: t.Service, ToolName: t.ToolName}) || t.ToolName == "" {
			return false
		}
	case "service_response":
		if t != (Target{Service: t.Service, Method: t.Method, Path: t.Path}) || t.Method == "" || !cleanAbsolute(t.Path) {
			return false
		}
	case "environment_state":
		if t.FileNamespace != "" {
			if t != (Target{FileNamespace: t.FileNamespace, RelativePath: t.RelativePath}) || !cleanRelative(t.RelativePath) {
				return false
			}
			for _, n := range f.FileNamespaces {
				if n.ID == t.FileNamespace {
					return true
				}
			}
			return false
		}
		if t != (Target{Service: t.Service, Collection: t.Collection, Key: t.Key}) || t.Collection == "" || t.Key == "" || t.Key == "." || t.Key == ".." || strings.ContainsAny(t.Key, "/\\\x00\r\n") {
			return false
		}
	default:
		return false
	}
	for _, svc := range f.Services {
		if svc.ID != t.Service || !slices.Contains(svc.InjectionSurfaces, r.Surface) {
			continue
		}
		for _, ep := range svc.Endpoints {
			switch r.Surface {
			case "mcp_tool_result":
				if ep.Kind == "mcp-tool" && ep.ID == t.ToolName {
					return true
				}
			case "service_response":
				if ep.Kind == "http-route" && ep.Method == t.Method && ep.Path == t.Path {
					return true
				}
			case "environment_state":
				if ep.Collection == t.Collection {
					return true
				}
			}
		}
		if r.Surface == "environment_state" {
			for _, collection := range svc.CreatableCollections {
				if collection.Collection == t.Collection {
					return true
				}
			}
		}
	}
	return false
}
func validPointer(s string) bool {
	if len(s) > 2048 || s != "" && !strings.HasPrefix(s, "/") {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < 32 || s[i] == 127 {
			return false
		}
		if s[i] == '~' {
			i++
			if i >= len(s) || s[i] != '0' && s[i] != '1' {
				return false
			}
		}
	}
	return true
}
func (p *Policy) authorizeInjection(d interceptor.Definition, knownTurns []string) error {
	s := d.Selector
	if s.TurnID != "" && !slices.Contains(knownTurns, s.TurnID) {
		return ErrPolicy
	}
	if s.Path != "" && !cleanAbsolute(s.Path) || s.RelativePath != "" && !cleanRelative(s.RelativePath) {
		return ErrPolicy
	}
	seen := map[string]bool{}
	for k := range s.HeadersEqual {
		name := textproto.CanonicalMIMEHeaderKey(k)
		if name == "" || seen[name] || strings.ContainsAny(k, " \t:") || slices.Contains([]string{"Authorization", "Proxy-Authorization", "Cookie", "Set-Cookie", "Host", "Connection", "Transfer-Encoding", "Content-Length", "Forwarded", "X-Forwarded-Host", "X-Forwarded-For", "X-Forwarded-Proto"}, name) {
			return ErrPolicy
		}
		seen[name] = true
	}
	t := Target{ToolName: s.ToolName, Service: s.Service, Method: s.Method, Path: s.Path, Collection: s.Collection, Key: s.Key, FileNamespace: s.FileNamespace, RelativePath: s.RelativePath}
	for _, r := range p.scopes.Routes {
		if r.Surface != d.Surface || r.Target != t || !slices.Contains(r.Scopes, d.Scope) || !slices.Contains(r.Placements, d.Placement.Operation) || !slices.Contains(r.Pointers, d.Placement.Pointer) {
			continue
		}
		for _, k := range d.Placement.AllowedFields {
			if !slices.Contains(r.MergeFields, k) {
				return ErrPolicy
			}
		}
		return nil
	}
	return ErrPolicy
}
