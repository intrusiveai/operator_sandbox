//go:build linux || darwin

package reporting

import (
	"encoding/json"
	"slices"

	"github.com/intrusiveai/operator_sandbox/contracts"
	"github.com/intrusiveai/operator_sandbox/internal/attemptadapter"
	"github.com/intrusiveai/operator_sandbox/internal/campaign"
	"github.com/intrusiveai/operator_sandbox/internal/interceptor"
)

type RouteCoverage struct {
	Digest  string `json:"route_digest"`
	Surface string `json:"surface"`
	State   string `json:"state"`
}
type routeCoverage struct {
	scopes     attemptadapter.Scopes
	operations map[string]bool
	routes     map[string]bool
}

func (c *routeCoverage) prepare(raw []byte) error {
	var v struct {
		Target struct {
			Scopes attemptadapter.Scopes `json:"scopes"`
		} `json:"target_profile"`
	}
	if json.Unmarshal(raw, &v) != nil {
		return campaign.ErrCorrupt
	}
	c.scopes = v.Target.Scopes
	c.operations = map[string]bool{}
	c.routes = map[string]bool{}
	return nil
}
func (c *routeCoverage) step(a *campaign.NativeRecovery, event campaign.Event) error {
	var v struct {
		Step campaign.NativeStep `json:"native_step"`
	}
	if json.Unmarshal(event.Metadata, &v) != nil {
		return campaign.ErrCorrupt
	}
	step := v.Step
	if step.State != campaign.Dispatched && step.State != campaign.ResultCommitted && step.State != campaign.Unknown {
		return nil
	}
	if step.Operation != "application.invoke" && step.Operation != "injection.arm" {
		return nil
	}
	raw := []byte{}
	for _, d := range step.Request {
		if len(raw)+int(d.SizeBytes) > campaign.DerivedManifestLimit {
			return campaign.ErrQuota
		}
		part, e := a.ReadContent(d)
		if e != nil {
			return e
		}
		raw = append(raw, part...)
	}
	var envelope struct {
		Body json.RawMessage `json:"body"`
	}
	if json.Unmarshal(raw, &envelope) != nil {
		return campaign.ErrCorrupt
	}
	if step.Operation == "application.invoke" {
		var turn interceptor.TurnRequest
		if json.Unmarshal(envelope.Body, &turn) != nil {
			return campaign.ErrCorrupt
		}
		if c.operations != nil {
			c.operations[turn.Operation] = true
		}
		return nil
	}
	var body struct {
		Definition interceptor.Definition `json:"definition"`
	}
	if json.Unmarshal(envelope.Body, &body) != nil {
		return campaign.ErrCorrupt
	}
	d := body.Definition
	s := d.Selector
	target := attemptadapter.Target{ToolName: s.ToolName, Service: s.Service, Method: s.Method, Path: s.Path, Collection: s.Collection, Key: s.Key, FileNamespace: s.FileNamespace, RelativePath: s.RelativePath}
	for _, r := range c.scopes.Routes {
		if r.Surface != d.Surface || r.Target != target || !slices.Contains(r.Scopes, d.Scope) || !slices.Contains(r.Placements, d.Placement.Operation) || !slices.Contains(r.Pointers, d.Placement.Pointer) {
			continue
		}
		match := true
		for _, field := range d.Placement.AllowedFields {
			if !slices.Contains(r.MergeFields, field) {
				match = false
			}
		}
		if match {
			raw, e := canonical(r)
			if e != nil {
				return e
			}
			c.routes[contracts.RawDigest(raw)] = true
		}
	}
	return nil
}
func (c *routeCoverage) summarize() ([]string, []RouteCoverage) {
	operations := []string{}
	routes := []RouteCoverage{}
	for _, id := range c.scopes.OperationIDs {
		if !c.operations[id] {
			operations = append(operations, id)
		}
	}
	for _, route := range c.scopes.Routes {
		raw, _ := canonical(route)
		digest := contracts.RawDigest(raw)
		state := "not-attempted"
		if c.routes[digest] {
			state = "arm-attempted"
		}
		routes = append(routes, RouteCoverage{digest, route.Surface, state})
	}
	slices.Sort(operations)
	return operations, routes
}
