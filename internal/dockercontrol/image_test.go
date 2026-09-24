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
			if !reflect.DeepEqual(references, []string{selector, id}) {
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
