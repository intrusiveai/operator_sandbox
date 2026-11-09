//go:build linux || darwin

package campaign

import (
	"fmt"
	"slices"
)

// CleanupDiagnostic projects action outcomes, never unrestricted cleanup errors.
// Empty older fields mean unconfirmed, not success.
type CleanupDiagnostic struct {
	Container  string `json:"harness_container"`
	Transport  string `json:"harness_transport"`
	Inputs     string `json:"harness_inputs"`
	Closure    string `json:"interceptor_ownership_closure"`
	Injections string `json:"interceptor_injection_cleanup"`
	TargetStop string `json:"interceptor_target_stop"`
}

func (d CleanupDiagnostic) Valid() bool {
	for _, field := range []string{d.Container, d.Transport, d.Inputs} {
		if !slices.Contains([]string{"", "removed", "retained", "unconfirmed", "not-created"}, field) {
			return false
		}
	}
	return slices.Contains([]string{"", "confirmed", "unconfirmed", "not-applicable"}, d.Closure) && slices.Contains([]string{"", "unconfirmed", "complete", "not-needed", "deadline", "bounded-remainder"}, d.Injections) && slices.Contains([]string{"", "confirmed", "unconfirmed", "not-applicable", "not-permitted"}, d.TargetStop)
}
func (d CleanupDiagnostic) Summary() string {
	known := func(s string) string {
		if s == "" {
			return "unconfirmed"
		}
		return s
	}
	return fmt.Sprintf("Owned cleanup actions: Harness container removal %s; transport removal %s; input removal %s; Interceptor ownership closure %s; injection cleanup %s; target stop %s. See service.terminal-result and launch.terminal evidence.", known(d.Container), known(d.Transport), known(d.Inputs), known(d.Closure), known(d.Injections), known(d.TargetStop))
}
