//go:build linux || darwin

package interceptor

import (
	"archive/tar"
	"bytes"
	"context"
	"io"
	"net/http"
	"testing"
)

type testMember struct {
	header tar.Header
	data   []byte
}

func archiveMembers() []testMember {
	var members []testMember
	for _, name := range []string{"manifest.json", "session.json", "events.jsonl", "state.jsonl", "file-manifest.json", "evidence-bundle.json"} {
		members = append(members, testMember{header: tar.Header{Name: name, Mode: 0600}, data: []byte(`{}`)})
	}
	return members
}
func archiveBytes(t *testing.T, members []testMember) []byte {
	t.Helper()
	var b bytes.Buffer
	w := tar.NewWriter(&b)
	for _, m := range members {
		m.header.Size = int64(len(m.data))
		if err := w.WriteHeader(&m.header); err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write(m.data); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}
func downloadBytes(t *testing.T, data []byte) *EvidenceDownload {
	t.Helper()
	c := evidenceClient(func(*http.Request) (*http.Response, error) { return evidenceResponse(data), nil })
	d, err := c.DownloadEvidence(context.Background(), evidenceQuery(), privateEvidenceDir(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := d.Close(); err != nil {
			t.Error(err)
		}
	})
	return d
}

func TestArchiveInspectionAndBoundedMemberReaders(t *testing.T) {
	members := archiveMembers()
	blob := []byte("native blob bytes")
	name := "blobs/sha256/" + rawDigest(blob)[7:]
	members = append(members, testMember{header: tar.Header{Name: name, Mode: 0640}, data: blob})
	d := downloadBytes(t, archiveBytes(t, members))
	a, err := d.InspectArchive(context.Background(), DefaultArchiveLimits(4<<30))
	if err != nil {
		t.Fatal(err)
	}
	entries := a.Entries()
	if len(entries) != len(members) || entries[len(entries)-1].SHA256 != rawDigest(blob) {
		t.Fatal(entries)
	}
	entries[0].Name = "changed"
	if a.Entries()[0].Name != "manifest.json" {
		t.Fatal("mutable index")
	}
	r, err := a.Reader(name)
	if err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(r)
	if err != nil || !bytes.Equal(got, blob) {
		t.Fatal(string(got), err)
	}
	if _, err := a.Reader("../outside"); err == nil {
		t.Fatal("unknown path accepted")
	}
	if err := d.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Reader(name); err == nil {
		t.Fatal("closed download accepted")
	}
}

func TestArchiveRejectsUnsafeMembersAndLimits(t *testing.T) {
	for name, change := range map[string]func([]testMember) []testMember{
		"traversal":  func(m []testMember) []testMember { m[0].header.Name = "../manifest.json"; return m },
		"absolute":   func(m []testMember) []testMember { m[0].header.Name = "/manifest.json"; return m },
		"alias":      func(m []testMember) []testMember { m[0].header.Name = "./manifest.json"; return m },
		"case alias": func(m []testMember) []testMember { m[0].header.Name = "Manifest.json"; return m },
		"backslash":  func(m []testMember) []testMember { m[0].header.Name = `checkpoints\evil.json`; return m },
		"duplicate":  func(m []testMember) []testMember { return append(m, m[0]) },
		"missing":    func(m []testMember) []testMember { return m[1:] },
		"link": func(m []testMember) []testMember {
			m[0].header.Typeflag = tar.TypeSymlink
			m[0].header.Linkname = "/etc/passwd"
			m[0].data = nil
			return m
		},
		"hardlink": func(m []testMember) []testMember {
			m[0].header.Typeflag = tar.TypeLink
			m[0].header.Linkname = "session.json"
			m[0].data = nil
			return m
		},
		"directory": func(m []testMember) []testMember { m[0].header.Typeflag = tar.TypeDir; m[0].data = nil; return m },
		"device":    func(m []testMember) []testMember { m[0].header.Typeflag = tar.TypeChar; m[0].data = nil; return m },
		"setuid":    func(m []testMember) []testMember { m[0].header.Mode = 04755; return m },
		"pax extension": func(m []testMember) []testMember {
			m[0].header.PAXRecords = map[string]string{"custom.key": "value"}
			return m
		},
		"large headers": func(m []testMember) []testMember {
			m[0].header.PAXRecords = map[string]string{"custom.key": string(bytes.Repeat([]byte("x"), 65<<10))}
			return m
		},
		"bad blob digest": func(m []testMember) []testMember {
			return append(m, testMember{header: tar.Header{Name: "blobs/sha256/" + rawDigest([]byte("other"))[7:], Mode: 0600}, data: []byte("bad")})
		},
		"half restore provenance": func(m []testMember) []testMember {
			return append(m, testMember{header: tar.Header{Name: "restore-source/checkpoint.json", Mode: 0600}, data: []byte(`{}`)})
		},
	} {
		t.Run(name, func(t *testing.T) {
			d := downloadBytes(t, archiveBytes(t, change(archiveMembers())))
			a, err := d.InspectArchive(context.Background(), DefaultArchiveLimits(4<<30))
			if a != nil || err == nil {
				t.Fatal("accepted", name)
			}
		})
	}
	d := downloadBytes(t, archiveBytes(t, archiveMembers()))
	for _, limits := range []ArchiveLimits{{5, 4 << 30}, {100, 11}, {0, 100}, {100001, 100}, {10, 0}} {
		if _, err := d.InspectArchive(context.Background(), limits); err == nil {
			t.Fatal("ignored limits", limits)
		}
	}
	if _, err := d.InspectArchive(context.Background(), ArchiveLimits{6, 12}); err != nil {
		t.Fatal("exact limits", err)
	}
}

func TestArchiveFooterTruncationAndMutation(t *testing.T) {
	raw := archiveBytes(t, archiveMembers())
	for name, data := range map[string][]byte{
		"no footer": raw[:len(raw)-1024], "one footer block": raw[:len(raw)-512],
		"truncated member": raw[:512+1], "trailing junk": append(bytes.Clone(raw), []byte("junk")...),
		"concatenated": append(bytes.Clone(raw), raw...), "extra padding": append(bytes.Clone(raw), make([]byte, 512)...),
	} {
		t.Run(name, func(t *testing.T) {
			d := downloadBytes(t, data)
			if _, err := d.InspectArchive(context.Background(), DefaultArchiveLimits(4<<30)); err == nil {
				t.Fatal("accepted", name)
			}
		})
	}
	d := downloadBytes(t, raw)
	// A locally changed metadata byte still parses as tar, but must not inherit
	// the previous transfer digest. Native semantic validation is a later layer.
	if _, err := d.file.WriteAt([]byte(`[]`), 512); err != nil {
		t.Fatal(err)
	}
	_, err := d.InspectArchive(context.Background(), DefaultArchiveLimits(4<<30))
	assertEvidenceError(t, err, "archive_integrity_failed")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := d.InspectArchive(ctx, DefaultArchiveLimits(4<<30)); err == nil {
		t.Fatal("ignored cancellation")
	}
}
