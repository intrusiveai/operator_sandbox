//go:build linux || darwin

package livequalification

import (
	"os"
	"path/filepath"
	"testing"
)

func TestEvidenceExclusionAndPrivateDirectory(t *testing.T) {
	dir := t.TempDir()
	name := filepath.Join(dir, "evidence.jsonl")
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	f, err := CreateEvidence(name)
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
	if _, err = CreateEvidence(name); err == nil {
		t.Fatal("overwrote prior evidence")
	}
	link := filepath.Join(dir, "link")
	if err = os.Symlink(name, link); err != nil {
		t.Fatal(err)
	}
	if _, err = CreateEvidence(link); err == nil {
		t.Fatal("followed symlink")
	}
	parentLink := filepath.Join(t.TempDir(), "parent-link")
	os.Symlink(dir, parentLink)
	if _, err = CreateEvidence(filepath.Join(parentLink, "new")); err == nil {
		t.Fatal("followed parent link")
	}
	os.Chmod(dir, 0755)
	if _, err = CreateEvidence(filepath.Join(dir, "new")); err == nil {
		t.Fatal("accepted public parent")
	}
}
