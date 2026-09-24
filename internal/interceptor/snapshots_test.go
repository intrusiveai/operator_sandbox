package interceptor

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

func checkpointFixture(t *testing.T, legacy bool) ([]byte, Checkpoint) {
	t.Helper()
	name := "checkpoint-v1.json"
	if legacy {
		name = "checkpoint-legacy-v1.json"
	}
	raw, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	cp, err := decodeCheckpoint(raw, "campaign-1")
	if err != nil {
		t.Fatal(err)
	}
	return bytes.TrimSpace(raw), cp
}

// Synthetic corrupt/scope/page cases use this helper; native golden hashes
// above are generated independently by Interceptor's own implementation.
func rehashCheckpoint(t *testing.T, cp Checkpoint) Checkpoint {
	t.Helper()
	cp.Hash = ""
	cp.Hash = rawDigest(encoded(t, cp))
	return cp
}

func TestNativeCheckpointGolden(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		raw, cp := checkpointFixture(t, legacy)
		if !bytes.Equal(encoded(t, cp), raw) || cp.JournalSeq != 9007199254740993 {
			t.Fatal("native serialization or uint64 precision changed")
		}
		if legacy && (cp.CampaignID != "" || cp.Description != "") {
			t.Fatal("legacy metadata rewritten")
		}
		if !legacy && (cp.Description != "Before mutation — retain context" || cp.CampaignID != "campaign-1") {
			t.Fatal("metadata changed")
		}
		var fields map[string]json.RawMessage
		_ = json.Unmarshal(raw, &fields)
		fields["hash"] = []byte(`""`)
		if rawDigest(encoded(t, fields)) == cp.Hash {
			t.Fatal("fixture should distinguish struct order from sorted-map JSON")
		}
	}
}

func TestSnapshotCreateAllowanceAndReceipt(t *testing.T) {
	_, cp := checkpointFixture(t, false)
	q := query()
	q.Operation = ""
	input := SnapshotCreate{Label: cp.Label, Description: cp.Description, MaximumCommittedBytes: 123}
	p, err := PrepareSnapshotCreate(q, input)
	if err != nil {
		t.Fatal(err)
	}
	input.MaximumCommittedBytes = 1000000
	calls := 0
	c := &Client{http: &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		calls++
		if r.URL.Path != "/v1/operations" {
			t.Fatal("wrong route")
		}
		raw, _ := io.ReadAll(r.Body)
		if !bytes.Equal(raw, p.Bytes()) {
			t.Fatal("prepared envelope changed")
		}
		fields, _ := object(raw)
		body, _ := object(fields["body"])
		if string(body["maximum_committed_bytes"]) != "123" || len(body) != 3 {
			t.Fatal("allowance omitted or mutated", string(raw))
		}
		return wireResponse(t, 201, cp), nil
	})}}
	r, err := c.Execute(context.Background(), p)
	if err != nil {
		t.Fatal(err)
	}
	target := Session{ID: cp.SourceSessionID, CampaignID: cp.CampaignID, EnvironmentDigest: cp.EnvironmentDigest, AppDigest: cp.AppDigest}
	got, err := DecodeSnapshotCreated(r, p, target)
	if err != nil || got != cp || calls != 1 {
		t.Fatal(got, err, calls)
	}
	for _, amount := range []int64{0, math.MaxInt64} {
		zero, err := PrepareSnapshotCreate(q, SnapshotCreate{MaximumCommittedBytes: amount})
		if err != nil {
			t.Fatal(err)
		}
		fields, _ := object(zero.Bytes())
		var body map[string]json.RawMessage
		_ = json.Unmarshal(fields["body"], &body)
		if !bytes.Equal(body["maximum_committed_bytes"], encoded(t, amount)) {
			t.Fatal("explicit allowance lost", string(fields["body"]))
		}
	}
	if _, err := DecodeSnapshotCreated(Response{Status: 200, Body: encoded(t, cp)}, p, target); err == nil {
		t.Fatal("accepted wrong native success status")
	}
	_, err = DecodeSnapshotCreated(Response{Status: 429, Body: []byte(`{"code":"snapshot_bytes_exhausted"}`)}, p, target)
	var remote *RemoteError
	if !errors.As(err, &remote) || remote.Response.Code() != "snapshot_bytes_exhausted" {
		t.Fatal("lost native quota outcome", err)
	}
}

