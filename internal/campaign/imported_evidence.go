//go:build linux || darwin

package campaign

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/intrusiveai/operator_sandbox/contracts"
	"github.com/intrusiveai/operator_sandbox/internal/interceptor"
)

// ImportedEvidence is a separate adoption record; it never changes the execution
// journal or the policy that governed online collection.
type ImportedEvidence struct {
	APIVersion     string         `json:"api_version"`
	ManifestDigest string         `json:"run_manifest_digest"`
	Archive        NativeEvidence `json:"archive"`
}

type importEnvelope struct {
	Record ImportedEvidence `json:"record"`
	Digest string           `json:"digest"`
}

func (r *NativeRecovery) ImportDirectory(maximum int64) (string, error) {
	if maximum <= 0 || maximum > contracts.MaxSafeInteger {
		return "", ErrInvalid
	}
	if e := r.evidenceSpace(2*maximum + 2*MaxContentBytes); e != nil {
		return "", e
	}
	if e := mkdir(r.root, "native-evidence"); e != nil {
		return "", e
	}
	return filepath.Join(r.root.Name(), "native-evidence"), nil
}

func (r *NativeRecovery) AdoptImport(ctx context.Context, v *interceptor.VerifiedEvidence) (ImportedEvidence, error) {
	if v == nil || v.Receipt().Identity.CampaignID != r.manifest.CampaignID {
		return ImportedEvidence{}, ErrInvalid
	}
	p := v.Receipt()
	records, e := r.ImportedEvidence()
	if e != nil {
		return ImportedEvidence{}, e
	}
	for _, old := range records {
		if old.Archive.Provenance.Transfer.SHA256 == p.Transfer.SHA256 {
			if old.Archive.Provenance.Identity != p.Identity {
				return ImportedEvidence{}, ErrCorrupt
			}
			return old, r.VerifyRetainedEvidence(ctx, old.Archive)
		}
	}
	if len(records) >= 1000 {
		return ImportedEvidence{}, ErrQuota
	}
	record := NativeEvidence{Path: "native-evidence/" + p.Identity.SessionID + "/" + p.Transfer.SHA256[7:] + ".tar", Provenance: p}
	if _, e = r.root.Lstat(record.Path); e == nil {
		e = r.VerifyRetainedEvidence(ctx, record)
	} else if errors.Is(e, os.ErrNotExist) {
		record, e = retainEvidence(ctx, r.root, r.manifest.CampaignID, v, r.minimumFreeBytes+2*MaxContentBytes, diskHooks())
	}
	if e != nil {
		return ImportedEvidence{}, e
	}
	out := ImportedEvidence{"operator.dev/evidence-import/v1alpha1", r.digest, record}
	raw, e := encode(out, MaxContentBytes)
	if e != nil {
		return ImportedEvidence{}, e
	}
	raw, e = encode(importEnvelope{out, contracts.RawDigest(raw)}, MaxContentBytes)
	if e != nil {
		return ImportedEvidence{}, e
	}
	if e = mkdir(r.root, "evidence-imports"); e != nil {
		return ImportedEvidence{}, e
	}
	h := diskHooks()
	e = publish(r.root, "evidence-imports/"+p.Transfer.SHA256[7:]+".json", raw, false, &h)
	return out, e
}

// ImportedEvidence validates record integrity and binding, not the tar bytes.
// Consumers must match recorded session lineage and rehash the selected archive.
func (r *NativeRecovery) ImportedEvidence() ([]ImportedEvidence, error) {
	out := []ImportedEvidence{}
	if e := privateDir(r.root, "evidence-imports"); e != nil {
		if errors.Is(e, os.ErrNotExist) {
			return out, nil
		}
		return nil, e
	}
	names, e := directoryNames(r.root, "evidence-imports")
	if e != nil {
		return nil, e
	}
	if len(names) > 1000 {
		return nil, ErrQuota
	}
	for _, name := range names {
		if !strings.HasSuffix(name, ".json") || !validDigest("sha256:"+strings.TrimSuffix(name, ".json")) {
			return nil, ErrCorrupt
		}
		raw, e := readFile(r.root, "evidence-imports/"+name, MaxContentBytes)
		var envelope importEnvelope
		if e != nil || decode(raw, &envelope, MaxContentBytes) != nil {
			return nil, ErrCorrupt
		}
		v := envelope.Record
		canonical, e := encode(v, MaxContentBytes)
		if e != nil || contracts.RawDigest(canonical) != envelope.Digest || v.APIVersion != "operator.dev/evidence-import/v1alpha1" || v.ManifestDigest != r.digest || v.Archive.Provenance.Transfer.SHA256 != "sha256:"+strings.TrimSuffix(name, ".json") || v.Archive.Provenance.Identity.CampaignID != r.manifest.CampaignID {
			return nil, ErrCorrupt
		}
		out = append(out, v)
	}
	return out, nil
}
