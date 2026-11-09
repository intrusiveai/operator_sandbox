// Package operationpolicy owns host operating defaults, outside the wire contract.
package operationpolicy

import (
	"encoding/json"
	"errors"
	"sync"

	"github.com/intrusiveai/operator_sandbox/contracts"
	"github.com/intrusiveai/operator_sandbox/schemas"
)

// MaximumMS is the host's supported campaign lifetime, not a registry timeout.
const MaximumMS int64 = 1800000

// DefaultModelTimeoutMS is the host configuration default and the fallback for
// direct callers without a deadline, never an additional provider ceiling.
const DefaultModelTimeoutMS int64 = 120000

var ErrSettings = errors.New("operation_timeouts_ms must map registered operation names to positive milliseconds within the supported campaign lifetime")
var protocol = sync.OnceValues(func() (*contracts.Protocol, error) { return contracts.LoadProtocol(schemas.Files) })

func Resolve(overrides map[string]int64) (map[string]int64, error) {
	p, err := protocol()
	if err != nil {
		return nil, err
	}
	values := map[string]int64{}
	for _, op := range p.Operations() {
		values[op.Name] = 30000
	}
	values["engine.attempt_execute"] = 120000
	values["engine.model_generate"] = DefaultModelTimeoutMS
	for _, name := range []string{"engine.snapshot_request", "engine.restore_request"} {
		values[name] = 300000
	}
	for name, ms := range overrides {
		if _, ok := values[name]; !ok || ms < 1 || ms > MaximumMS {
			return nil, ErrSettings
		}
		values[name] = ms
	}
	return values, nil
}

// Select freezes exactly the advertised operation set, not unrelated defaults.
func Select(operations []string, overrides map[string]int64) (map[string]int64, error) {
	values, err := Resolve(overrides)
	if err != nil {
		return nil, err
	}
	selected := map[string]int64{}
	for _, name := range operations {
		n, ok := values[name]
		if !ok {
			return nil, ErrSettings
		}
		selected[name] = n
	}
	return selected, nil
}
func FromContext(raw []byte) map[string]int64 {
	var v struct {
		Timeouts map[string]int64 `json:"operation_timeouts_ms"`
	}
	_ = json.Unmarshal(raw, &v)
	return v.Timeouts
}
