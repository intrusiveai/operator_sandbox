//go:build linux || darwin

package imagerelease

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/intrusiveai/operator_sandbox/internal/dockercontrol"
)

// Explicit opt-in: creates only a scratch image and stopped inspection containers.
// No pull, registry, model call or campaign execution occurs.
func TestNativeLocalImageCompatibility(t *testing.T) {
	endpoint := os.Getenv("OPERATOR_NATIVE_DOCKER_ENDPOINT")
	if endpoint == "" {
		t.Skip("set OPERATOR_NATIVE_DOCKER_ENDPOINT to opt in")
	}
	docker, err := exec.LookPath("docker")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	run := func(args ...string) ([]byte, error) {
		cmd := exec.CommandContext(ctx, docker, append([]string{"--host", endpoint}, args...)...)
		return cmd.Output()
	}
	p := embeddedProtocol(t)
	h := requirements()
	h.HostPlatform = runtime.GOOS + "/" + runtime.GOARCH
	h.Contract, _ = p.PackageIdentity()
	files, m := embeddedFiles(t, p, h)
	platform := "linux/" + runtime.GOARCH
	m.Platform = platform
	files[ManifestPath], _ = json.Marshal(m)
	record := release()
	record.Platform = platform
	record.ContractPackageVersion = h.Contract.Version
	record.ContractPackageDigest = h.Contract.Digest
	files[CompatibilityPath] = wire(record)
	dir := t.TempDir()
	for name, raw := range files {
		name = filepath.Join(dir, "root", strings.TrimPrefix(name, "/"))
		if err = os.MkdirAll(filepath.Dir(name), 0755); err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(name, raw, 0444); err != nil {
			t.Fatal(err)
		}
	}
	if err = os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte("FROM scratch\nCOPY root/ /\n"), 0600); err != nil {
		t.Fatal(err)
	}
	selector := fmt.Sprintf("operator-compatibility-probe:native-%d", time.Now().UnixNano())
	if _, err = run("build", "--pull=false", "--network=none", "--platform", platform, "--tag", selector, dir); err != nil {
		t.Fatal("local scratch build", err)
	}
	t.Cleanup(func() {
		cmd := exec.Command(docker, "--host", endpoint, "image", "rm", selector)
		if err := cmd.Run(); err != nil {
			t.Errorf("fixture image cleanup: %v", err)
		}
	})
	raw, err := run("image", "inspect", "--platform", platform, "--format", "{{.Id}}", selector)
	if err != nil {
		t.Fatal(err)
	}
	id := strings.TrimSpace(string(raw))
	_, bareErr := run("image", "inspect", "--platform", platform, id)
	t.Logf("tag inspection succeeded; bare ID inspection failed=%v", bareErr != nil)
	client, err := dockercontrol.New(docker)
	if err != nil {
		t.Fatal(err)
	}
	prepared, embedded, inspection, err := Prepare(ctx, client, endpoint, selector, h, p)
	if err != nil {
		t.Fatalf("local preparation: %v; inspection=%+v", err, inspection)
	}
	if !inspection.Removed || inspection.ContainerID == "" || !embedded.Matches(prepared) || prepared.Image.ImageID != id {
		t.Fatal("invalid local admission/cleanup")
	}
	if err = client.VerifyImage(ctx, prepared.Image); err != nil {
		t.Fatal(err)
	}
	remaining, err := run("container", "ls", "--all", "--no-trunc", "--filter", "id="+inspection.ContainerID, "--format", "{{.ID}}")
	if err != nil || len(strings.TrimSpace(string(remaining))) != 0 {
		t.Fatal("inspection container remains", err)
	}
	t.Logf("verified selected identity %s with immutable reference %s; five embedded files validated; inspection container removed", id, prepared.Image.ReferenceForDocker())
}
