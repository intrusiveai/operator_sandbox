//go:build linux || darwin

package imagerelease

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/intrusiveai/operator_sandbox/contracts"
	"github.com/intrusiveai/operator_sandbox/internal/dockercontrol"
)

type localImage struct {
	pin          dockercontrol.ImagePin
	files        map[string][]byte
	reads        int
	resolveError error
}

func (i *localImage) ResolveImage(context.Context, string, string, string) (dockercontrol.ImagePin, error) {
	return i.pin, i.resolveError
}
func (i *localImage) ReadReleaseFiles(_ context.Context, pin dockercontrol.ImagePin) (dockercontrol.ReleaseFiles, error) {
	i.reads++
	return dockercontrol.ReleaseFiles{Files: i.files, Endpoint: pin.Endpoint, DaemonID: pin.DaemonID, ContainerID: strings.Repeat("b", 64), Removed: true}, nil
}

func TestLocalCompatibilityAdmission(t *testing.T) {
	p := embeddedProtocol(t)
	for _, mode := range []string{"valid", "missing record", "bad version", "contract mismatch", "platform mismatch", "runtime mismatch", "extra field", "self digest", "oversized", "image unavailable", "wrong selector", "canceled"} {
		t.Run(mode, func(t *testing.T) {
			h := requirements()
			h.Contract, _ = p.PackageIdentity()
			files, _ := embeddedFiles(t, p, h)
			image := &localImage{pin: dockercontrol.ImagePin{Selector: "local:test", Endpoint: "unix:///saved/docker.sock", DaemonID: "daemon-1", ImageID: contracts.RawDigest([]byte("local image")), HostPlatform: h.HostPlatform, ImagePlatform: "linux/arm64"}, files: files}
			r, _ := Parse(files[CompatibilityPath])
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			switch mode {
			case "bad version":
				r.MinimumOperatorVersion = "99.0.0"
			case "contract mismatch":
				r.ContractPackageDigest = contracts.RawDigest([]byte("other"))
			case "platform mismatch":
				r.Platform = "linux/amd64"
			case "runtime mismatch":
				r.RuntimeProfile = "other/v1"
			case "image unavailable":
				image.resolveError = dockercontrol.ErrImage
			case "wrong selector":
				image.pin.Selector = "different"
			case "canceled":
				cancel()
			}
			files[CompatibilityPath] = append(wire(r), '\n')
			switch mode {
			case "missing record":
				delete(files, CompatibilityPath)
			case "extra field":
				files[CompatibilityPath] = append([]byte(`{"unknown":true,`), files[CompatibilityPath][1:]...)
			case "self digest":
				files[CompatibilityPath] = append([]byte(`{"image_digest":"`+image.pin.ImageID+`",`), files[CompatibilityPath][1:]...)
			case "oversized":
				files[CompatibilityPath] = append(files[CompatibilityPath], bytes.Repeat([]byte(" "), ResponseLimit)...)
			}
			prepared, embedded, inspection, err := Prepare(ctx, image, image.pin.Endpoint, "local:test", h, p)
			if (err == nil) != (mode == "valid") {
				t.Fatal(mode, err)
			}
			if mode == "valid" {
				if !inspection.Removed || !embedded.Matches(prepared) || !bytes.Equal(prepared.Release.Bytes(), files[CompatibilityPath]) || prepared.Release.Digest() != contracts.RawDigest(files[CompatibilityPath]) {
					t.Fatal("lost identity or bytes")
				}
				raw := prepared.Release.Bytes()
				raw[0] = '!'
				if prepared.Release.Bytes()[0] != '{' {
					t.Fatal("mutable compatibility")
				}
				if prepared.Release.Compatible(contracts.RawDigest([]byte("other image")), h) == nil {
					t.Fatal("compatibility rebound to another image")
				}
				other, e := Validate(files[CompatibilityPath], contracts.RawDigest([]byte("other image")), h)
				if e != nil {
					t.Fatal(e)
				}
				rebound := prepared
				rebound.Release = other
				if embedded.Matches(rebound) {
					t.Fatal("same metadata bytes allowed a different image binding")
				}
				changed := prepared
				changed.Image.DaemonID = "other"
				if embedded.Matches(changed) {
					t.Fatal("metadata rebound to another daemon")
				}
			}
			if mode == "canceled" && (!errors.Is(err, context.Canceled) || image.reads != 0) {
				t.Fatal("canceled admission performed IO")
			}
			if mode == "image unavailable" && image.reads != 0 {
				t.Fatal("metadata substituted for missing image")
			}
		})
	}
}
