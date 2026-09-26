//go:build linux || darwin

package campaign

import (
	"errors"
	"os"
	"regexp"
	"syscall"
	"time"

	"github.com/intrusiveai/operator_sandbox/contracts"
)

const TerminationVersion = "operator.dev/termination/v1alpha1"
const TerminationRecordLimit = 8 << 10
const TerminationSegmentLimit = 1 << 20
const MaxTerminationResults = 128

var requestIDPattern = regexp.MustCompile(`^[0-9a-f]{32}$`)

type TerminationOutcome struct {
	Confirmed     bool   `json:"confirmed"`
	KillAttempted bool   `json:"kill_attempted"`
	State         string `json:"state"`
	Code          string `json:"code"`
}

// StopRecord is an emergency record independent of the campaign journal/head and
// writer lock. The first intent is immutable; results preserve bounded observations.
// Neither record grants permission to restart, dispatch, remove or purge anything.
type StopRecord struct {
	APIVersion        string              `json:"api_version"`
	RequestID         string              `json:"request_id"`
	CampaignID        string              `json:"campaign_id"`
	LaunchID          string              `json:"launch_id"`
	RunManifestDigest string              `json:"run_manifest_digest"`
	DockerContainerID string              `json:"docker_container_id"`
	RecordedAt        string              `json:"recorded_at"`
	Reason            string              `json:"reason"`
	Outcome           *TerminationOutcome `json:"outcome"`
}

type stopEnvelope struct {
	Record StopRecord `json:"record"`
	Digest string     `json:"digest"`
}

type terminationSegment struct {
	Records []stopEnvelope `json:"records"`
	Digest  string         `json:"digest"`
}

func encodeResults(records []stopEnvelope) ([]byte, error) {
	raw, err := encode(records, TerminationSegmentLimit)
	if err != nil {
		return nil, err
	}
	return encode(terminationSegment{records, contracts.RawDigest(raw)}, TerminationSegmentLimit)
}

func decodeStop(raw []byte, b DockerBinding, result bool) (StopRecord, error) {
	var envelope stopEnvelope
	if decode(raw, &envelope, TerminationRecordLimit) != nil || envelope.Record.validate(b, result) != nil {
		return StopRecord{}, ErrCorrupt
	}
	canonical, err := encode(envelope.Record, TerminationRecordLimit)
	if err != nil || contracts.RawDigest(canonical) != envelope.Digest {
		return StopRecord{}, ErrCorrupt
	}
	return envelope.Record, nil
}

func decodeResults(raw []byte, b DockerBinding) ([]stopEnvelope, error) {
	var segment terminationSegment
	if decode(raw, &segment, TerminationSegmentLimit) != nil || len(segment.Records) == 0 || len(segment.Records) > MaxTerminationResults {
		return nil, ErrCorrupt
	}
	records := segment.Records
	canonical, err := encode(records, TerminationSegmentLimit)
	if err != nil || contracts.RawDigest(canonical) != segment.Digest {
		return nil, ErrCorrupt
	}
	reasons := map[string]string{}
	for _, e := range records {
		one, err := encode(e, TerminationRecordLimit)
		if err != nil {
			return nil, ErrCorrupt
		}
		r, err := decodeStop(one, b, true)
		if err != nil {
			return nil, err
		}
		if prior, ok := reasons[r.RequestID]; ok && prior != r.Reason {
			return nil, ErrCorrupt
		}
		reasons[r.RequestID] = r.Reason
	}
	return records, nil
}

