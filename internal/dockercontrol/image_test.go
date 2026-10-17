//go:build linux || darwin

package dockercontrol

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestLocalImageResolutionAndPin(t *testing.T) {
	for _, host := range []string{"linux/amd64", "linux/arm64", "darwin/amd64", "darwin/arm64"} {
		t.Run(host, func(t *testing.T) {
			platform, _ := ImagePlatform(host)
			selector := "registry.example.com/attack_harness:latest"
			id := "sha256:" + strings.Repeat("a", 64)
			repo := "registry.example.com/attack_harness@sha256:" + strings.Repeat("b", 64)
			var references []string
			c := &Client{run: func(ctx context.Context, endpoint string, args ...string) ([]byte, error) {
				if endpoint != "unix:///saved/docker.sock" {
					t.Fatal(endpoint)
				}
				if args[0] == "info" {
					return []byte(`"daemon-1"`), nil
				}
				if !reflect.DeepEqual(args[:len(args)-1], []string{"image", "inspect", "--platform", platform, "--format", imageFormat, "--"}) {
					t.Fatal("unexpected Docker action", args)
				}
				references = append(references, args[len(args)-1])
				return json.Marshal(map[string]any{"id": id, "os": "linux", "architecture": strings.Split(platform, "/")[1], "repo_digests": []string{repo}})
			}}
			pin, err := c.ResolveImage(context.Background(), "unix:///saved/docker.sock", selector, host)
			if err != nil || pin.ImageID != id || pin.RepoDigests[0] != repo || pin.HostPlatform != host {
				t.Fatal(pin, err)
			}
			if err := c.VerifyImage(context.Background(), pin); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(references, []string{selector, id, id}) {
				t.Fatal("verification retargeted mutable selector", references)
			}
		})
	}
}

func TestImageFailuresDoNotPullOrSubstitute(t *testing.T) {
	for _, kind := range []string{"missing", "wrong architecture", "wrong OS", "wrong ID", "daemon changed", "malformed", "missing repository inventory", "unknown field", "duplicate field"} {
		t.Run(kind, func(t *testing.T) {
			id := "sha256:" + strings.Repeat("a", 64)
			infos := 0
			c := &Client{run: func(ctx context.Context, endpoint string, args ...string) ([]byte, error) {
				if args[0] == "info" {
					infos++
					if kind == "daemon changed" && infos == 2 {
						return []byte(`"other"`), nil
					}
					return []byte(`"daemon-1"`), nil
				}
				if args[0] != "image" || args[1] != "inspect" {
					t.Fatal("effect during image lookup", args)
				}
				if kind == "missing" {
					return nil, errors.New("missing")
				}
				m := map[string]any{"id": id, "os": "linux", "architecture": "arm64", "repo_digests": nil}
				switch kind {
				case "wrong architecture":
					m["architecture"] = "amd64"
				case "wrong OS":
					m["os"] = "windows"
				case "wrong ID":
					m["id"] = "sha256:" + strings.Repeat("b", 64)
				case "malformed":
					return []byte(`[]`), nil
				case "missing repository inventory":
					delete(m, "repo_digests")
				case "unknown field":
					m["extra"] = true
				}
				raw, _ := json.Marshal(m)
				if kind == "duplicate field" {
					raw = append([]byte(`{"id":"bad",`), raw[1:]...)
				}
				return raw, nil
			}}
			if _, err := c.ResolveImage(context.Background(), "unix:///saved/docker.sock", id, "darwin/arm64"); err == nil {
				t.Fatal("accepted", kind)
			}
		})
	}
}

func TestImageLookupRejectsInvalidInputsBeforeDocker(t *testing.T) {
	c := &Client{run: func(context.Context, string, ...string) ([]byte, error) {
		t.Fatal("unexpected Docker call")
		return nil, nil
	}}
	for _, selector := range []string{"", "--help", "image\nsecond", "https://registry.example/image", "x y", "$(whoami)"} {
		if _, err := c.ResolveImage(context.Background(), "unix:///saved/docker.sock", selector, "linux/amd64"); err == nil {
			t.Fatal(selector)
		}
	}
	if _, err := c.ResolveImage(context.Background(), "tcp://remote:2375", "image", "linux/amd64"); err == nil {
		t.Fatal("remote allowed")
	}
	if _, err := c.ResolveImage(context.Background(), "unix:///saved/docker.sock", "image", "windows/amd64"); err == nil {
		t.Fatal("unsupported host allowed")
	}
}

func TestTagChangeBeforeAndAfterAcceptance(t *testing.T) {
	oldID := "sha256:" + strings.Repeat("a", 64)
	newID := "sha256:" + strings.Repeat("b", 64)
	selected := oldID
	c := &Client{run: func(ctx context.Context, endpoint string, args ...string) ([]byte, error) {
		if args[0] == "info" {
			return []byte(`"daemon-1"`), nil
		}
		if args[0] != "image" || args[1] != "inspect" {
			t.Fatal(args)
		}
		id := oldID
		if args[len(args)-1] == "image:latest" {
			id = selected
		} else if args[len(args)-1] != oldID {
			t.Fatal("pin was substituted", args)
		}
		return json.Marshal(map[string]any{"id": id, "os": "linux", "architecture": "arm64", "repo_digests": nil})
	}}
	pin, err := c.ResolveImage(context.Background(), "unix:///saved/docker.sock", "image:latest", "darwin/arm64")
	if err != nil {
		t.Fatal(err)
	}
	selected = newID
	if err := c.VerifySelection(context.Background(), pin); !errors.Is(err, ErrImageIdentity) {
		t.Fatal("prepared selector change ignored", err)
	}
	if err := c.VerifyImage(context.Background(), pin); err != nil {
		t.Fatal("accepted pin followed latest", err)
	}
}

