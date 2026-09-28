//go:build linux || darwin

package campaign

import (
	"errors"
	"os"

	"github.com/intrusiveai/operator_sandbox/contracts"
	"github.com/intrusiveai/operator_sandbox/internal/interceptor"
)

// AttachmentIntent precedes the first native mutation, independently of launch
// preparation. It freezes permission even when the configuration later changes.
type AttachmentIntent struct {
	CampaignID        string `json:"campaign_id"`
	LaunchID          string `json:"launch_id"`
	StartRequestID    string `json:"start_request_id"`
	WorkerInstanceID  string `json:"worker_instance_id"`
	InputsFingerprint string `json:"inputs_fingerprint"`
	AllowTargetStop   bool   `json:"allow_target_stop"`
	MinimumFreeBytes  int64  `json:"minimum_free_bytes"`
}

func (i AttachmentIntent) valid() bool {
	return validID(i.CampaignID) && validID(i.LaunchID) && validID(i.WorkerInstanceID) && ValidStopRequest(i.StartRequestID, "start") && digestPattern.MatchString(i.InputsFingerprint) && i.MinimumFreeBytes >= 0 && i.MinimumFreeBytes <= contracts.MaxSafeInteger
}

// AttachmentBinding is a bounded routing projection, not capability content or
// authority to start execution. The original worker is attribution only.
type AttachmentBinding struct {
	Binding interceptor.Binding `json:"binding"`
	Session interceptor.Session `json:"session"`
}
type attachmentBindingRecord struct {
	IntentDigest string            `json:"intent_digest"`
	Binding      AttachmentBinding `json:"binding"`
}
type attachmentInstanceRecord struct {
	BindingDigest string `json:"binding_digest"`
	InstanceID    string `json:"instance_id"`
}
type AttachmentSnapshot struct {
	Intent       AttachmentIntent
	Digest       string
	IntentDigest string
	Binding      *AttachmentBinding
	InstanceID   string
}
type AttachmentWriter struct {
	root          *os.Root
	lock          *os.File
	snapshot      AttachmentSnapshot
	failed        bool
	bindingDigest string
}

func (w *AttachmentWriter) Close() error { return errors.Join(w.lock.Close(), w.root.Close()) }

func CreateAttachment(stateRoot string, i AttachmentIntent) (*AttachmentWriter, error) {
	if !i.valid() {
		return nil, ErrInvalid
	}
	r, e := os.OpenRoot(stateRoot)
	if e != nil {
		return nil, e
	}
	defer r.Close()
	if e = privateDir(r, "."); e != nil {
		return nil, e
	}
	if e = mkdir(r, "attachments"); e != nil {
		return nil, e
	}
	name := "attachments/" + i.CampaignID
	if e = r.Mkdir(name, 0700); e != nil {
		return nil, e
	}
	if e = syncDir(r, "attachments"); e != nil {
		return nil, e
	}
	group, e := r.OpenRoot(name)
	if e != nil {
		return nil, e
	}
	l, e := lock(group, true)
	if e != nil {
		group.Close()
		return nil, e
	}
	w := &AttachmentWriter{root: group, lock: l}
	raw, e := encode(i, ManifestLimit)
	if e == nil {
		available, err := filesystemAvailable(group)
		e = err
		if e == nil && available-i.MinimumFreeBytes < nativeRecoveryBudget+3*ManifestLimit {
			e = ErrQuota
		}
	}
	if e == nil {
		h := diskHooks()
		e = publish(group, "intent.json", raw, false, &h)
	}
	if e != nil {
		w.Close()
		return nil, e
	}
	w.snapshot = AttachmentSnapshot{Intent: i, IntentDigest: contracts.RawDigest(raw)}
	return w, nil
}
func validAttachmentBinding(i AttachmentIntent, b AttachmentBinding) bool {
	s := b.Session
	return b.Binding.WorkerInstanceID == i.WorkerInstanceID && validID(b.Binding.SessionID) && b.Binding.RunRevision == 1 && s.ID == b.Binding.SessionID && s.CampaignID == i.CampaignID && s.OperationAPIVersion == interceptor.OperationVersion && s.Phase == "running" && s.Revision > 0 && s.Revision <= contracts.MaxSafeInteger && digestPattern.MatchString(s.EnvironmentDigest) && digestPattern.MatchString(s.AppDigest) && digestPattern.MatchString(s.CapabilityManifestDigest) && (s.FeedbackProfile == "black-box" || s.FeedbackProfile == "diagnostic" || s.FeedbackProfile == "oracle-assisted")
}
func (w *AttachmentWriter) save(name string, value any) error {
	if w.failed {
		return ErrClosed
	}
	raw, e := encode(value, ManifestLimit)
	if e == nil {
		h := diskHooks()
		e = publish(w.root, name, raw, false, &h)
	}
	if e != nil {
		w.failed = true
	}
	return e
}
func (w *AttachmentWriter) Bind(a interceptor.Attachment) error {
	b := AttachmentBinding{a.Binding, a.Session}
	if w.snapshot.Binding != nil || a.CampaignID != w.snapshot.Intent.CampaignID || !validAttachmentBinding(w.snapshot.Intent, b) {
		w.failed = true
		return ErrInvalid
	}
	record := attachmentBindingRecord{w.snapshot.IntentDigest, b}
	if e := w.save("binding.json", record); e != nil {
		return e
	}
	raw, _ := encode(record, ManifestLimit)
	w.bindingDigest = contracts.RawDigest(raw)
	w.snapshot.Binding = &b
	return nil
}
func (w *AttachmentWriter) Verify(s interceptor.Status) error {
	b := w.snapshot.Binding
	if b == nil || w.snapshot.InstanceID != "" || !validID(s.InstanceID) || s.CampaignID != w.snapshot.Intent.CampaignID || !s.Matches(s.InstanceID, b.Binding) {
		w.failed = true
		return ErrInvalid
	}
	if e := w.save("instance.json", attachmentInstanceRecord{w.bindingDigest, s.InstanceID}); e != nil {
		return e
	}
	w.snapshot.InstanceID = s.InstanceID
	if !s.Ready() {
		w.failed = true
		return ErrInvalid
	}
	return nil
}

