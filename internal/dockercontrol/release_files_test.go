//go:build linux || darwin

package dockercontrol

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"path"
	"slices"
	"strings"
	"testing"
)

func tarFile(t *testing.T, header *tar.Header, body []byte) []byte {
	t.Helper()
	var out bytes.Buffer
	w := tar.NewWriter(&out)
	if err := w.WriteHeader(header); err != nil {
		t.Fatal(err)
	}
	if len(body) > 0 {
		if _, err := w.Write(body); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}
func TestReleaseFileArchive(t *testing.T) {
	for _, kind := range []string{"valid", "link", "hardlink", "directory", "traversal", "absolute", "wrong name", "empty", "oversize", "xattrs", "setuid", "extra entry", "trailing data", "truncated"} {
		t.Run(kind, func(t *testing.T) {
			body := []byte("content")
			h := &tar.Header{Name: "prompt", Typeflag: tar.TypeReg, Mode: 0444, Size: int64(len(body))}
			switch kind {
			case "link":
				h.Typeflag = tar.TypeSymlink
				h.Linkname = "outside"
				h.Size = 0
				body = nil
			case "hardlink":
				h.Typeflag = tar.TypeLink
				h.Linkname = "outside"
				h.Size = 0
				body = nil
			case "directory":
				h.Typeflag = tar.TypeDir
				h.Size = 0
				body = nil
			case "traversal":
				h.Name = "../prompt"
			case "absolute":
				h.Name = "/prompt"
			case "wrong name":
				h.Name = "different"
			case "empty":
				h.Size = 0
				body = nil
			case "oversize":
				body = make([]byte, (1<<20)+1)
				h.Size = int64(len(body))
			case "xattrs":
				h.Xattrs = map[string]string{"attribute": "value"}
			case "setuid":
				h.Mode = 04444
			}
			raw := tarFile(t, h, body)
			switch kind {
			case "extra entry":
				raw = append(raw[:len(raw)-1024], raw...)
			case "trailing data":
				raw = append(raw, 1)
			case "truncated":
				raw = raw[:512+len(body)-1]
			}
			got, err := releaseFile(raw, "prompt")
			if (err == nil) != (kind == "valid") {
				t.Fatalf("err=%v", err)
			}
			if err == nil && !bytes.Equal(got, body) {
				t.Fatal("bytes changed")
			}
		})
	}
}
func TestStoppedImageInspection(t *testing.T) {
	for _, mode := range []string{"success", "cp failure", "cancel during read", "lost create with ID", "lost create without ID", "foreign daemon", "wrong image", "unexpected running", "remove failure", "volume", "archive link"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			pin := ImagePin{Selector: "harness:latest", Endpoint: "unix:///saved/docker.sock", DaemonID: "daemon-1", ImageID: "sha256:" + strings.Repeat("a", 64), HostPlatform: "darwin/arm64", ImagePlatform: "linux/arm64"}
			id := strings.Repeat("b", 64)
			label := ""
			created, removed, copies := 0, 0, 0
			c := &Client{}
			c.run = func(ctx context.Context, endpoint string, args ...string) ([]byte, error) {
				if endpoint != pin.Endpoint {
					t.Fatal("endpoint changed")
				}
				if ctx.Err() != nil {
					return nil, ctx.Err()
				}
				if args[0] == "--config" {
					args = args[2:]
				}
				switch args[0] {
				case "info":
					if mode == "foreign daemon" && created > 0 {
						return []byte(`"foreign"`), nil
					}
					return []byte(`"daemon-1"`), nil
				case "image":
					if slices.Contains(args, "{{json .Config.Volumes}}") {
						if mode == "volume" {
							return []byte(`{"/opt":{}}`), nil
						}
						return []byte(`null`), nil
					}
					if args[len(args)-1] != pin.ImageID {
						t.Fatal("mutable image selector used")
					}
					return json.Marshal(map[string]any{"id": pin.ImageID, "os": "linux", "architecture": "arm64", "repo_digests": nil})
				case "container":
					switch args[1] {
					case "create":
						created++
						for _, flag := range []string{"--pull=never", "--network=none", "--read-only", "--cap-drop=ALL", "--restart=no", "--user=65532:65532", "--entrypoint=/usr/bin/python3", pin.ImageID} {
							if !slices.Contains(args, flag) {
								t.Fatal("missing containment", flag)
							}
						}
						label = args[slices.Index(args, "--label")+1]
						label = strings.TrimPrefix(label, inspectionLabel+"=")
						if mode == "lost create without ID" {
							return nil, errors.New("lost")
						}
						if mode == "lost create with ID" {
							return []byte(id), errors.New("lost")
						}
						return []byte(id), nil
					case "inspect":
						if args[len(args)-1] != id {
							t.Fatal("not exact ID")
						}
						item := identity{ID: id, Image: pin.ImageID, Labels: map[string]string{inspectionLabel: label}, Status: "created", RestartPolicy: "no"}
						if mode == "wrong image" {
							item.Image = "sha256:" + strings.Repeat("c", 64)
						}
						if mode == "unexpected running" {
							item.Running = true
							item.Status = "running"
						}
						return json.Marshal(item)
					case "rm":
						if len(args) != 3 || args[2] != id {
							t.Fatal("force or non-ID removal")
						}
						removed++
						if mode == "remove failure" {
							return nil, errors.New("lost remove")
						}
						return []byte(id), nil
					}
				}
				t.Fatal("unexpected Docker operation", args)
				return nil, nil
			}
			c.archive = func(ctx context.Context, endpoint string, args ...string) ([]byte, error) {
				copies++
				if endpoint != pin.Endpoint || len(args) != 6 || args[2] != "container" || args[3] != "cp" || args[5] != "-" {
					t.Fatal("unexpected archive command", args)
				}
				file := strings.TrimPrefix(args[4], id+":")
				if !slices.Contains(releasePaths, file) {
					t.Fatal("arbitrary path", file)
				}
				if mode == "cancel during read" {
					cancel()
					return nil, context.Canceled
				}
				if mode == "cp failure" {
					return nil, errors.New("cp failed")
				}
				h := &tar.Header{Name: path.Base(file), Typeflag: tar.TypeReg, Mode: 0444, Size: 1}
				body := []byte("x")
				if mode == "archive link" {
					h.Typeflag = tar.TypeSymlink
					h.Linkname = "outside"
					h.Size = 0
					body = nil
				}
				return tarFile(t, h, body), nil
			}
			result, err := c.ReadReleaseFiles(ctx, pin)
			if (err == nil) != (mode == "success") {
				t.Fatalf("mode=%s err=%v", mode, err)
			}
			if mode == "success" && (len(result.Files) != 4 || !result.Removed || copies != 4) {
				t.Fatal(result, copies)
			}
			if mode != "success" && len(result.Files) != 0 {
				t.Fatal("partial bytes usable after failure")
			}
			wantRemove := mode == "success" || mode == "cp failure" || mode == "cancel during read" || mode == "archive link" || mode == "lost create with ID" || mode == "remove failure"
			if (removed == 1) != wantRemove {
				t.Fatalf("unexpected cleanup count %d", removed)
			}
			if mode == "lost create with ID" && (result.ContainerID != id || !result.Removed) {
				t.Fatal("lost reply identity not cleaned")
			}
		})
	}
}
