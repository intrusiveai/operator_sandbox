package feedback

import (
	"bytes"
	"encoding/json"
	"reflect"
	"slices"

	"github.com/intrusive-ai/operator-sandbox/contracts"
	"github.com/intrusive-ai/operator-sandbox/internal/interceptor"
)

type ReadResult struct {
	ReceiptID    string    `json:"receipt_id"`
	EntryID      string    `json:"entry_id"`
	Availability string    `json:"availability"`
	Reason       string    `json:"reason,omitempty"`
	Offset       int64     `json:"offset"`
	Content      []byte    `json:"content"`
	RawLength    int       `json:"raw_length"`
	EOF          bool      `json:"eof"`
	Artifact     *Artifact `json:"artifact,omitempty"`
	Truncated    bool      `json:"truncated"`
}

// Read requires current channel admission and explicit current permitted kinds.
// Original source bindings remain fixed across restore. No worker/revision access
// gate exists, and this method performs no native reads or target effects.
func (r *Receipt) Read(campaign, receipt, entry string, offset int64, maximum int, admitted bool, currentlyAllowed []string) ([]byte, error) {
	if !admitted || campaign != r.source.CampaignID || receipt != r.id || offset < 0 || maximum < 1 || maximum > MaxChunk {
		return nil, ErrRead
	}
	for _, e := range r.entries {
		if e.entry.ID != entry {
			continue
		}
		if !slices.Contains(currentlyAllowed, e.entry.Kind) {
			return nil, ErrRead
		}
		result := ReadResult{ReceiptID: receipt, EntryID: entry, Availability: e.entry.Availability, Reason: e.entry.Reason, Offset: offset, Content: []byte{}, Truncated: e.entry.Truncated}
		if e.entry.Availability == "available" {
			if offset > int64(len(e.content)) {
				return nil, ErrRead
			}
			end := min(int64(len(e.content)), offset+int64(maximum))
			result.Content = bytes.Clone(e.content[offset:end])
			result.RawLength = len(result.Content)
			result.EOF, result.Artifact = end == int64(len(e.content)), e.entry.Artifact
		}
		return json.Marshal(result)
	}
	return nil, ErrRead
}

// Assembly validates ordered bounded native chunks, including each repeated
// entry descriptor and final content digest. Bytes are inaccessible until the
// entire artifact is verified; malformed chunks permanently invalidate it.
type Assembly struct {
	receipt      string
	entry        interceptor.FeedbackEntry
	data         []byte
	done, failed bool
}

func NewAssembly(receipt string, e interceptor.FeedbackEntry) (*Assembly, error) {
	if e.Availability != "available" || e.Artifact == nil || e.Artifact.SizeBytes < 0 || e.Artifact.SizeBytes > MaxArtifact || !digestPattern.MatchString(e.Artifact.Digest) || !idPattern.MatchString(e.ID) || !idPattern.MatchString(receipt) {
		return nil, ErrFeedback
	}
	raw, _ := json.Marshal(e)
	var copy interceptor.FeedbackEntry
	_ = json.Unmarshal(raw, &copy)
	return &Assembly{receipt: receipt, entry: copy, data: []byte{}}, nil
}
func (a *Assembly) Next(maximum int) (interceptor.FeedbackReadRequest, error) {
	if a.failed || a.done || maximum < 1 || maximum > MaxChunk {
		return interceptor.FeedbackReadRequest{}, ErrRead
	}
	return interceptor.FeedbackReadRequest{ReceiptID: a.receipt, EntryID: a.entry.ID, Offset: int64(len(a.data)), MaxBytes: maximum}, nil
}
func (a *Assembly) Accept(q interceptor.FeedbackReadRequest, raw []byte) error {
	var c interceptor.FeedbackChunk
	valid := !a.failed && !a.done && q.ReceiptID == a.receipt && q.EntryID == a.entry.ID && q.Offset == int64(len(a.data)) && q.MaxBytes > 0 && q.MaxBytes <= MaxChunk
	if !valid || interceptor.DecodeTypedBody(raw, &c, (MaxChunk*4/3)+8192) != nil || c.ReceiptID != q.ReceiptID || !reflect.DeepEqual(c.Entry, a.entry) || c.Offset != q.Offset || c.RawLength != len(c.Content) || c.RawLength > q.MaxBytes || int64(len(a.data)+c.RawLength) > a.entry.Artifact.SizeBytes || c.EOF != (int64(len(a.data)+c.RawLength) == a.entry.Artifact.SizeBytes) || c.RawLength == 0 && !c.EOF {
		a.failed = true
		a.data = nil
		return ErrFeedback
	}
	a.data = append(a.data, c.Content...)
	if c.EOF {
		if contracts.RawDigest(a.data) != a.entry.Artifact.Digest {
			a.failed = true
			a.data = nil
			return ErrFeedback
		}
		a.done = true
	}
	return nil
}
func (a *Assembly) Bytes() ([]byte, error) {
	if a.failed || !a.done {
		return nil, ErrFeedback
	}
	return bytes.Clone(a.data), nil
}
