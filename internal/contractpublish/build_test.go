//go:build linux || darwin

package contractpublish

import (
	"bytes"
	"context"
	"github.com/intrusiveai/operator_sandbox/contracts"
	"os"
	"path/filepath"
	"testing"

	"github.com/intrusiveai/operator_sandbox/internal/contractstore"
)

func TestPublicationIsReproducibleAndExact(t *testing.T) {
	source, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	base := t.TempDir()
	rawInterface, err := os.ReadFile(filepath.Join(source, "schemas", contracts.InterfaceDocument))
	if err != nil {
		t.Fatal(err)
	}
	identity, err := contracts.InterfaceIdentity(rawInterface)
	if err != nil {
		t.Fatal(err)
	}

	a, b := filepath.Join(base, "a"), filepath.Join(base, "b")
	first, err := Build(context.Background(), source, a, identity.Version)
	if err != nil {
		t.Fatal(err)
	}
	second, err := Build(context.Background(), source, b, identity.Version)
	if err != nil {
		t.Fatal(err)
	}
	if first.Package != second.Package {
		t.Fatal("nondeterministic package", first, second)
	}
	left, err := os.ReadFile(filepath.Join(a, "package.json"))
	if err != nil {
		t.Fatal(err)
	}
	right, err := os.ReadFile(filepath.Join(b, "package.json"))
	if err != nil || !bytes.Equal(left, right) {
		t.Fatal("different manifests", err)
	}
	if _, err := Build(context.Background(), source, a, identity.Version); err == nil {
		t.Fatal("overwrote published package")
	}
	for _, name := range []string{"source/contracts/python/operator_contracts/protocol.py", "source/contracts/model_test.go", "source/schemas/fixtures/model-codec.json", "source/go.mod"} {
		if _, err := os.Stat(filepath.Join(a, name)); err != nil {
			t.Fatal(name, err)
		}
	}
	if err := os.WriteFile(filepath.Join(a, "source/contracts/model.go"), []byte("changed"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := contractstore.Load(context.Background(), a, first.Package); err == nil {
		t.Fatal("modified validator source accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	c := filepath.Join(base, "c")
	if _, err := Build(ctx, source, c, identity.Version); err == nil {
		t.Fatal("cancelled build succeeded")
	}
	if _, err := os.Stat(c); !os.IsNotExist(err) {
		t.Fatal("cancelled build created output")
	}
}
