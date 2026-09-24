//go:build linux || darwin

package hostconfig

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/intrusive-ai/operator-sandbox/contracts"
)

const minimal = "engine:\n  image: intrusive/attack_harness:dev\n"

func linuxDefaults(t *testing.T) Paths {
	t.Helper()
	p, err := Defaults("linux", "")
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestDefaultsAndEffectiveSettings(t *testing.T) {
	for _, goos := range []string{"linux", "darwin"} {
		t.Run(goos, func(t *testing.T) {
			p, err := Defaults(goos, "/Users/test")
			if err != nil {
				t.Fatal(err)
			}
			c, err := Parse([]byte(minimal), p)
			if err != nil {
				t.Fatal(err)
			}
			if c.State.Root != p.StateRoot || c.Docker.Endpoint != p.DockerEndpoint || c.Spool.MaxBytes != 536870912 || c.Cache.ReleaseDirectory != filepath.Join(p.StateRoot, "cache/releases") || c.Docker.Executable != "" {
				t.Fatal(c)
			}
			if goos == "darwin" && (p.ConfigFile != "/Users/test/Library/Application Support/Operator/config/config.yaml" || p.DockerEndpoint != "unix:///Users/test/.docker/run/docker.sock") {
				t.Fatal(p)
			}
		})
	}
	if _, err := Defaults("windows", ""); err == nil {
		t.Fatal("unsupported OS accepted")
	}
	if _, err := Defaults("darwin", "relative"); err == nil {
		t.Fatal("relative home accepted")
	}
	raw := minimal + "docker:\n  endpoint: unix:///run/user/1000/docker.sock\n  executable: /opt/docker/bin/docker\nstate:\n  root: /srv/operator\nspool:\n  max_bytes: 9007199254740991\n"
	c, err := Parse([]byte(raw), linuxDefaults(t))
	if err != nil || c.Cache.ReleaseDirectory != "/srv/operator/cache/releases" || c.Spool.MaxBytes != contracts.MaxSafeInteger || c.Docker.Executable != "/opt/docker/bin/docker" || c.Docker.Endpoint != "unix:///run/user/1000/docker.sock" {
		t.Fatal(c, err)
	}
	c, err = Parse([]byte(raw+"cache:\n  release_directory: /srv/approvals\n"), linuxDefaults(t))
	if err != nil || c.Cache.ReleaseDirectory != "/srv/approvals" {
		t.Fatal(c, err)
	}
}

func TestRejectAmbiguousOrUnsupportedConfiguration(t *testing.T) {
	cases := map[string]string{
		"empty": "", "missing image": "engine: {}", "null image": "engine: {image: null}",
		"boolean image": "engine: {image: true}", "number image": "engine: {image: 1}",
		"empty image": "engine: {image: ''}", "sequence": "[engine, image]",
		"duplicate section": minimal + minimal, "duplicate field": "engine: {image: first, image: second}",
		"unknown section": minimal + "secrets: {}", "unknown field": minimal + "docker: {context: default}",
		"release override": minimal + "cache: {release_url: 'https://evil.test'}",
		"alias":            "engine: &e {image: test}\ndocker: *e", "scalar anchor": "engine: {image: &i test}",
		"merge": "engine: {<<: {image: test}}", "custom tag": "engine: {image: !custom test}",
		"multiple documents": minimal + "---\n" + minimal, "empty trailing document": minimal + "---\n",
		"invalid yaml": "engine: [", "bad utf8": minimal + "#\xff",
		"oversize":            minimal + "#" + strings.Repeat("x", MaxBytes),
		"remote docker":       minimal + "docker: {endpoint: 'tcp://localhost:2375'}",
		"docker query":        minimal + "docker: {endpoint: 'unix:///run/docker.sock?x=y'}",
		"relative executable": minimal + "docker: {executable: docker}",
		"relative state":      minimal + "state: {root: ./data}", "tilde": minimal + "state: {root: ~/data}",
		"unclean path": minimal + "state: {root: /srv/../data}", "root": minimal + "state: {root: /}",
		"path tab":          minimal + "state: {root: \"/srv/\\tdata\"}",
		"purgeable cache":   minimal + "cache: {release_directory: /var/lib/operator/campaigns/test/cache}",
		"campaigns cache":   minimal + "cache: {release_directory: /var/lib/operator/campaigns}",
		"url image":         "engine: {image: 'https://registry.test/image'}",
		"image credentials": "engine: {image: 'user:password@registry.test/image'}",
		"bad digest":        "engine: {image: 'image@sha256:1234'}",
	}
	for _, n := range []string{"0", "-1", "1.5", "0x200", "010", "1_000", "+10", "'512'", "9007199254740992", "999999999999999999999999999999"} {
		cases["spool "+n] = minimal + "spool: {max_bytes: " + n + "}"
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			if c, err := Parse([]byte(raw), linuxDefaults(t)); err == nil {
				t.Fatalf("accepted: %+v", c)
			}
		})
	}
	_, err := Parse([]byte("engine: {unknown-super-secret-value: [password]}"), linuxDefaults(t))
	if err == nil || strings.Contains(err.Error(), "password") || strings.Contains(err.Error(), "super-secret") {
		t.Fatal(err)
	}
}

func TestLiteralStringsAndLocalSelectors(t *testing.T) {
	for _, image := range []string{"registry.test:5000/attack_harness:dev", "sha256:" + strings.Repeat("a", 64), "repo/image@sha256:" + strings.Repeat("a", 64)} {
		c, err := Parse([]byte("engine: {image: '"+image+"'}\nstate: {root: '/srv/$USER/operator'}\n"), linuxDefaults(t))
		if err != nil || c.Engine.Image != image || c.State.Root != "/srv/$USER/operator" {
			t.Fatal(c, err)
		}
	}
}

func TestPrivateBoundedFileLoading(t *testing.T) {
	p := linuxDefaults(t)
	dir := t.TempDir()
	name := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(name, []byte(minimal), 0600); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(name, p)
	if err != nil || loaded.Path != name || loaded.Digest != contracts.RawDigest([]byte(minimal)) {
		t.Fatal(loaded, err)
	}
	if _, err = Load(filepath.Join(dir, "missing"), p); !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
	if _, err = Load("relative.yaml", p); !errors.Is(err, ErrPath) {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link.yaml")
	if err = os.Symlink(name, link); err != nil {
		t.Fatal(err)
	}
	if _, err = Load(link, p); !errors.Is(err, ErrPrivate) {
		t.Fatal(err)
	}
	if err = os.Link(name, filepath.Join(dir, "hardlink")); err != nil {
		t.Fatal(err)
	}
	if _, err = Load(name, p); !errors.Is(err, ErrPrivate) {
		t.Fatal(err)
	}
	public := filepath.Join(dir, "public.yaml")
	if err = os.WriteFile(public, []byte(minimal), 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.Chmod(public, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err = Load(public, p); !errors.Is(err, ErrPrivate) {
		t.Fatal(err)
	}
	fifo := filepath.Join(dir, "fifo")
	if err = syscall.Mkfifo(fifo, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = Load(fifo, p); !errors.Is(err, ErrPrivate) {
		t.Fatal(err)
	}
	if _, err = Load(dir, p); !errors.Is(err, ErrPrivate) {
		t.Fatal(err)
	}
}
