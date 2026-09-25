package capabilities

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestLoadExportFiles(t *testing.T) {
	c := catalogForTest(t)
	native := fixture(t, "interceptor-export.json")
	e := mustExport(t, c, native)
	for _, kind := range []string{"regular", "public symlink", "native symlink", "public hardlink", "native hardlink", "wrong native", "missing native", "directory"} {
		t.Run(kind, func(t *testing.T) {
			dir := t.TempDir()
			public := filepath.Join(dir, "public.json")
			companion := filepath.Join(dir, e.CompanionName())
			write := func(path string, raw []byte) {
				t.Helper()
				if err := os.WriteFile(path, raw, 0600); err != nil {
					t.Fatal(err)
				}
			}
			write(public, e.PublicJSON())
			if kind != "missing native" {
				write(companion, native)
			}
			switch kind {
			case "public symlink", "native symlink", "public hardlink", "native hardlink":
				target := public
				if kind == "native symlink" || kind == "native hardlink" {
					target = companion
				}
				old := target + ".original"
				if err := os.Rename(target, old); err != nil {
					t.Fatal(err)
				}
				link := os.Link
				if kind == "public symlink" || kind == "native symlink" {
					link = os.Symlink
				}
				if err := link(old, target); err != nil {
					t.Fatal(err)
				}
			case "wrong native":
				write(companion, append(native, ' '))
			case "directory":
				if err := os.Remove(companion); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(companion, 0700); err != nil {
					t.Fatal(err)
				}
			}
			loaded, err := LoadExport(context.Background(), c, public, "delivery-example")
			if kind == "regular" {
				if err != nil {
					t.Fatal(err)
				}
				if loaded.SourceDigest() != e.SourceDigest() {
					t.Fatal("wrong source")
				}
			} else if err == nil {
				t.Fatal("accepted unsafe or unverified files")
			}
		})
	}
}
