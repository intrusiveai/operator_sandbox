//go:build linux || darwin

package interceptor

import (
	"archive/tar"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"strings"
)

// ArchiveLimits are host policy, independent of guest artifact quotas. Zero
// values are rejected; the finalizer must freeze its selected inspection limits.
type ArchiveLimits struct {
	MaxEntries       int
	MaxExpandedBytes int64
}

func DefaultArchiveLimits(maxBytes int64) ArchiveLimits { return ArchiveLimits{10000, maxBytes} }

type ArchiveEntry struct {
	Name   string `json:"name"`
	Bytes  int64  `json:"bytes"`
	SHA256 string `json:"sha256"`
}
type archiveMember struct {
	ArchiveEntry
	offset int64
}

// EvidenceArchive is a structural index only, not a native provenance verdict.
// It shares the download's lifetime. No extraction or evidence publication occurs.
type EvidenceArchive struct {
	download *EvidenceDownload
	members  map[string]archiveMember
	ordered  []ArchiveEntry
}

func (a *EvidenceArchive) Entries() []ArchiveEntry { return append([]ArchiveEntry(nil), a.ordered...) }
func (a *EvidenceArchive) Reader(name string) (*io.SectionReader, error) {
	if a == nil || a.download == nil || a.download.closed {
		return nil, evidenceError("download_closed")
	}
	m, ok := a.members[name]
	if !ok {
		return nil, evidenceError("archive_member_missing")
	}
	return io.NewSectionReader(evidenceReadAt{a.download.file}, m.offset, m.Bytes), nil
}

var evidenceFiles = map[string]bool{
	"manifest.json": true, "session.json": true, "events.jsonl": true, "state.jsonl": true,
	"file-manifest.json": true, "evidence-bundle.json": true, "execution.json": true,
	"artifacts.json": true, "attempts.json": true, "final-evaluation.json": true,
	"restore-source/checkpoint.json": true, "restore-source/state.jsonl": true,
	"host-interruption.json": true, "transport-failure.json": true, "cleanup-pending.json": true,
}

func nativeArchiveName(name string) bool {
	if evidenceFiles[name] {
		return true
	}
	if strings.HasPrefix(name, "blobs/sha256/") {
		return digest.MatchString("sha256:" + strings.TrimPrefix(name, "blobs/sha256/"))
	}
	if strings.HasPrefix(name, "checkpoints/") && strings.HasSuffix(name, ".json") {
		return checkpointID(strings.TrimSuffix(strings.TrimPrefix(name, "checkpoints/"), ".json"))
	}
	return false
}

type archiveReader struct {
	ctx    context.Context
	source io.Reader
	offset int64
	budget int64
}

func (r *archiveReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	if r.budget <= 0 {
		return 0, evidenceError("archive_header_limit_exceeded")
	}
	if int64(len(p)) > r.budget {
		p = p[:r.budget]
	}
	n, err := r.source.Read(p)
	r.offset += int64(n)
	r.budget -= int64(n)
	return n, err
}

// InspectArchive reads every member with bounded memory, checks regular-file
// framing, native paths, entry/expanded-byte limits and content-addressed blobs.
// Headers/PAX metadata have a separate 64 KiB budget per Next call. It rechecks
// the whole download digest, so changed temporary bytes cannot acquire an index.
// Native JSON/journal semantics and identity still require the later verifier.
func (d *EvidenceDownload) InspectArchive(ctx context.Context, limits ArchiveLimits) (*EvidenceArchive, error) {
	if limits.MaxEntries <= 0 || limits.MaxEntries > 100000 || limits.MaxExpandedBytes <= 0 || limits.MaxExpandedBytes > maxEvidenceBytes {
		return nil, ErrRequest
	}
	input, err := d.Reader()
	if err != nil {
		return nil, err
	}
	if d.receipt.Bytes%512 != 0 {
		return nil, evidenceError("invalid_archive")
	}
	whole := sha256.New()
	source := &archiveReader{ctx: ctx, source: io.TeeReader(input, whole)}
	tr := tar.NewReader(source)
	result := &EvidenceArchive{download: d, members: map[string]archiveMember{}, ordered: []ArchiveEntry{}}
	var expanded int64
	buffer := make([]byte, 64<<10)
	for {
		before := source.offset
		source.budget = 64 << 10
		h, err := tr.Next()
		if err == io.EOF {
			// Native tar.Writer emits two zero footer blocks, with no appended
			// payload or concatenated archive. tar.Reader alone accepts no footer.
			if source.offset-before < 1024 || source.offset != d.receipt.Bytes {
				return nil, evidenceError("invalid_archive_footer")
			}
			break
		}
		if err != nil {
			return nil, evidenceError("invalid_archive")
		}
		if len(result.ordered) >= limits.MaxEntries {
			return nil, evidenceError("archive_entry_limit_exceeded")
		}
		if h.Typeflag != tar.TypeReg || h.Linkname != "" || h.Mode&^0777 != 0 || len(h.Xattrs) != 0 || h.Devmajor != 0 || h.Devminor != 0 || !nativeArchiveName(h.Name) || h.Size < 0 {
			return nil, evidenceError("invalid_archive_member")
		}
		for key := range h.PAXRecords {
			// Native tar.Writer only needs a size extension for large blobs.
			// Reject sparse maps, path overrides and arbitrary extension metadata.
			if key != "size" {
				return nil, evidenceError("invalid_archive_extension")
			}
		}
		if _, exists := result.members[h.Name]; exists {
			return nil, evidenceError("duplicate_archive_member")
		}
		if h.Size > limits.MaxExpandedBytes-expanded {
			return nil, evidenceError("archive_expanded_limit_exceeded")
		}
		member := archiveMember{ArchiveEntry: ArchiveEntry{Name: h.Name, Bytes: h.Size}, offset: source.offset}
		source.budget = h.Size + 1
		hash := sha256.New()
		n, err := io.CopyBuffer(hash, tr, buffer)
		if err != nil || n != h.Size {
			return nil, evidenceError("invalid_archive_member")
		}
		member.SHA256 = "sha256:" + hex.EncodeToString(hash.Sum(nil))
		if strings.HasPrefix(h.Name, "blobs/sha256/") && member.SHA256 != "sha256:"+strings.TrimPrefix(h.Name, "blobs/sha256/") {
			return nil, evidenceError("archive_blob_digest_mismatch")
		}
		expanded += h.Size
		result.members[h.Name] = member
		result.ordered = append(result.ordered, member.ArchiveEntry)
	}
	for _, name := range []string{"manifest.json", "session.json", "events.jsonl", "state.jsonl", "file-manifest.json", "evidence-bundle.json"} {
		if _, ok := result.members[name]; !ok {
			return nil, evidenceError("archive_member_missing")
		}
	}
	_, checkpoint := result.members["restore-source/checkpoint.json"]
	_, journal := result.members["restore-source/state.jsonl"]
	if checkpoint != journal {
		return nil, evidenceError("archive_restore_source_incomplete")
	}
	if "sha256:"+hex.EncodeToString(whole.Sum(nil)) != d.receipt.SHA256 || ctx.Err() != nil {
		return nil, evidenceError("archive_integrity_failed")
	}
	return result, nil
}