func TestSnapshotCreateGuards(t *testing.T) {
	q := query()
	q.Operation = ""
	for _, input := range []SnapshotCreate{{MaximumCommittedBytes: -1}, {Label: strings.Repeat("x", 257)}, {Label: "\xff"}, {Description: strings.Repeat("é", 2049)}, {Description: "\xff"}} {
		if _, err := PrepareSnapshotCreate(q, input); !errors.Is(err, ErrRequest) {
			t.Fatal("accepted invalid input", err)
		}
	}
	if _, err := PrepareSnapshotCreate(q, SnapshotCreate{Label: strings.Repeat("é", 256), Description: strings.Repeat("é", 2048), MaximumCommittedBytes: 1}); err != nil {
		t.Fatal("rejected boundary", err)
	}
	q.Operation = "snapshot.restore"
	if _, err := PrepareSnapshotCreate(q, SnapshotCreate{}); !errors.Is(err, ErrRequest) {
		t.Fatal(err)
	}
	q.Operation = "snapshot.create"
	p, err := PrepareOperation(q, []byte(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	target := Session{ID: q.SessionID, CampaignID: q.CampaignID}
	if _, err := DecodeSnapshotCreated(Response{Status: 201}, p, target); !errors.Is(err, ErrRequest) {
		t.Fatal("missing saved allowance accepted", err)
	}
}

func TestSnapshotCreationRejectsMismatchedReceipt(t *testing.T) {
	_, cp := checkpointFixture(t, false)
	q := query()
	q.Operation = "snapshot.create"
	p, err := PrepareSnapshotCreate(q, SnapshotCreate{cp.Label, cp.Description, cp.CanonicalSizeBytes})
	if err != nil {
		t.Fatal(err)
	}
	target := Session{ID: cp.SourceSessionID, CampaignID: cp.CampaignID, EnvironmentDigest: cp.EnvironmentDigest, AppDigest: cp.AppDigest}
	for name, change := range map[string]func(*Checkpoint){
		"campaign":       func(c *Checkpoint) { c.CampaignID = "campaign-other" },
		"legacy":         func(c *Checkpoint) { c.CampaignID = "" },
		"source":         func(c *Checkpoint) { c.SourceSessionID = "sess-old" },
		"label":          func(c *Checkpoint) { c.Label = "different" },
		"description":    func(c *Checkpoint) { c.Description = "different" },
		"environment":    func(c *Checkpoint) { c.EnvironmentDigest = rawDigest([]byte("other")) },
		"application":    func(c *Checkpoint) { c.AppDigest = rawDigest([]byte("other")) },
		"over allowance": func(c *Checkpoint) { c.CanonicalSizeBytes++ },
	} {
		t.Run(name, func(t *testing.T) {
			changed := cp
			change(&changed)
			changed = rehashCheckpoint(t, changed)
			_, err := DecodeSnapshotCreated(Response{Status: 201, Body: encoded(t, changed)}, p, target)
			assertUncertain(t, err)
		})
	}
}

func TestSnapshotIntegrityAndSchemaGuards(t *testing.T) {
	raw, cp := checkpointFixture(t, false)
	for _, field := range []string{"description", "label", "campaign_id", "canonical_size_bytes", "hash"} {
		var fields map[string]json.RawMessage
		_ = json.Unmarshal(raw, &fields)
		fields[field] = []byte(`"tampered"`)
		if field == "canonical_size_bytes" {
			fields[field] = []byte(`124`)
		}
		_, err := decodeCheckpoint(encoded(t, fields), "campaign-1")
		assertUncertain(t, err)
	}
	for name, change := range map[string]func(*Checkpoint){
		"version":           func(c *Checkpoint) { c.SchemaVersion = 2 },
		"journal version":   func(c *Checkpoint) { c.JournalSchemaVersion = 2 },
		"status":            func(c *Checkpoint) { c.Status = "unavailable" },
		"path id":           func(c *Checkpoint) { c.ID = "../checkpoint" },
		"parent":            func(c *Checkpoint) { c.ParentCheckpoint = "../checkpoint" },
		"empty source":      func(c *Checkpoint) { c.SourceSessionID = "" },
		"negative size":     func(c *Checkpoint) { c.CanonicalSizeBytes = -1 },
		"empty journal":     func(c *Checkpoint) { c.JournalSeq = 0 },
		"time":              func(c *Checkpoint) { c.CreatedAt = time.Time{} },
		"version missing":   func(c *Checkpoint) { c.InterceptorVersion = "" },
		"bad digest":        func(c *Checkpoint) { c.FileManifestDigest = "bad" },
		"description bytes": func(c *Checkpoint) { c.Description = strings.Repeat("é", 2049) },
	} {
		t.Run(name, func(t *testing.T) {
			changed := cp
			change(&changed)
			changed = rehashCheckpoint(t, changed)
			_, err := decodeCheckpoint(encoded(t, changed), "campaign-1")
			assertUncertain(t, err)
		})
	}
	for _, edit := range []func(map[string]json.RawMessage){
		func(m map[string]json.RawMessage) { delete(m, "event_seq") },
		func(m map[string]json.RawMessage) { m["canonical_size_bytes"] = []byte(`null`) },
		func(m map[string]json.RawMessage) { m["ID"] = encoded(t, cp.ID) },
		func(m map[string]json.RawMessage) { m["extra"] = []byte(`true`) },
	} {
		var fields map[string]json.RawMessage
		_ = json.Unmarshal(raw, &fields)
		edit(fields)
		_, err := decodeCheckpoint(encoded(t, fields), "campaign-1")
		assertUncertain(t, err)
	}
}

func TestSnapshotListPaginationAndRetainedSessions(t *testing.T) {
	_, cp := checkpointFixture(t, false)
	other := cp
	other.SourceSessionID = "sess-older"
	other = rehashCheckpoint(t, other)
	for _, scenario := range []struct {
		offset, limit, total int
		items                []Checkpoint
		next                 *int
	}{
		{0, 0, 2, []Checkpoint{cp, other}, nil},
		{0, 1, 2, []Checkpoint{cp}, new(1)},
		{1, 1, 2, []Checkpoint{other}, nil},
		{100, 10, 2, []Checkpoint{}, nil},
		{0, 0, 0, []Checkpoint{}, nil},
	} {
		calls := 0
		body := map[string]any{"campaign_id": "campaign-1", "checkpoints": scenario.items, "total": scenario.total}
		if scenario.next != nil {
			body["next_offset"] = *scenario.next
		}
		c := &Client{http: &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
			calls++
			if r.URL.Path != "/v1/snapshots/list" {
				t.Fatal("wrong route")
			}
			raw, _ := io.ReadAll(r.Body)
			var request SnapshotListRequest
			if json.Unmarshal(raw, &request) != nil || request.APIVersion != LifecycleVersion || request.CampaignID != "campaign-1" || request.Offset != scenario.offset || request.Limit != scenario.limit && !(scenario.limit == 0 && request.Limit == 100) {
				t.Fatal("wrong pagination request", string(raw))
			}
			return wireResponse(t, 200, body), nil
		})}}
		got, err := c.ListSnapshots(context.Background(), SnapshotListRequest{CampaignID: "campaign-1", Offset: scenario.offset, Limit: scenario.limit})
		if err != nil || got.Checkpoints == nil || len(got.Checkpoints) != len(scenario.items) || got.Offset != scenario.offset || got.Total != scenario.total || calls != 1 {
			t.Fatal(got, err, calls)
		}
	}
}

