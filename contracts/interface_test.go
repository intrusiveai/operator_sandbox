package contracts

import (
	"bytes"
	"github.com/intrusiveai/operator_sandbox/schemas"
	"reflect"
	"testing"
)

func TestInterfaceIdentityAndIndependentImplementation(t *testing.T) {
	p, err := LoadProtocol(schemas.Files)
	if err != nil {
		t.Fatal(err)
	}
	files, profiles := packageTestFiles(t)
	raw := files[InterfaceDocument]
	identity, err := InterfaceIdentity(raw)
	if err != nil {
		t.Fatal(err)
	}
	first, pin, err := p.BuildPackageManifest(identity.Version, files, profiles)
	if err != nil {
		t.Fatal(err)
	}
	legacy := pin
	legacy.Digest = RawDigest(first)
	if err := p.VerifyPackage(first, files, legacy); err == nil {
		t.Fatal("legacy manifest hash interpreted as interface hash")
	}
	files["source/validator.py"] = []byte("# Independent implementation change\n")
	second, next, err := p.BuildPackageManifest(identity.Version, files, profiles)
	if err != nil {
		t.Fatal(err)
	}
	if next != pin || bytes.Equal(first, second) {
		t.Fatal("implementation affected interface identity or escaped file inventory")
	}
	if _, err = p.LoadVerifiedProtocol(second, files, pin); err != nil {
		t.Fatal(err)
	}
	files[InterfaceDocument] = append(append([]byte(nil), raw...), '\n')
	if err = p.VerifyPackage(second, files, pin); err == nil {
		t.Fatal("exact-byte mismatch accepted")
	}
	files[InterfaceDocument] = raw
	files["operations.json"] = []byte(`{"operations":[]}`)
	if _, _, err = p.BuildPackageManifest(identity.Version, files, profiles); err == nil {
		t.Fatal("registry drift accepted")
	}
}

func TestInterfaceStandaloneValidation(t *testing.T) {
	raw, err := schemas.Files.ReadFile(InterfaceDocument)
	if err != nil {
		t.Fatal(err)
	}
	pin, err := InterfaceIdentity(raw)
	if err != nil {
		t.Fatal(err)
	}
	p, err := LoadInterfaceProtocol(raw, pin)
	if err != nil {
		t.Fatal(err)
	}
	existing, err := LoadProtocol(schemas.Files)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(p.RegistryDigests(), existing.RegistryDigests()) {
		t.Fatal("standalone registry differs")
	}
	resources, err := InterfaceResources(raw)
	if err != nil {
		t.Fatal(err)
	}
	for name, content := range resources {
		if name == InterfaceDocument {
			continue
		}
		expected, err := schemas.Files.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		// References may be made absolute during extraction; re-projection proves equality.
		if name == "catalog.json" || name == "operations.json" {
			a, _ := CanonicalDigest(content, PackageFileLimit)
			b, _ := CanonicalDigest(expected, PackageFileLimit)
			if a != b {
				t.Fatal(name)
			}
		}
	}
	pin.Digest = RawDigest([]byte("different"))
	if _, err = LoadInterfaceProtocol(raw, pin); err == nil {
		t.Fatal("unapproved document accepted")
	}
}
