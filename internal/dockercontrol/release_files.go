//go:build linux || darwin

package dockercontrol

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path"
	"strings"
	"time"

	"github.com/intrusiveai/operator_sandbox/contracts"
)

const imageArchiveLimit = (1 << 20) + (64 << 10)
const inspectionLabel = "ai.intrusive.operator.image-inspection"

var ErrReleaseFiles = errors.New("cannot inspect immutable image release files")

// ReleaseFiles retains exact bytes and the inspection identity for diagnostics.
// A nonempty ContainerID with Removed=false requires administrative cleanup.
// No successful result is returned until the stopped inspection container is removed.
type ReleaseFiles struct {
	Endpoint    string
	DaemonID    string
	ContainerID string
	Removed     bool
	Files       map[string][]byte
}

// Fixed paths are implementation-owned. Image metadata cannot select another
// file, command, mount or executable. The image is never started during inspection.
var releasePaths = []string{
	"/opt/operator/engine/share/engine-manifest.json",
	"/opt/operator/engine/share/default-system-prompt.txt",
	"/opt/operator/engine/lib/attack_harness/skill_loader.py",
	"/opt/operator/engine/share/tool-catalog.json",
}

func (c *Client) ReadReleaseFiles(ctx context.Context, pin ImagePin) (result ReleaseFiles, err error) {
	if c == nil || c.run == nil || c.archive == nil || pin.Validate() != nil {
		return result, ErrReleaseFiles
	}
	if err = c.VerifyImage(ctx, pin); err != nil {
		return result, err
	}
	result.Endpoint, result.DaemonID = pin.Endpoint, pin.DaemonID
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	directory, e := os.MkdirTemp("", "operator-image-inspection-")
	if e != nil {
		return result, ErrReleaseFiles
	}
	defer os.Remove(directory)
	nonce := make([]byte, 16)
	if _, e = rand.Read(nonce); e != nil {
		return result, ErrReleaseFiles
	}
	label := hex.EncodeToString(nonce)
	run := func(ctx context.Context, args ...string) ([]byte, error) {
		return c.run(ctx, pin.Endpoint, append([]string{"--config", directory}, args...)...)
	}
	// Volumes would conceal image files and leave extra Docker storage behind.
	volumes, e := run(ctx, "image", "inspect", "--platform", pin.ImagePlatform, "--format", "{{json .Config.Volumes}}", "--", pin.ImageID)
	if e != nil || (strings.TrimSpace(string(volumes)) != "null" && strings.TrimSpace(string(volumes)) != "{}") {
		return result, ErrReleaseFiles
	}
	raw, e := run(ctx, "container", "create", "--pull=never", "--platform", pin.ImagePlatform, "--network=none", "--read-only", "--cap-drop=ALL", "--security-opt=no-new-privileges=true", "--restart=no", "--user=65532:65532", "--label", inspectionLabel+"="+label, "--entrypoint=/usr/bin/python3", pin.ImageID, "-I", "-S", "-B")
	id := strings.TrimSpace(string(raw))
	if len(id) != 64 || !imageDigest.MatchString("sha256:"+id) {
		return result, ErrReleaseFiles
	}
	result.ContainerID = id
	verify := func(ctx context.Context) error {
		daemon, err := c.imageDaemon(ctx, pin.Endpoint)
		if err != nil || daemon != pin.DaemonID {
			return ErrImageIdentity
		}
		raw, err := run(ctx, "container", "inspect", "--format", inspectFormat, id)
		if err != nil {
			return ErrReleaseFiles
		}
		value, err := contracts.Decode(raw, outputLimit)
		object, ok := value.(map[string]any)
		if err != nil || !ok || len(object) != 9 {
			return ErrReleaseFiles
		}
		var item identity
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.DisallowUnknownFields()
		if decoder.Decode(&item) != nil || item.ID != id || item.Image != pin.ImageID || item.Labels[inspectionLabel] != label || item.Status != "created" || item.Running || item.Paused || item.Restarting || item.RestartPolicy != "no" || item.AutoRemove {
			return ErrReleaseFiles
		}
		daemon, err = c.imageDaemon(ctx, pin.Endpoint)
		if err != nil || daemon != pin.DaemonID {
			return ErrImageIdentity
		}
		return nil
	}
	defer func() {
		cleanup, stop := context.WithTimeout(context.Background(), ConfirmationTimeout)
		defer stop()
		if verify(cleanup) != nil {
			err = errors.Join(err, ErrReleaseFiles)
			result.Files = nil
			return
		}
		if raw, e := run(cleanup, "container", "rm", id); e != nil || strings.TrimSpace(string(raw)) != id {
			err = errors.Join(err, ErrReleaseFiles)
			result.Files = nil
			return
		}
		daemon, e := c.imageDaemon(cleanup, pin.Endpoint)
		if e != nil || daemon != pin.DaemonID {
			err = errors.Join(err, ErrImageIdentity)
			result.Files = nil
			return
		}
		result.Removed = true
	}()
	if e != nil {
		return result, ErrReleaseFiles
	}
	if err = verify(ctx); err != nil {
		return result, err
	}
	result.Files = map[string][]byte{}
	for _, file := range releasePaths {
		raw, e := c.archive(ctx, pin.Endpoint, "--config", directory, "container", "cp", id+":"+file, "-")
		if e != nil {
			result.Files = nil
			return result, ErrReleaseFiles
		}
		body, e := releaseFile(raw, path.Base(file))
		if e != nil {
			result.Files = nil
			return result, e
		}
		result.Files[file] = body
	}
	if err = verify(ctx); err != nil {
		result.Files = nil
		return result, err
	}
	return result, nil
}

// Decode in memory; never extract an archive into the host filesystem. Links,
// sparse files, alternate paths, additional entries and trailing data are rejected.
func releaseFile(raw []byte, name string) ([]byte, error) {
	if len(raw) > imageArchiveLimit {
		return nil, ErrReleaseFiles
	}
	reader := bytes.NewReader(raw)
	archive := tar.NewReader(reader)
	header, err := archive.Next()
	if err != nil || header.Name != name || header.Typeflag != tar.TypeReg || header.Linkname != "" || header.Size < 1 || header.Size > 1<<20 || header.Mode&07000 != 0 || len(header.Xattrs) > 0 {
		return nil, ErrReleaseFiles
	}
	for key := range header.PAXRecords {
		if strings.HasPrefix(key, "GNU.sparse") || strings.HasPrefix(key, "SCHILY.") {
			return nil, ErrReleaseFiles
		}
	}
	data, err := io.ReadAll(io.LimitReader(archive, header.Size+1))
	if err != nil || int64(len(data)) != header.Size {
		return nil, ErrReleaseFiles
	}
	if _, err = archive.Next(); err != io.EOF {
		return nil, ErrReleaseFiles
	}
	// Docker/tar may pad blocks with zeroes after the required end markers.
	tail := raw[len(raw)-reader.Len():]
	if !bytes.Equal(tail, make([]byte, len(tail))) {
		return nil, ErrReleaseFiles
	}
	return data, nil
}
