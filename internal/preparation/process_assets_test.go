//go:build linux || darwin

package preparation_test

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/intrusiveai/operator_sandbox/contracts"
	"github.com/intrusiveai/operator_sandbox/internal/contractpublish"
	"github.com/intrusiveai/operator_sandbox/internal/contractstore"
)

// Only immutable prerequisites are shared. Every campaign keeps its own input
// objects, writable tree, journal, target peer and transport. Integrity/mutation
// tests in contractstore and contractpublish continue to create fresh packages.
var processAssetRoot string
var processAssetDirectory = sync.OnceValues(func() (string, error) {
	var err error
	processAssetRoot, err = os.MkdirTemp("", "operator-process-assets-")
	return processAssetRoot, err
})

func TestMain(m *testing.M) {
	code := m.Run()
	if processAssetRoot != "" {
		if err := os.RemoveAll(processAssetRoot); err != nil {
			fmt.Fprintln(os.Stderr, "remove process test assets:", err)
			code = 1
		}
	}
	os.Exit(code)
}

var loadProcessContract = sync.OnceValues(func() (*contractstore.Loaded, error) {
	root, err := processAssetDirectory()
	if err != nil {
		return nil, err
	}
	source, err := filepath.Abs("../..")
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	pkg := filepath.Join(root, "contracts")
	report, err := contractpublish.Build(ctx, source, pkg, "0.9.0")
	if err != nil {
		return nil, err
	}
	return contractstore.Load(ctx, pkg, report.Package)
})

func processContract(t *testing.T) (string, *contracts.Protocol) {
	t.Helper()
	loaded, err := loadProcessContract()
	if err != nil {
		t.Fatal(err)
	}
	return loaded.Report().Directory, loaded.Protocol()
}

var buildProcessCLI = sync.OnceValues(func() (string, error) {
	root, err := processAssetDirectory()
	if err != nil {
		return "", err
	}
	binary := filepath.Join(root, "operatorctl")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	build := exec.CommandContext(ctx, "go", "build", "-o", binary, "./cmd/operatorctl")
	build.Dir = "../.."
	if output, err := build.CombinedOutput(); err != nil {
		return "", fmt.Errorf("build process-test CLI: %w: %s", err, output)
	}
	return binary, nil
})

func processCLI(t *testing.T) string {
	t.Helper()
	binary, err := buildProcessCLI()
	if err != nil {
		t.Fatal(err)
	}
	return binary
}
