//go:build linux || darwin

package campaign

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func createIntentEntry(b DockerBinding) Entry {
	metadata, _ := json.Marshal(map[string]any{"endpoint": b.Endpoint, "daemon_id": b.DaemonID, "image_id": b.ImageDigest})
	return Entry{RunRevision: 3, Kind: "launch.start-intent", Metadata: metadata}
}

func TestRecoverBindingPreservesJournalAndIdentity(t *testing.T) {
	root, w := newWriter(t)
	b := fixtureBinding(w)
	appendOK(t, w, createIntentEntry(b))
	ctx := context.Background()
	if err := SaveRecoveredDockerBinding(ctx, root, b); !errors.Is(err, ErrActive) {
		t.Fatal("active writer was not protected", err)
	}
	w.Close()
	before, err := Inspect(root, b.CampaignID, nil)
	if err != nil {
		t.Fatal(err)
	}
	i, err := ReadCreateIntent(ctx, root, b.CampaignID)
	if err != nil {
		t.Fatal(err)
	}
	got, err := i.Binding(b.DockerContainerID)
	if err != nil || got.RunManifestDigest != b.RunManifestDigest || got.Endpoint != b.Endpoint {
		t.Fatal(got, err)
	}
	for range 2 {
		if err := SaveRecoveredDockerBinding(ctx, root, got); err != nil {
			t.Fatal(err)
		}
	}
	saved, err := ReadDockerBinding(root, b.CampaignID)
	if err != nil || saved.DockerContainerID != b.DockerContainerID {
		t.Fatal(saved, err)
	}
	got.DockerContainerID = strings.Repeat("c", 64)
	if err := SaveRecoveredDockerBinding(ctx, root, got); err == nil {
		t.Fatal("replaced binding")
	}
	after, err := Inspect(root, b.CampaignID, nil)
	if err != nil || !after.JournalIntact || before.VerifiedEvents != after.VerifiedEvents || before.VerifiedBytes != after.VerifiedBytes {
		t.Fatal(after, err)
	}
	if _, err := Create(root, before.Manifest); err == nil {
		t.Fatal("reopened campaign")
	}
}

func TestRecoverBindingRejectsUntrustedOrIncompleteEvidence(t *testing.T) {
	for _, kind := range []string{"no intent", "duplicate intent", "bad endpoint", "bad image", "bad revision", "bad metadata", "corrupt head", "pending head", "partial binding", "symlink binding", "changed endpoint", "changed daemon", "changed image", "cancelled"} {
		t.Run(kind, func(t *testing.T) {
			root, w := newWriter(t)
			b := fixtureBinding(w)
			e := createIntentEntry(b)
			switch kind {
			case "bad endpoint":
				wrong := b
				wrong.Endpoint = "tcp://localhost:2375"
				e = createIntentEntry(wrong)
			case "bad image":
				wrong := b
				wrong.ImageDigest = "sha256:" + strings.Repeat("c", 64)
				e = createIntentEntry(wrong)
			case "bad revision":
				e.RunRevision++
			case "bad metadata":
				e.Metadata = json.RawMessage(`{}`)
			}
			if kind != "no intent" {
				appendOK(t, w, e)
			}
			if kind == "duplicate intent" {
				appendOK(t, w, e)
			}
			w.Close()
			dir := filepath.Join(root, "campaigns", b.CampaignID)
			write := func(name, data string) {
				t.Helper()
				if err := os.WriteFile(filepath.Join(dir, name), []byte(data), 0600); err != nil {
					t.Fatal(err)
				}
			}
			ctx := context.Background()
			switch kind {
			case "corrupt head":
				write("journal-head.json", "bad")
			case "pending head":
				write("journal-head.json.pending", "bad")
			case "partial binding":
				write("launch/docker-binding.json.pending", "partial")
			case "symlink binding":
				if err := os.Symlink("run-manifest.json", filepath.Join(dir, "launch/docker-binding.json")); err != nil {
					t.Fatal(err)
				}
			case "changed endpoint":
				b.Endpoint = "unix:///other/socket"
			case "changed daemon":
				b.DaemonID = "other-daemon"
			case "changed image":
				b.ImageDigest = "sha256:" + strings.Repeat("c", 64)
			case "cancelled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			if err := SaveRecoveredDockerBinding(ctx, root, b); err == nil {
				t.Fatal("accepted invalid recovery")
			}
		})
	}
}
