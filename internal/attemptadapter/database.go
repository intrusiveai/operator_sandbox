package attemptadapter

import (
	"bytes"
	"encoding/json"
	"slices"
	"strconv"
	"strings"

	"github.com/intrusiveai/operator_sandbox/contracts"
	"github.com/intrusiveai/operator_sandbox/internal/capabilities"
	"github.com/intrusiveai/operator_sandbox/internal/interceptor"
)

func databaseService(f capabilities.ExecutionFacts, id string) *capabilities.ServiceCapability {
	for i := range f.Services {
		s := &f.Services[i]
		if s.ID == id && s.Kind == "postgresql" {
			return s
		}
	}
	return nil
}
func databaseTable(s capabilities.ServiceCapability, id string) *capabilities.DatabaseTableCapability {
	for i := range s.DatabaseTables {
		if s.DatabaseTables[i].ID == id {
			return &s.DatabaseTables[i]
		}
	}
	return nil
}
func databaseKey(raw string, t capabilities.DatabaseTableCapability) ([]byte, bool) {
	if len(raw) > 4096 {
		return nil, false
	}
	value, err := contracts.Decode([]byte(raw), 4096)
	if err != nil {
		return nil, false
	}
	key, ok := value.(map[string]any)
	if !ok || len(key) != len(t.Keys) {
		return nil, false
	}
	for _, name := range t.Keys {
		switch key[name].(type) {
		case string, json.Number, bool:
		default:
			return nil, false
		}
	}
	canonical, err := contracts.Canonicalize([]byte(raw), 4096)
	return canonical, err == nil
}
func sameTarget(f capabilities.ExecutionFacts, a, b Target) bool {
	s := databaseService(f, a.Service)
	if s == nil || a.Collection == "" {
		return a == b
	}
	table := databaseTable(*s, a.Collection)
	if table == nil {
		return false
	}
	ka, ok := databaseKey(a.Key, *table)
	if !ok {
		return false
	}
	kb, ok := databaseKey(b.Key, *table)
	if !ok {
		return false
	}
	a.Key = ""
	b.Key = ""
	return a == b && bytes.Equal(ka, kb)
}
func pointerParts(pointer string) []string {
	if pointer == "" || !validPointer(pointer) {
		return nil
	}
	parts := strings.Split(pointer[1:], "/")
	for i := range parts {
		parts[i] = strings.ReplaceAll(strings.ReplaceAll(parts[i], "~1", "/"), "~0", "~")
	}
	return parts
}
func validDatabaseRoute(s capabilities.ServiceCapability, r Route) bool {
	if r.Surface == "database_response" {
		for _, p := range r.Pointers {
			parts := pointerParts(p)
			if len(parts) < 3 || parts[0] != "rows" {
				return false
			}
			for _, index := range parts[1:3] {
				n, err := strconv.ParseUint(index, 10, 32)
				if err != nil || strconv.FormatUint(n, 10) != index {
					return false
				}
			}
		}
		return true
	}
	if r.Surface != "environment_state" {
		return false
	}
	table := databaseTable(s, r.Target.Collection)
	if table == nil {
		return false
	}
	if _, ok := databaseKey(r.Target.Key, *table); !ok {
		return false
	}
	for _, p := range r.Pointers {
		if p == "" {
			if !table.AllowCreate || len(r.Placements) != 1 || r.Placements[0] != "replace" {
				return false
			}
			continue
		}
		parts := pointerParts(p)
		if len(parts) == 0 || !slices.Contains(table.Fields, parts[0]) {
			return false
		}
	}
	return true
}
func (p *Policy) validateDatabaseInjection(d interceptor.Definition) error {
	s := databaseService(p.facts, d.Selector.Service)
	if s == nil || d.Surface != "environment_state" {
		return nil
	}
	table := databaseTable(*s, d.Selector.Collection)
	if table == nil {
		return ErrPolicy
	}
	key, ok := databaseKey(d.Selector.Key, *table)
	if !ok {
		return ErrPolicy
	}
	if d.Placement.Pointer != "" {
		parts := pointerParts(d.Placement.Pointer)
		if len(parts) == 0 || !slices.Contains(table.Fields, parts[0]) {
			return ErrPolicy
		}
		return nil
	}
	if !table.AllowCreate || d.Placement.Operation != "replace" {
		return ErrPolicy
	}
	value, err := contracts.Decode(d.Payload, contracts.OrdinaryLimit)
	if err != nil {
		return ErrAttempt
	}
	row, ok := value.(map[string]any)
	if !ok {
		return ErrAttempt
	}
	keys := map[string]any{}
	for _, name := range table.Keys {
		keys[name] = row[name]
	}
	for name := range row {
		if !slices.Contains(table.Keys, name) && !slices.Contains(table.Fields, name) {
			return ErrPolicy
		}
	}
	raw, _ := json.Marshal(keys)
	createdKey, ok := databaseKey(string(raw), *table)
	if !ok || !bytes.Equal(key, createdKey) {
		return ErrPolicy
	}
	return nil
}
