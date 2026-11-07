package contracts

import (
	"encoding/json"
	"reflect"
	"regexp"
	"strings"
)

const InterfaceDocument = "asyncapi.json"

var interfaceVersion = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`)

const InterfaceSchemaFormat = "application/schema+json;version=draft-2020-12"

// InterfaceIdentity hashes exact document bytes. It is not a package inventory
// hash or proof of implementation conformance.
func InterfaceIdentity(raw []byte) (PackageIdentity, error) {
	v, err := Decode(raw, PackageFileLimit)
	if err != nil {
		return PackageIdentity{}, err
	}
	m, ok := v.(map[string]any)
	if !ok || m["asyncapi"] != "3.0.0" {
		return PackageIdentity{}, ErrProtocol
	}
	info, ok := m["info"].(map[string]any)
	if !ok {
		return PackageIdentity{}, ErrProtocol
	}
	version, ok := info["version"].(string)
	if !ok || len(version) > 128 || !interfaceVersion.MatchString(version) {
		return PackageIdentity{}, ErrProtocol
	}
	return PackageIdentity{Version: version, Digest: RawDigest(raw)}, nil
}

// verifyInterface binds executable validator resources to the approved interface.
// Package implementation code is never executed by this verifier.
func verifyInterface(files map[string][]byte, expected PackageIdentity) error {
	raw := files[InterfaceDocument]
	identity, err := InterfaceIdentity(raw)
	if err != nil || identity != expected {
		return ErrProtocol
	}
	value, _ := Decode(raw, PackageFileLimit)
	document := value.(map[string]any)
	catalogValue, err := Decode(files["catalog.json"], OrdinaryLimit)
	if err != nil {
		return err
	}
	catalog, ok := catalogValue.(map[string]any)
	if !ok || !reflect.DeepEqual(document["x-operator-catalog"], catalog) {
		return ErrProtocol
	}
	registry, err := Decode(files["operations.json"], OrdinaryLimit)
	if err != nil || !reflect.DeepEqual(document["x-operator-registry"], registry) {
		return ErrProtocol
	}
	components, ok := document["components"].(map[string]any)
	if !ok {
		return ErrProtocol
	}
	schemas, ok := components["schemas"].(map[string]any)
	if !ok || len(schemas) != len(catalog) {
		return ErrProtocol
	}
	for id, filename := range catalog {
		name, ok := filename.(string)
		if !ok {
			return ErrProtocol
		}
		schema, err := Decode(files[name], PackageFileLimit)
		if err != nil {
			return err
		}
		object, ok := schema.(map[string]any)
		if !ok || object["$id"] != id {
			return ErrProtocol
		}
		projection, err := projectInterfaceSchema(schema, catalog, id)
		if err != nil {
			return err
		}
		expected := map[string]any{"schemaFormat": InterfaceSchemaFormat, "schema": projection}
		if !reflect.DeepEqual(schemas[name], expected) {
			return ErrProtocol
		}
	}
	return nil
}

func projectInterfaceSchema(value any, catalog map[string]any, current string) (any, error) {
	switch v := value.(type) {
	case []any:
		result := make([]any, len(v))
		for i, child := range v {
			projected, err := projectInterfaceSchema(child, catalog, current)
			if err != nil {
				return nil, err
			}
			result[i] = projected
		}
		return result, nil
	case map[string]any:
		result := map[string]any{}
		for key, child := range v {
			if key == "$id" {
				continue
			}
			if key == "$ref" {
				ref, ok := child.(string)
				if !ok {
					return nil, ErrProtocol
				}
				resource, fragment, _ := strings.Cut(ref, "#")
				if resource == "" {
					resource = current
				}
				name, ok := catalog[resource].(string)
				if !ok {
					return nil, ErrProtocol
				}
				result[key] = "#/components/schemas/" + name + "/schema" + fragment
			} else {
				projected, err := projectInterfaceSchema(child, catalog, current)
				if err != nil {
					return nil, err
				}
				result[key] = projected
			}
		}
		return result, nil
	default:
		return value, nil
	}
}

// InterfaceResources extracts the schema/registry projections for offline
// counterpart conformance tooling without requiring an Operator source revision.
func InterfaceResources(raw []byte) (map[string][]byte, error) {
	if _, err := InterfaceIdentity(raw); err != nil {
		return nil, err
	}
	v, _ := Decode(raw, PackageFileLimit)
	doc := v.(map[string]any)
	catalog, ok := doc["x-operator-catalog"].(map[string]any)
	if !ok {
		return nil, ErrProtocol
	}
	components, ok := doc["components"].(map[string]any)
	if !ok {
		return nil, ErrProtocol
	}
	schemas, ok := components["schemas"].(map[string]any)
	if !ok {
		return nil, ErrProtocol
	}
	reverse := map[string]string{}
	for id, value := range catalog {
		name, ok := value.(string)
		if !ok {
			return nil, ErrProtocol
		}
		reverse[name] = id
	}
	var restore func(any) (any, error)
	restore = func(value any) (any, error) {
		switch v := value.(type) {
		case []any:
			out := make([]any, len(v))
			for i, c := range v {
				x, e := restore(c)
				if e != nil {
					return nil, e
				}
				out[i] = x
			}
			return out, nil
		case map[string]any:
			out := map[string]any{}
			for k, c := range v {
				if k == "$ref" {
					ref, ok := c.(string)
					if !ok || !strings.HasPrefix(ref, "#/components/schemas/") {
						return nil, ErrProtocol
					}
					name, fragment, ok := strings.Cut(strings.TrimPrefix(ref, "#/components/schemas/"), "/schema")
					id, exists := reverse[name]
					if !ok || !exists {
						return nil, ErrProtocol
					}
					out[k] = id
					if fragment != "" {
						out[k] = id + "#" + fragment
					}
				} else {
					x, e := restore(c)
					if e != nil {
						return nil, e
					}
					out[k] = x
				}
			}
			return out, nil
		default:
			return value, nil
		}
	}
	files := map[string][]byte{InterfaceDocument: append([]byte(nil), raw...)}
	for name, id := range reverse {
		entry, ok := schemas[name].(map[string]any)
		if !ok || entry["schemaFormat"] != InterfaceSchemaFormat {
			return nil, ErrProtocol
		}
		value, err := restore(entry["schema"])
		if err != nil {
			return nil, err
		}
		schema, ok := value.(map[string]any)
		if !ok {
			return nil, ErrProtocol
		}
		schema["$id"] = id
		files[name], err = json.Marshal(schema)
		if err != nil {
			return nil, err
		}
	}
	var err error
	files["catalog.json"], err = json.Marshal(catalog)
	if err != nil {
		return nil, err
	}
	files["operations.json"], err = json.Marshal(doc["x-operator-registry"])
	if err != nil {
		return nil, err
	}
	identity, _ := InterfaceIdentity(raw)
	if err = verifyInterface(files, identity); err != nil {
		return nil, err
	}
	return files, nil
}

// LoadInterfaceProtocol compiles the approved interface without package source.
// Callers must obtain the expected identity through trusted installation metadata.
func LoadInterfaceProtocol(raw []byte, expected PackageIdentity) (*Protocol, error) {
	identity, err := InterfaceIdentity(raw)
	if err != nil || identity != expected {
		return nil, ErrProtocol
	}
	files, err := InterfaceResources(raw)
	if err != nil {
		return nil, err
	}
	protocol, err := LoadProtocol(resourceFS(files))
	if err != nil {
		return nil, err
	}
	protocol.packageIdentity = &expected
	return protocol, nil
}
