//go:build linux || darwin

package hostrun

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/intrusiveai/operator_sandbox/internal/hostconfig"
)

func TestSelectionRejectsInvalidIdentityAndPromptModesBeforeIO(t *testing.T) {
	for _, change := range []func(*Selection){
		func(s *Selection) { s.RunDirectory = "relative" },
		func(s *Selection) { s.CampaignID = "../escape" },
		func(s *Selection) { s.ContainerID = "docker-name" },
		func(s *Selection) { s.StartRequestID = "not-an-id" },
		func(s *Selection) { s.PromptMode = "arbitrary" },
		func(s *Selection) { s.PromptMode = "replacement" },
		func(s *Selection) { s.ReplacementFile = "/tmp/prompt" },
		func(s *Selection) { s.PromptMode = "extension" },
		func(s *Selection) { s.AppendFiles = []string{"/tmp/prompt"} },
		func(s *Selection) { s.SkillDigests = []string{"not-a-digest"} },
		func(s *Selection) {
			s.SkillDigests = []string{"sha256:" + strings.Repeat("a", 64), "sha256:" + strings.Repeat("a", 64)}
		},
		func(s *Selection) { s.AppendFiles = make([]string, 17) },
	} {
		s := NewSelection("/tmp/run")
		change(&s)
		if _, err := LoadInputs(context.Background(), "/nonexistent/config", hostconfig.Paths{}, s); !errors.Is(err, ErrSession) {
			t.Fatalf("invalid selection reached filesystem: %v", err)
		}
	}
}
func TestNewSelectionUsesDistinctHostIdentities(t *testing.T) {
	seen := map[string]bool{}
	for n := 0; n < 20; n++ {
		s := NewSelection("/tmp/run")
		if err := validateSelection(s); err != nil {
			t.Fatal(err)
		}
		for _, id := range []string{s.CampaignID, s.LaunchID, s.ContainerID, s.WorkerInstanceID, s.StartRequestID} {
			if seen[id] {
				t.Fatal("identity reused")
			}
			seen[id] = true
		}
	}
}