func TestSnapshotListRejectsInvalidPages(t *testing.T) {
	_, cp := checkpointFixture(t, false)
	for name, change := range map[string]func(map[string]any){
		"wrong campaign":  func(m map[string]any) { m["campaign_id"] = "different" },
		"null records":    func(m map[string]any) { m["checkpoints"] = nil },
		"missing records": func(m map[string]any) { delete(m, "checkpoints") },
		"too large total": func(m map[string]any) { m["total"] = MaxSnapshotInventory + 1 },
		"negative total":  func(m map[string]any) { m["total"] = -1 },
		"truncated page":  func(m map[string]any) { m["total"] = 2 },
		"null next":       func(m map[string]any) { m["next_offset"] = nil },
		"spurious next":   func(m map[string]any) { m["next_offset"] = 1 },
		"duplicate":       func(m map[string]any) { m["checkpoints"] = []Checkpoint{cp, cp}; m["total"] = 2 },
		"wrong sort": func(m map[string]any) {
			earlier := cp
			earlier.CreatedAt = cp.CreatedAt.Add(-time.Minute)
			earlier.ID = "cp-1790251200000-1123456789abcdef01234567"
			earlier = rehashCheckpoint(t, earlier)
			m["checkpoints"] = []Checkpoint{cp, earlier}
			m["total"] = 2
		},
	} {
		t.Run(name, func(t *testing.T) {
			body := map[string]any{"campaign_id": "campaign-1", "checkpoints": []Checkpoint{cp}, "total": 1}
			change(body)
			_, err := fakeClient(t, "/v1/snapshots/list", body).ListSnapshots(context.Background(), SnapshotListRequest{CampaignID: "campaign-1"})
			assertUncertain(t, err)
		})
	}
	for _, next := range []any{nil, 0, 2} {
		body := map[string]any{"campaign_id": "campaign-1", "checkpoints": []Checkpoint{cp}, "total": 2, "next_offset": next}
		_, err := fakeClient(t, "/v1/snapshots/list", body).ListSnapshots(context.Background(), SnapshotListRequest{CampaignID: "campaign-1", Limit: 1})
		assertUncertain(t, err)
	}
	body := map[string]any{"campaign_id": "campaign-1", "checkpoints": []Checkpoint{cp}, "total": 1}
	_, err := fakeClient(t, "/v1/snapshots/list", body).ListSnapshots(context.Background(), SnapshotListRequest{CampaignID: "campaign-1", SourceSessionID: "sess-other"})
	assertUncertain(t, err)
}

