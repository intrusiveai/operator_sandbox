//go:build linux || darwin

// Package hostconfig reads administrator-owned installation settings. It never
// sources settings from campaigns, the current directory or Docker environment.
package hostconfig

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"unicode/utf8"

	"go.yaml.in/yaml/v3"

	"github.com/intrusive-ai/operator-sandbox/contracts"
	"github.com/intrusive-ai/operator-sandbox/internal/campaign"
	"github.com/intrusive-ai/operator-sandbox/internal/dockercontrol"
)

const MaxBytes = 64 << 10
const DefaultSpoolBytes int64 = 512 << 20

var (
	ErrInvalid = errors.New("invalid configuration: use only documented sections, fields and scalar types")
	ErrPrivate = errors.New("configuration must be a private regular file owned by the service user or root")
	ErrImage   = errors.New("engine.image must select a local Docker image name, repository digest or full image ID")
	ErrPath    = errors.New("configuration paths must be absolute and clean, without control characters")
	ErrSpool   = errors.New("spool.max_bytes must be a positive decimal integer no greater than 9007199254740991")
)

type Paths struct{ ConfigFile, StateRoot, DockerEndpoint string }

// Defaults uses host facts resolved once by the installation/service caller.
func Defaults(goos, home string) (Paths, error) {
	switch goos {
	case "linux":
		return Paths{"/etc/operator/config.yaml", "/var/lib/operator", "unix:///var/run/docker.sock"}, nil
	case "darwin":
		if !absolute(home) {
			return Paths{}, ErrPath
		}
		base := filepath.Join(home, "Library", "Application Support", "Operator")
		return Paths{filepath.Join(base, "config", "config.yaml"), filepath.Join(base, "data"), "unix://" + filepath.Join(home, ".docker", "run", "docker.sock")}, nil
	default:
		return Paths{}, errors.New("unsupported Operator host OS")
	}
}

type Config struct {
	Engine struct {
		Image string `json:"image"`
	} `json:"engine"`
	Docker struct {
		Endpoint   string `json:"endpoint"`
		Executable string `json:"executable"`
	} `json:"docker"`
	State struct {
		Root string `json:"root"`
	} `json:"state"`
	Cache struct {
		ReleaseDirectory string `json:"release_directory"`
	} `json:"cache"`
	Spool struct {
		MaxBytes int64 `json:"max_bytes"`
	} `json:"spool"`
}

type Loaded struct {
	Path   string `json:"source"`
	Digest string `json:"source_digest"`
	Config Config `json:"settings"`
}

func absolute(s string) bool {
	return len(s) <= 4096 && filepath.IsAbs(s) && filepath.Clean(s) == s && s != "/" && strings.IndexFunc(s, func(r rune) bool { return r < 32 || r == 127 }) < 0
}

func mapping(n *yaml.Node) (map[string]*yaml.Node, error) {
	if n.Kind != yaml.MappingNode || n.Tag != "!!map" || n.Anchor != "" {
		return nil, ErrInvalid
	}
	result := map[string]*yaml.Node{}
	for i := 0; i < len(n.Content); i += 2 {
		k, v := n.Content[i], n.Content[i+1]
		if k.Kind != yaml.ScalarNode || k.Tag != "!!str" || k.Anchor != "" || v.Anchor != "" || result[k.Value] != nil {
			return nil, ErrInvalid
		}
		result[k.Value] = v
	}
	return result, nil
}

var decimal = regexp.MustCompile(`^[1-9][0-9]*$`)

