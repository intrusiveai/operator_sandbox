// Package campaignlimits resolves administrator defaults and bundle narrowing.
// It supplies immutable initial budgets; accounting and admission remain separate.
package campaignlimits

import (
	"encoding/json"
	"errors"
	"sync"

	"github.com/intrusiveai/operator_sandbox/contracts"
	"github.com/intrusiveai/operator_sandbox/schemas"
)

var ErrLimits = errors.New("invalid campaign limits")
var localProtocol = sync.OnceValues(func() (*contracts.Protocol, error) { return contracts.LoadProtocol(schemas.Files) })
var hostFields = map[string]string{
	"max_attempt_admissions":  "attempt_admissions",
	"max_active_seconds":      "campaign_time_ms",
	"max_model_tokens":        "model_tokens",
	"max_artifact_bytes":      "artifact_bytes",
	"max_artifact_objects":    "artifact_objects",
	"max_snapshot_admissions": "snapshot_admissions",
	"max_snapshot_bytes":      "snapshot_bytes",
	"max_observation_reads":   "observation_reads",
}

func defaults() map[string]int64 {
	return map[string]int64{
		"max_attempt_admissions": 100, "max_active_seconds": 1800, "max_model_tokens": 250000,
		"max_artifact_bytes": 1 << 30, "max_artifact_objects": 4096,
		"max_snapshot_admissions": 20, "max_snapshot_bytes": 1 << 30, "max_observation_reads": 2000,
	}
}

type Host struct{ values, harness map[string]int64 }

func (h Host) MarshalJSON() ([]byte, error) {
	if h.values == nil || h.harness == nil {
		return nil, ErrLimits
	}
	out := map[string]any{"harness": h.harness}
	for k, v := range h.values {
		out[k] = v
	}
	return json.Marshal(out)
}
func integer(v any, zero bool) (int64, error) {
	n, ok := v.(json.Number)
	if !ok {
		return 0, ErrLimits
	}
	f, err := n.Float64()
	if err != nil || f < 0 || f > contracts.MaxSafeInteger || float64(int64(f)) != f || (!zero && f == 0) {
		return 0, ErrLimits
	}
	return int64(f), nil
}
func object(raw []byte) (map[string]any, error) {
	v, err := contracts.Decode(raw, contracts.ControlLimit)
	m, ok := v.(map[string]any)
	if err != nil || !ok {
		return nil, ErrLimits
	}
	return m, nil
}

// ParseHost accepts closed administrator overrides. Only snapshot counters accept
// zero. Active duration can be narrowed from the MVP's 30-minute hard ceiling.
func ParseHost(raw []byte) (Host, error) {
	m, err := object(raw)
	if err != nil {
		return Host{}, err
	}
	values := defaults()
	harness := []byte(`{}`)
	for k, v := range m {
		if k == "harness" {
			harness, err = json.Marshal(v)
			if err != nil {
				return Host{}, ErrLimits
			}
			continue
		}
		if _, ok := hostFields[k]; !ok {
			return Host{}, ErrLimits
		}
		n, err := integer(v, k == "max_snapshot_admissions" || k == "max_snapshot_bytes")
		if err != nil {
			return Host{}, err
		}
		if k == "max_active_seconds" && n > 1800 {
			return Host{}, ErrLimits
		}
		values[k] = n
	}
	p, err := localProtocol()
	if err != nil {
		return Host{}, err
	}
	limits, err := p.ResolveHarnessLimits(harness, []byte(`{}`))
	if err != nil {
		return Host{}, ErrLimits
	}
	return Host{values, limits}, nil
}

type Resolved struct{ Campaign, Harness map[string]int64 }

// Resolve applies the submitted requested_limits object only as a cap. The maps
// are fresh per call. No request can raise an administrator budget or restore it.
func Resolve(p *contracts.Protocol, host Host, requested []byte) (Resolved, error) {
	if p == nil || host.values == nil || host.harness == nil {
		return Resolved{}, ErrLimits
	}
	m, err := object(requested)
	if err != nil {
		return Resolved{}, err
	}
	campaign := map[string]int64{}
	for key, wire := range hostFields {
		campaign[wire] = host.values[key]
	}
	campaign["campaign_time_ms"] *= 1000
	harnessCaps := []byte(`{}`)
	fields := map[string]string{"attempt_admissions": "attempt_admissions", "active_seconds": "campaign_time_ms", "model_tokens": "model_tokens", "snapshot_creations": "snapshot_admissions", "snapshot_committed_bytes": "snapshot_bytes"}
	for key, value := range m {
		if key == "harness" {
			harnessCaps, err = json.Marshal(value)
			if err != nil {
				return Resolved{}, ErrLimits
			}
			continue
		}
		name, ok := fields[key]
		if !ok {
			return Resolved{}, ErrLimits
		}
		n, err := integer(value, false)
		if err != nil {
			return Resolved{}, err
		}
		if key == "active_seconds" {
			n = min(n, 1800) * 1000
		}
		campaign[name] = min(campaign[name], n)
	}
	overrides, err := json.Marshal(host.harness)
	if err != nil {
		return Resolved{}, ErrLimits
	}
	harness, err := p.ResolveHarnessLimits(overrides, harnessCaps)
	if err != nil {
		return Resolved{}, ErrLimits
	}
	campaign["model_turns"] = harness["max_model_turns"]
	campaign["observation_bytes"] = harness["max_read_bytes"]
	raw, err := json.Marshal(campaign)
	if err != nil {
		return Resolved{}, ErrLimits
	}
	if _, err = p.Catalog().Validate(contracts.RemainingLimitsSchema, raw, contracts.ControlLimit); err != nil {
		return Resolved{}, ErrLimits
	}
	return Resolved{campaign, harness}, nil
}
