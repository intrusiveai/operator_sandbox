package contracts

import (
	"errors"
	"io/fs"
	"path"
	"sort"
	"strings"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

var ErrSchema = errors.New("contract schema validation failed")
var ErrCatalog = errors.New("invalid installed contract catalog")

type offlineLoader struct{}

func (offlineLoader) Load(string) (any, error) { return nil, ErrCatalog }

type Catalog struct {
	schemas map[string]*jsonschema.Schema
	digest  string
	sources map[string][]byte
}

// LoadCatalog loads only resources explicitly listed in an installed, trusted
// schema filesystem. It never fetches network/file references outside that catalog.
func LoadCatalog(files fs.FS) (*Catalog, error) {
	raw, err := fs.ReadFile(files, "catalog.json")
	if err != nil {
		return nil, ErrCatalog
	}
	value, err := Decode(raw, OrdinaryLimit)
	if err != nil {
		return nil, ErrCatalog
	}
	entries, ok := value.(map[string]any)
	if !ok || len(entries) == 0 {
		return nil, ErrCatalog
	}
	digest, err := objectDigest(value, OrdinaryLimit)
	if err != nil {
		return nil, ErrCatalog
	}
	compiler := jsonschema.NewCompiler()
	compiler.DefaultDraft(jsonschema.Draft2020)
	compiler.UseLoader(offlineLoader{})
	ids := make([]string, 0, len(entries))
	seen := map[string]bool{}
	sources := map[string][]byte{}
	for id, file := range entries {
		name, ok := file.(string)
		if !ok || !fs.ValidPath(name) || path.Base(name) != name || strings.Contains(name, `\`) || !strings.HasSuffix(name, ".schema.json") || seen[name] {
			return nil, ErrCatalog
		}
		seen[name] = true
		raw, err = fs.ReadFile(files, name)
		if err != nil {
			return nil, ErrCatalog
		}
		sources[id] = raw
		schema, err := Decode(raw, OrdinaryLimit)
		if err != nil {
			return nil, ErrCatalog
		}
		object, ok := schema.(map[string]any)
		if !ok || object["$id"] != id || object["$schema"] != "https://json-schema.org/draft/2020-12/schema" {
			return nil, ErrCatalog
		}
		if err = compiler.AddResource(id, schema); err != nil {
			return nil, ErrCatalog
		}
		ids = append(ids, id)
	}
	result := &Catalog{schemas: map[string]*jsonschema.Schema{}, digest: digest, sources: sources}
	sort.Strings(ids)
	for _, id := range ids {
		schema, err := compiler.Compile(id)
		if err != nil {
			return nil, ErrCatalog
		}
		result.schemas[id] = schema
	}
	return result, nil
}
func (c *Catalog) IDs() []string {
	ids := make([]string, 0, len(c.schemas))
	for id := range c.schemas {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// Validate performs strict wire decoding and structural schema validation only.
// Receipt lookup, lifecycle state, digest and operation semantics are separate gates.
func (c *Catalog) Validate(id string, raw []byte, maximum int) (any, error) {
	schema, ok := c.schemas[id]
	if !ok {
		return nil, ErrCatalog
	}
	value, err := Decode(raw, maximum)
	if err != nil {
		return nil, err
	}
	if err = schema.Validate(value); err != nil {
		return nil, ErrSchema
	}
	return value, nil
}