// Parse accepts a closed two-level YAML map. Aliases, anchors, merge keys, multiple
// documents, coercions, unknown/duplicate fields and nulls are rejected. Errors do
// not include parser excerpts or unrecognized values.
func Parse(raw []byte, defaults Paths) (Config, error) {
	var c Config
	if len(raw) == 0 || len(raw) > MaxBytes || !utf8.Valid(raw) {
		return c, ErrInvalid
	}
	if !absolute(defaults.StateRoot) || campaign.ValidateDockerEndpoint(defaults.DockerEndpoint) != nil {
		return c, ErrPath
	}
	d := yaml.NewDecoder(bytes.NewReader(raw))
	var doc, extra yaml.Node
	if d.Decode(&doc) != nil || len(doc.Content) != 1 || !errors.Is(d.Decode(&extra), io.EOF) {
		return c, ErrInvalid
	}
	sections, err := mapping(doc.Content[0])
	if err != nil {
		return c, err
	}
	c.State.Root = defaults.StateRoot
	c.Docker.Endpoint = defaults.DockerEndpoint
	c.Spool.MaxBytes = DefaultSpoolBytes
	fields := map[string]map[string]*string{
		"engine": {"image": &c.Engine.Image},
		"docker": {"endpoint": &c.Docker.Endpoint, "executable": &c.Docker.Executable},
		"state":  {"root": &c.State.Root},
		"cache":  {"release_directory": &c.Cache.ReleaseDirectory},
	}
	for section, node := range sections {
		allowed, ok := fields[section]
		if !ok && section != "spool" {
			return Config{}, ErrInvalid
		}
		values, err := mapping(node)
		if err != nil {
			return Config{}, err
		}
		for name, value := range values {
			if value.Kind != yaml.ScalarNode || value.Anchor != "" {
				return Config{}, ErrInvalid
			}
			if section == "spool" {
				if name != "max_bytes" || value.Tag != "!!int" || !decimal.MatchString(value.Value) {
					return Config{}, ErrSpool
				}
				n, err := strconv.ParseInt(value.Value, 10, 64)
				if err != nil || n > contracts.MaxSafeInteger {
					return Config{}, ErrSpool
				}
				c.Spool.MaxBytes = n
				continue
			}
			dst, ok := allowed[name]
			if !ok || value.Tag != "!!str" || value.Value == "" {
				return Config{}, ErrInvalid
			}
			*dst = value.Value
		}
	}
	if dockercontrol.ValidateImageSelector(c.Engine.Image) != nil {
		return Config{}, ErrImage
	}
	if campaign.ValidateDockerEndpoint(c.Docker.Endpoint) != nil {
		return Config{}, errors.New("docker.endpoint must be a canonical local unix:/// socket")
	}
	if !absolute(c.State.Root) || (c.Docker.Executable != "" && !absolute(c.Docker.Executable)) {
		return Config{}, ErrPath
	}
	if c.Cache.ReleaseDirectory == "" {
		c.Cache.ReleaseDirectory = filepath.Join(c.State.Root, "cache", "releases")
	}
	if !absolute(c.Cache.ReleaseDirectory) {
		return Config{}, ErrPath
	}
	// Reusable approvals must not be inside the purgeable campaign tree.
	campaigns := filepath.Join(c.State.Root, "campaigns")
	if c.Cache.ReleaseDirectory == campaigns || strings.HasPrefix(c.Cache.ReleaseDirectory, campaigns+string(filepath.Separator)) {
		return Config{}, ErrPath
	}
	return c, nil
}

func privateFile(info os.FileInfo) bool {
	st, ok := info.Sys().(*syscall.Stat_t)
	return ok && info.Mode().IsRegular() && info.Mode().Perm()&0077 == 0 && st.Nlink == 1 && (st.Uid == 0 || st.Uid == uint32(os.Geteuid()))
}

// Load reads one absolute explicit host path without following its final link.
// os.ErrNotExist is preserved; malformed/unreadable files are not absence.
// Loading creates no directories and contacts neither Docker nor the network.
func Load(name string, defaults Paths) (Loaded, error) {
	if !absolute(name) {
		return Loaded{}, ErrPath
	}
	info, err := os.Lstat(name)
	if err != nil {
		return Loaded{}, err
	}
	if !privateFile(info) || info.Size() > MaxBytes {
		return Loaded{}, ErrPrivate
	}
	f, err := os.OpenFile(name, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return Loaded{}, err
	}
	defer f.Close()
	before, err := f.Stat()
	if err != nil || !privateFile(before) || !os.SameFile(info, before) || before.Size() > MaxBytes {
		return Loaded{}, ErrPrivate
	}
	raw, err := io.ReadAll(io.LimitReader(f, MaxBytes+1))
	if err != nil {
		return Loaded{}, err
	}
	after, err := f.Stat()
	if err != nil || !privateFile(after) || before.Size() != after.Size() || after.Size() != int64(len(raw)) || !before.ModTime().Equal(after.ModTime()) || len(raw) > MaxBytes {
		return Loaded{}, ErrInvalid
	}
	c, err := Parse(raw, defaults)
	if err != nil {
		return Loaded{}, err
	}
	return Loaded{name, contracts.RawDigest(raw), c}, nil
}
