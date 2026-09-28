//go:build linux || darwin

package campaign

import (
	"context"
	"errors"
	"io"
	"os"
	"regexp"
	"syscall"
	"time"
)

var stageName = regexp.MustCompile(`^stage-[A-Za-z0-9]{26}$`)

type TransientCleanup struct {
	CampaignID     string `json:"campaign_id"`
	ManifestDigest string `json:"run_manifest_digest"`
	Transport      string `json:"transport"`
	Inputs         string `json:"inputs"`
	Policies       string `json:"policies"`
	RecordedAt     string `json:"recorded_at"`
}

// CleanupTransient requires installation ownership and confirmed absence of this
// campaign's container (or a verified never-created launch). It takes the writer
// lock but never reopens execution or trusts paths from journal/spool messages.
func CleanupTransient(ctx context.Context, stateRoot, id string, containerAbsent bool) (out TransientCleanup, err error) {
	out = TransientCleanup{CampaignID: id, Transport: "unconfirmed", Inputs: "retained-unarchived", Policies: "unconfirmed"}
	if !containerAbsent {
		return out, ErrInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	r, err := openCampaign(stateRoot, id)
	if err != nil {
		return out, err
	}
	defer r.Close()
	l, err := lock(r, false)
	if err != nil {
		return out, err
	}
	defer l.Close()
	m, digest, err := loadManifest(r, id)
	if err != nil {
		return out, err
	}
	out.ManifestDigest = digest
	retained := 0
	report, inspectErr := inspectRoot(ctx, r, id, func(e Event) error {
		if e.Kind == "campaign.launch-inputs-retained" {
			var v struct {
				Input string `json:"input_tree_digest"`
				Skill string `json:"skill_set_digest"`
				Files int    `json:"files"`
				Bytes int64  `json:"size_bytes"`
			}
			if decode(e.Metadata, &v, MaxMetadataBytes) != nil || v.Input != m.InputTreeDigest || v.Skill != m.SkillSetDigest || v.Files < 1 || v.Bytes < 0 {
				return ErrCorrupt
			}
			retained++
		}
		return nil
	}, false)
	if e := ctx.Err(); e != nil {
		return out, e
	}
	// Cleanup receipts live outside the original journal. Preserve the first intent
	// and atomically replace only the bounded latest result, never retained evidence.
	intent, _ := encode(map[string]string{"campaign_id": id, "run_manifest_digest": digest}, ManifestLimit)
	if old, e := readFile(r, "transient-cleanup-intent.json", ManifestLimit); e == nil {
		if string(old) != string(intent) {
			return out, ErrCorrupt
		}
	} else if !errors.Is(e, os.ErrNotExist) {
		return out, e
	} else {
		h := diskHooks()
		if e = publish(r, "transient-cleanup-intent.json", intent, false, &h); e != nil {
			return out, e
		}
	}
	defer func() {
		out.RecordedAt = time.Now().UTC().Format(time.RFC3339Nano)
		raw, e := encode(out, ManifestLimit)
		if e == nil {
			h := diskHooks()
			e = publish(r, "transient-cleanup-result.json", raw, true, &h)
		}
		err = errors.Join(err, e)
	}()
	remove := func(parent, name string) (string, error) {
		if e := ctx.Err(); e != nil {
			return "unconfirmed", e
		}
		if _, e := r.Lstat(parent); errors.Is(e, os.ErrNotExist) {
			return "absent", nil
		}
		if e := privateDir(r, parent); e != nil {
			return "unconfirmed", e
		}
		p, e := r.OpenRoot(parent)
		if e != nil {
			return "unconfirmed", e
		}
		defer p.Close()
		info, e := p.Lstat(name)
		if errors.Is(e, os.ErrNotExist) {
			return "absent", nil
		}
		if e != nil || !info.IsDir() {
			return "unconfirmed", ErrInvalid
		}
		base, e := p.Stat(".")
		if e != nil {
			return "unconfirmed", e
		}
		budget := 100000
		if e = removeTransientTree(ctx, p, name, uint64(base.Sys().(*syscall.Stat_t).Dev), 0, &budget); e != nil {
			return "unconfirmed", e
		}
		if e = syncDir(p, "."); e != nil {
			return "unconfirmed", e
		}
		return "removed", nil
	}
	out.Transport, err = remove("runtime", "transport")
	var e error
	out.Policies, e = remove("launch", "policies")
	err = errors.Join(err, e)
	if inspectErr == nil && report.JournalIntact && retained == 1 {
		out.Inputs = "unconfirmed"
		if e = privateDir(r, "launch"); e != nil {
			return out, errors.Join(err, e)
		}
		f, e := r.Open("launch")
		if e != nil {
			return out, errors.Join(err, e)
		}
		names, e := f.Readdirnames(4097)
		f.Close()
		if e != nil && e != io.EOF || len(names) > 4096 {
			return out, errors.Join(err, ErrQuota)
		}
		out.Inputs = "absent"
		for _, name := range names {
			if !stageName.MatchString(name) {
				continue
			}
			state, e := remove("launch", name)
			if e != nil {
				out.Inputs = "unconfirmed"
				return out, errors.Join(err, e)
			}
			if state == "removed" {
				out.Inputs = state
			}
		}
	}
	return out, errors.Join(err, ctx.Err())
}

// Streaming traversal never reads file bytes, follows links, or crosses mounts.
// The dead guest may have left nested directories, FIFOs and arbitrary filenames.
func removeTransientTree(ctx context.Context, parent *os.Root, name string, device uint64, depth int, budget *int) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	*budget--
	if *budget < 0 || depth > 128 {
		return ErrQuota
	}
	i, err := parent.Lstat(name)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	st, ok := i.Sys().(*syscall.Stat_t)
	if !ok || uint64(st.Dev) != device {
		return ErrInvalid
	}
	if !i.IsDir() {
		return parent.Remove(name)
	}
	r, err := parent.OpenRoot(name)
	if err != nil {
		return err
	}
	defer r.Close()
	actual, err := r.Stat(".")
	if err != nil || !os.SameFile(i, actual) {
		return ErrInvalid
	}
	if err = r.Chmod(".", 0700); err != nil {
		return err
	}
	f, err := r.Open(".")
	if err != nil {
		return err
	}
	defer f.Close()
	for {
		names, e := f.Readdirnames(128)
		for _, child := range names {
			if err = removeTransientTree(ctx, r, child, device, depth+1, budget); err != nil {
				return err
			}
		}
		if e == io.EOF {
			break
		}
		if e != nil {
			return e
		}
	}
	actual, err = parent.Lstat(name)
	if err != nil || !os.SameFile(i, actual) {
		return ErrInvalid
	}
	return parent.Remove(name)
}