func TestSnapshotReadScopingAndErrors(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		_, cp := checkpointFixture(t, legacy)
		c := &Client{http: &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
			if r.URL.Path != "/v1/snapshots/inspect" {
				t.Fatal("wrong route")
			}
			raw, _ := io.ReadAll(r.Body)
			var body map[string]string
			if json.Unmarshal(raw, &body) != nil || len(body) != 4 || body["api_version"] != LifecycleVersion || body["campaign_id"] != "campaign-1" || body["source_session_id"] != cp.SourceSessionID || body["checkpoint_id"] != cp.ID {
				t.Fatal("wrong scoped request", string(raw))
			}
			return wireResponse(t, 200, cp), nil
		})}}
		got, err := c.InspectSnapshot(context.Background(), "campaign-1", cp.SourceSessionID, cp.ID)
		if err != nil || got != cp {
			t.Fatal(got, err)
		}
		_, err = fakeClient(t, "/v1/snapshots/inspect", cp).InspectSnapshot(context.Background(), "campaign-1", "other", cp.ID)
		assertUncertain(t, err)
	}
	_, cp := checkpointFixture(t, false)
	for _, status := range []int{403, 404, 413, 503} {
		c := &Client{http: &http.Client{Transport: roundTrip(func(*http.Request) (*http.Response, error) {
			return wireResponse(t, status, map[string]string{"code": "snapshot_read_failed"}), nil
		})}}
		_, err := c.InspectSnapshot(context.Background(), "campaign-1", cp.SourceSessionID, cp.ID)
		var remote *RemoteError
		if !errors.As(err, &remote) || remote.Response.Status != status {
			t.Fatal(err)
		}
	}
	c := &Client{http: &http.Client{Transport: roundTrip(func(*http.Request) (*http.Response, error) { t.Fatal("invalid request dispatched"); return nil, nil })}}
	for _, q := range []SnapshotListRequest{{CampaignID: "../bad"}, {CampaignID: "campaign-1", SourceSessionID: "../bad"}, {CampaignID: "campaign-1", Offset: -1}, {CampaignID: "campaign-1", Offset: 10001}, {CampaignID: "campaign-1", Limit: 1001}, {CampaignID: "campaign-1", Limit: -1}, {CampaignID: "campaign-1", APIVersion: "wrong"}} {
		if _, err := c.ListSnapshots(context.Background(), q); !errors.Is(err, ErrRequest) {
			t.Fatal(err)
		}
	}
	if _, err := c.InspectSnapshot(context.Background(), "campaign-1", "sess-1", "../bad"); !errors.Is(err, ErrRequest) {
		t.Fatal(err)
	}
}

func TestSnapshotResponseBound(t *testing.T) {
	for _, length := range []int64{-1, MaxSnapshotResponse + 1} {
		c := &Client{http: &http.Client{Transport: roundTrip(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, ContentLength: length, Body: io.NopCloser(strings.NewReader(strings.Repeat(" ", MaxSnapshotResponse+1)))}, nil
		})}}
		_, err := c.ListSnapshots(context.Background(), SnapshotListRequest{CampaignID: "campaign-1"})
		assertUncertain(t, err)
	}
}
