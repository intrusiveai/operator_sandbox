//go:build linux || darwin

// Package hostconfig reads administrator-owned installation settings. It never
// sources settings from campaigns, the current directory or Docker environment.
package hostconfig

import (
	"bytes"
	"encoding/json"
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

	"github.com/intrusiveai/operator_sandbox/contracts"
	"github.com/intrusiveai/operator_sandbox/internal/campaign"
	"github.com/intrusiveai/operator_sandbox/internal/campaignlimits"
	"github.com/intrusiveai/operator_sandbox/internal/dockercontrol"
)

const MaxBytes = 64 << 10
const DefaultSpoolBytes int64 = 512 << 20
const DefaultEvidenceBytes int64 = 4 << 30

var (
	ErrInvalid  = errors.New("invalid configuration: use only documented sections, fields and scalar types")
	ErrPrivate  = errors.New("configuration must be a private regular file owned by the service user or root")
	ErrImage    = errors.New("engine.image must select a local Docker image name, repository digest or full image ID")
	ErrPath     = errors.New("configuration paths must be absolute and clean, without control characters")
	ErrSpool    = errors.New("spool.max_bytes must be a positive decimal integer no greater than 9007199254740991")
	ErrEvidence = errors.New("evidence.max_archive_bytes must be a positive decimal integer no greater than 9007199254740991")
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
	Limits campaignlimits.Host `json:"limits"`
	Model  struct {
		ProfileFile string `json:"profile_file"`
	} `json:"model"`
	Credentials struct {
		File string `json:"file"`
	} `json:"credentials"`
	Contract struct {
		Directory string `json:"directory"`
		Version   string `json:"version"`
		Digest    string `json:"digest"`
	} `json:"contract"`
	Target struct {
		ProfileFile string `json:"profile_file"`
	} `json:"target"`
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
	Evidence struct {
		MaxArchiveBytes int64 `json:"max_archive_bytes"`
	} `json:"evidence"`
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

// Parse accepts a closed YAML map with one nested harness-limits map. Aliases, anchors, merge keys, multiple
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
	c.Evidence.MaxArchiveBytes = DefaultEvidenceBytes
	c.Limits, err = campaignlimits.ParseHost([]byte(`{}`))
	if err != nil {
		return Config{}, err
	}
	fields := map[string]map[string]*string{
		"model":       {"profile_file": &c.Model.ProfileFile},
		"credentials": {"file": &c.Credentials.File},
		"contract":    {"directory": &c.Contract.Directory, "version": &c.Contract.Version, "digest": &c.Contract.Digest},
		"target":      {"profile_file": &c.Target.ProfileFile},
		"engine":      {"image": &c.Engine.Image},
		"docker":      {"endpoint": &c.Docker.Endpoint, "executable": &c.Docker.Executable},
		"state":       {"root": &c.State.Root},
		"cache":       {"release_directory": &c.Cache.ReleaseDirectory},
	}
	for section, node := range sections {
		if section == "limits" {
			raw, e := limitsJSON(node, true)
			if e != nil {
				return Config{}, e
			}
			c.Limits, e = campaignlimits.ParseHost(raw)
			if e != nil {
				return Config{}, e
			}
			continue
		}
		allowed, ok := fields[section]
		if !ok && section != "spool" && section != "evidence" {
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
			if section == "spool" || section == "evidence" {
				key, dst, invalid := "max_bytes", &c.Spool.MaxBytes, ErrSpool
				if section == "evidence" {
					key, dst, invalid = "max_archive_bytes", &c.Evidence.MaxArchiveBytes, ErrEvidence
				}
				if name != key || value.Tag != "!!int" || !decimal.MatchString(value.Value) {
					return Config{}, invalid
				}
				n, err := strconv.ParseInt(value.Value, 10, 64)
				if err != nil || n > contracts.MaxSafeInteger {
					return Config{}, invalid
				}
				*dst = n
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
	if c.Contract.Directory != "" || c.Contract.Version != "" || c.Contract.Digest != "" {
		if !absolute(c.Contract.Directory) || !regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`).MatchString(c.Contract.Version) || !regexp.MustCompile(`^sha256:[0-9a-f]{64}$`).MatchString(c.Contract.Digest) {
			return Config{}, errors.New("contract requires an absolute directory, release version and independently installed SHA-256 digest")
		}
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
	if (c.Target.ProfileFile != "" && !absolute(c.Target.ProfileFile)) || (c.Credentials.File != "" && !absolute(c.Credentials.File)) || (c.Model.ProfileFile != "" && !absolute(c.Model.ProfileFile)) {
		return Config{}, ErrPath
	}
	// Reusable approvals must not be inside the purgeable campaign tree.
	campaigns := filepath.Join(c.State.Root, "campaigns")
	if c.Cache.ReleaseDirectory == campaigns || strings.HasPrefix(c.Cache.ReleaseDirectory, campaigns+string(filepath.Separator)) {
		return Config{}, ErrPath
	}
	return c, nil
}

// Preserve strict YAML integer handling before the shared JSON-safe validator.
func limitsJSON(node *yaml.Node, allowHarness bool) ([]byte, error) {
	values, err := mapping(node)
	if err != nil {
		return nil, err
	}
	out := map[string]any{}
	for key, value := range values {
		if key == "harness" && allowHarness {
			raw, err := limitsJSON(value, false)
			if err != nil {
				return nil, err
			}
			out[key] = json.RawMessage(raw)
			continue
		}
		if value.Kind != yaml.ScalarNode || value.Tag != "!!int" || value.Anchor != "" || (value.Value != "0" && !decimal.MatchString(value.Value)) {
			return nil, ErrInvalid
		}
		n, err := strconv.ParseInt(value.Value, 10, 64)
		if err != nil || n > contracts.MaxSafeInteger {
			return nil, ErrInvalid
		}
		out[key] = n
	}
	return json.Marshal(out)
}

func privateFile(info os.FileInfo) bool {
	st, ok := info.Sys().(*syscall.Stat_t)
	return ok && info.Mode().IsRegular() && info.Mode().Perm()&0077 == 0 && st.Nlink == 1 && (st.Uid == 0 || st.Uid == uint32(os.Geteuid()))
}

// Load reads one absolute explicit host path without following its final link.
// os.ErrNotExist is preserved; malformed/unreadable files are not absence.
// Loading creates no directories and contacts neither Docker nor the network.
func Load(name string, defaults Paths) (Loaded, error) {
	raw, err := ReadPrivate(name, MaxBytes)
	if err != nil {
		return Loaded{}, err
	}
	c, err := Parse(raw, defaults)
	if err != nil {
		return Loaded{}, err
	}
	return Loaded{name, contracts.RawDigest(raw), c}, nil
}

// ReadPrivate captures an administrator-owned bounded file using the same
// ownership, no-follow and stability checks as installation configuration.
func ReadPrivate(name string, maximum int64) ([]byte, error) {
	if maximum < 1 || maximum > 1<<20 {
		return nil, ErrInvalid
	}
	if !absolute(name) {
		return nil, ErrPath
	}
	info, err := os.Lstat(name)
	if err != nil {
		return nil, err
	}
	if !privateFile(info) || info.Size() > maximum {
		return nil, ErrPrivate
	}
	f, err := os.OpenFile(name, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	before, err := f.Stat()
	if err != nil || !privateFile(before) || !os.SameFile(info, before) || before.Size() > maximum {
		return nil, ErrPrivate
	}
	raw, err := io.ReadAll(io.LimitReader(f, maximum+1))
	if err != nil {
		return nil, err
	}
	after, err := f.Stat()
	if err != nil || !privateFile(after) || before.Size() != after.Size() || after.Size() != int64(len(raw)) || !before.ModTime().Equal(after.ModTime()) || int64(len(raw)) > maximum {
		return nil, ErrInvalid
	}
	return raw, nil
}