func AttachmentIDs(stateRoot string) ([]string, error) { return groupIDs(stateRoot, "attachments") }

// OpenAttachmentRecovery takes the attachment writer lock. The caller also owns
// the installation lease and must defer to any prepared campaign journal.
func OpenAttachmentRecovery(stateRoot, id string) (*NativeRecovery, AttachmentSnapshot, error) {
	var s AttachmentSnapshot
	if !validID(id) {
		return nil, s, ErrInvalid
	}
	r, e := os.OpenRoot(stateRoot)
	if e != nil {
		return nil, s, e
	}
	defer r.Close()
	for _, name := range []string{".", "attachments", "attachments/" + id} {
		if e = privateDir(r, name); e != nil {
			return nil, s, e
		}
	}
	g, e := r.OpenRoot("attachments/" + id)
	if e != nil {
		return nil, s, e
	}
	l, e := lock(g, false)
	if e != nil {
		g.Close()
		return nil, s, e
	}
	a := &NativeRecovery{root: g, lock: l, manifest: RunManifest{CampaignID: id}, available: filesystemAvailable}
	fail := func(err error) (*NativeRecovery, AttachmentSnapshot, error) {
		a.Close()
		return nil, AttachmentSnapshot{}, err
	}
	raw, e := readFile(g, "intent.json", ManifestLimit)
	if e != nil {
		return fail(e)
	}
	if decode(raw, &s.Intent, ManifestLimit) != nil || !s.Intent.valid() || s.Intent.CampaignID != id {
		return fail(ErrCorrupt)
	}
	s.IntentDigest = contracts.RawDigest(raw)
	a.minimumFreeBytes = s.Intent.MinimumFreeBytes
	var bindingDigest string
	raw, e = readFile(g, "binding.json", ManifestLimit)
	if e == nil {
		var record attachmentBindingRecord
		if decode(raw, &record, ManifestLimit) != nil || record.IntentDigest != s.IntentDigest {
			return fail(ErrCorrupt)
		}
		b := record.Binding
		if !validAttachmentBinding(s.Intent, b) {
			return fail(ErrCorrupt)
		}
		s.Binding = &b
		bindingDigest = contracts.RawDigest(raw)
	} else if !errors.Is(e, os.ErrNotExist) {
		return fail(e)
	}
	raw, e = readFile(g, "instance.json", ManifestLimit)
	if e == nil {
		var v attachmentInstanceRecord
		if decode(raw, &v, ManifestLimit) != nil || !validID(v.InstanceID) || s.Binding == nil || v.BindingDigest != bindingDigest {
			return fail(ErrCorrupt)
		}
		s.InstanceID = v.InstanceID
	} else if !errors.Is(e, os.ErrNotExist) {
		return fail(e)
	}
	// A publication that did not finish is uncertainty, not permission to dispatch.
	for _, name := range []string{"intent.json.pending", "binding.json.pending", "instance.json.pending"} {
		if _, e = g.Lstat(name); e == nil {
			return fail(ErrCorrupt)
		} else if !errors.Is(e, os.ErrNotExist) {
			return fail(e)
		}
	}
	identity, e := encode(map[string]string{"intent_digest": s.IntentDigest, "binding_digest": bindingDigest, "instance_id": s.InstanceID}, ManifestLimit)
	if e != nil {
		return fail(e)
	}
	s.Digest = contracts.RawDigest(identity)
	a.attachmentDigest = s.Digest
	return a, s, nil
}