// The emergency segment is logically append-only. Atomic replacement publishes
// the complete bounded inventory, retaining prior outcomes even on later retries.
func saveResult(r *os.Root, b DockerBinding, record StopRecord) error {
	var records []stopEnvelope
	raw, err := readFile(r, "termination-results.json", TerminationSegmentLimit)
	if err == nil {
		records, err = decodeResults(raw, b)
		if err != nil {
			return err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if len(records) >= MaxTerminationResults {
		return ErrQuota
	}
	for _, e := range records {
		if e.Record.RequestID == record.RequestID && e.Record.Reason != record.Reason {
			return ErrInvalid
		}
	}
	one, err := encode(record, TerminationRecordLimit)
	if err != nil {
		return err
	}
	records = append(records, stopEnvelope{record, contracts.RawDigest(one)})
	raw, err = encodeResults(records)
	if err != nil {
		return err
	}
	hooks := diskHooks()
	return publish(r, "termination-results.json", raw, true, &hooks)
}

func ValidStopRequest(requestID, reason string) bool {
	return requestIDPattern.MatchString(requestID) && validID(reason)
}

func StopObservation(b DockerBinding, requestID, reason string, outcome *TerminationOutcome) StopRecord {
	return StopRecord{TerminationVersion, requestID, b.CampaignID, b.LaunchID, b.RunManifestDigest,
		b.DockerContainerID, time.Now().UTC().Format(time.RFC3339Nano), reason, outcome}
}

func (r StopRecord) validate(b DockerBinding, result bool) error {
	if b.Validate() != nil || r.APIVersion != TerminationVersion || !ValidStopRequest(r.RequestID, r.Reason) ||
		r.CampaignID != b.CampaignID || r.LaunchID != b.LaunchID || r.RunManifestDigest != b.RunManifestDigest ||
		r.DockerContainerID != b.DockerContainerID || !validTime(r.RecordedAt) || (r.Outcome != nil) != result {
		return ErrInvalid
	}
	if result {
		o := r.Outcome
		switch o.Code {
		case "confirmed_stopped", "invalid_binding", "docker_unavailable", "invalid_response", "identity_mismatch",
			"container_unconfirmed", "deadline_or_cancellation", "lifecycle_policy_mismatch":
		default:
			return ErrInvalid
		}
		if o.Confirmed != (o.Code == "confirmed_stopped") {
			return ErrInvalid
		}
		switch o.State {
		case "unknown", "created", "exited", "dead":
		default:
			return ErrInvalid
		}
		if o.Confirmed && o.State == "unknown" {
			return ErrInvalid
		}
	}
	return nil
}

// SaveStopRecord never takes the worker lock. Concurrent administrative writers
// try a separate lock once; contention is a recording failure, never a kill gate.
// It may block in the filesystem, so termination must run independently of it.
func SaveStopRecord(stateRoot string, b DockerBinding, record StopRecord) error {
	result := record.Outcome != nil
	if record.validate(b, result) != nil {
		return ErrInvalid
	}
	r, err := openCampaign(stateRoot, b.CampaignID)
	if err != nil {
		return err
	}
	defer r.Close()
	m, digest, err := loadManifest(r, b.CampaignID)
	if err != nil || b.validate(m, digest) != nil {
		return ErrCorrupt
	}
	f, err := r.OpenFile("termination.lock", os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
	if errors.Is(err, os.ErrExist) {
		f, err = openRegular(r, "termination.lock", os.O_RDWR)
	}
	if err != nil {
		return err
	}
	defer f.Close()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return ErrActive
	}
	if result {
		return saveResult(r, b, record)
	}
	name := "termination-intent.json"
	if _, err := r.Lstat(name + ".pending"); !errors.Is(err, os.ErrNotExist) {
		return ErrCorrupt
	}
	if raw, err := readFile(r, name, TerminationRecordLimit); err == nil {
		prior, err := decodeStop(raw, b, result)
		if err != nil {
			return ErrCorrupt
		}
		if prior.RequestID == record.RequestID && prior.Reason != record.Reason {
			return ErrInvalid
		}
		return nil // The first terminal intent permanently closes this launch.
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	raw, err := encode(record, TerminationRecordLimit)
	if err != nil {
		return err
	}
	raw, err = encode(stopEnvelope{record, contracts.RawDigest(raw)}, TerminationRecordLimit)
	if err != nil {
		return err
	}
	hooks := diskHooks()
	return publish(r, name, raw, false, &hooks)
}

// ReadStopIntent is a future launch/dispatch gate, independent of the worker lock.
// Only absent intent, results and interrupted publications return nil.
// Any error must close admission; result records cannot remove a stop intent.
func ReadStopIntent(stateRoot, campaignID string) (*StopRecord, error) {
	return readStopRecord(stateRoot, campaignID, false)
}

// ReadTerminationResult reads the latest emergency observation. It is reporting
// evidence, not current Docker state or a verified container-removal receipt.
func ReadTerminationResult(stateRoot, campaignID string) (*StopRecord, error) {
	return readStopRecord(stateRoot, campaignID, true)
}

func readStopRecord(stateRoot, campaignID string, result bool) (*StopRecord, error) {
	b, err := ReadDockerBinding(stateRoot, campaignID)
	if err != nil {
		return nil, err
	}
	r, err := openCampaign(stateRoot, campaignID)
	if err != nil {
		return nil, err
	}
	defer r.Close()
	name := "termination-intent.json"
	limit := TerminationRecordLimit
	if result {
		name = "termination-results.json"
		limit = TerminationSegmentLimit
	}
	if _, err := r.Lstat(name + ".pending"); !errors.Is(err, os.ErrNotExist) {
		return nil, ErrCorrupt
	}
	raw, err := readFile(r, name, limit)
	if errors.Is(err, os.ErrNotExist) {
		if !result {
			// An outcome can survive a failed intent publication. It still records
			// a terminal decision, even when the Docker outcome was unconfirmed.
			for _, name := range []string{"termination-results.json", "termination-results.json.pending"} {
				if _, err := r.Lstat(name); !errors.Is(err, os.ErrNotExist) {
					return nil, ErrClosed
				}
			}
		}
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if result {
		records, err := decodeResults(raw, b)
		if err != nil {
			return nil, err
		}
		return &records[len(records)-1].Record, nil
	}
	record, err := decodeStop(raw, b, result)
	if err != nil {
		return nil, ErrCorrupt
	}
	return &record, nil
}