func TestContainerdImmutableReferenceSelection(t *testing.T) {
	for _, host := range []string{"linux/amd64", "darwin/arm64"} {
		for _, mode := range []string{"success", "no matching repo", "wrong bytes", "wrong platform", "timeout", "tag moved", "pinned absent"} {
			t.Run(host+"/"+mode, func(t *testing.T) {
				platform, _ := ImagePlatform(host)
				id := "sha256:" + strings.Repeat("a", 64)
				selected := "registry.example/harness:candidate"
				repo := "registry.example/harness@sha256:" + strings.Repeat("b", 64)
				unrelated := "other.example/harness@sha256:" + strings.Repeat("c", 64)
				moved, absent := false, false
				calls := []string{}
				c := &Client{run: func(ctx context.Context, endpoint string, args ...string) ([]byte, error) {
					if args[0] == "info" {
						return []byte(`"daemon-1"`), nil
					}
					if args[0] != "image" || args[1] != "inspect" || args[3] != platform {
						t.Fatal(args)
					}
					ref := args[len(args)-1]
					calls = append(calls, ref)
					if ref == unrelated {
						t.Fatal("selected unrelated repository")
					}
					if ref == id || (absent && ref == repo) {
						return nil, classifyCommand(ctx, errors.New("exit"), []byte("No such image: private"), false)
					}
					rid, arch := id, strings.Split(platform, "/")[1]
					if ref == repo {
						switch mode {
						case "wrong bytes":
							rid = "sha256:" + strings.Repeat("d", 64)
						case "wrong platform":
							arch = "invalid"
						case "timeout":
							return nil, context.DeadlineExceeded
						}
					} else if ref != selected {
						t.Fatal(ref)
					}
					if moved && ref == selected {
						rid = "sha256:" + strings.Repeat("e", 64)
					}
					inventory := []string{unrelated, repo}
					if mode == "no matching repo" {
						inventory = []string{unrelated}
					}
					return json.Marshal(map[string]any{"id": rid, "os": "linux", "architecture": arch, "repo_digests": inventory})
				}}
				pin, err := c.ResolveImage(context.Background(), "unix:///saved/docker.sock", selected, host)
				if mode == "no matching repo" || mode == "wrong bytes" || mode == "wrong platform" || mode == "timeout" {
					if err == nil {
						t.Fatal("accepted", mode)
					}
					return
				}
				if err != nil || pin.Reference != repo || pin.ImageID != id {
					t.Fatal(pin, err)
				}
				if !reflect.DeepEqual(calls, []string{selected, id, repo}) {
					t.Fatal(calls)
				}
				moved = mode == "tag moved"
				absent = mode == "pinned absent"
				if moved {
					if err = c.VerifySelection(context.Background(), pin); !errors.Is(err, ErrImageIdentity) {
						t.Fatal(err)
					}
				}
				calls = nil
				err = c.VerifyImage(context.Background(), pin)
				if absent && err == nil || !absent && err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(calls, []string{repo}) {
					t.Fatal("verification changed reference", calls)
				}
				// Round-trip host metadata. Legacy pins remain bare-ID only and fail here;
				// a failed legacy lookup must not upgrade itself using the repository inventory.
				raw, _ := json.Marshal(pin)
				var saved ImagePin
				if err = json.Unmarshal(raw, &saved); err != nil || saved.Reference != repo {
					t.Fatal(err, saved)
				}
				saved.Reference = ""
				calls = nil
				if err = c.VerifyImage(context.Background(), saved); err == nil {
					t.Fatal("legacy pin reinterpreted")
				}
				if !reflect.DeepEqual(calls, []string{id}) {
					t.Fatal(calls)
				}
			})
		}
	}
}

func TestRepositoryReferenceValidation(t *testing.T) {
	for name, want := range map[string]string{"ubuntu:latest": "docker.io/library/ubuntu", "library/ubuntu": "docker.io/library/ubuntu", "docker.io/ubuntu:1": "docker.io/library/ubuntu", "index.docker.io/library/ubuntu:1": "docker.io/library/ubuntu", "localhost:5000/a:b": "localhost:5000/a"} {
		if got := repository(name); got != want {
			t.Fatalf("%s: %s != %s", name, got, want)
		}
	}
	for _, ref := range []string{"repo:latest", "repo:tag@sha256:" + strings.Repeat("a", 64), "repo@@sha256:" + strings.Repeat("a", 64)} {
		if immutableReference(ref) {
			t.Fatal(ref)
		}
	}
}
