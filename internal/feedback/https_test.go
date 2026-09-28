package feedback

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/intrusiveai/operator_sandbox/internal/interceptor"
)

func TestHTTPSFeedbackPolicyAndAvailability(t *testing.T) {
	for _, tc := range []struct {
		name, profile       string
		selection           *interceptor.ObservationSelection
		complete, truncated bool
		maximum             int64
		entries             int
		availability        string
	}{
		{"black-box", "black-box", nil, true, false, 1024, 1, "available"},
		{"diagnostic", "diagnostic", nil, false, true, 1024, 2, "unavailable"},
		{"quota", "black-box", nil, true, false, 0, 1, "unavailable"},
		{"none", "diagnostic", &interceptor.ObservationSelection{Mode: "selected", Kinds: []string{"injection_delivery"}}, true, false, 1024, 0, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			allowed := []string{"target_output"}
			if tc.profile == "diagnostic" {
				allowed = append(allowed, "operation_error")
			}
			p, err := New("diagnostic", tc.profile, allowed, tc.selection)
			if err != nil {
				t.Fatal(err)
			}
			s, _ := view(p)
			r, err := HTTPS(catalog(t), p, s, "receipt", []byte("reply"), "text/plain", "HTTPS_STATUS_FAILED", tc.complete, tc.truncated, tc.maximum)
			if err != nil {
				t.Fatal(err)
			}
			var m Manifest
			_ = json.Unmarshal(r.ManifestJSON(), &m)
			if m.Boundary != "request-ended" || len(m.Entries) != tc.entries {
				t.Fatal(m)
			}
			if tc.entries > 0 && (m.Entries[0].Availability != tc.availability || m.Entries[0].Truncated != tc.truncated) {
				t.Fatal(m)
			}
			if tc.profile == "black-box" && bytes.Contains(r.RecordJSON(), []byte("HTTPS_STATUS_FAILED")) {
				t.Fatal("withheld error leaked")
			}
			var record Record
			_ = json.Unmarshal(r.RecordJSON(), &record)
			record.NativeReceiptID = interceptor.FeedbackReceiptID(s.SessionID, s.TurnID)
			if _, err = OpenRecord(catalog(t), encoded(record)); err == nil {
				t.Fatal("fabricated native receipt accepted")
			}
			if tc.entries > 0 {
				record.NativeReceiptID = ""
				m.Entries[0].Assurance = "native-oracle"
				record.Feedback = encoded(m)
				if _, err = OpenRecord(catalog(t), encoded(record)); err == nil {
					t.Fatal("fabricated assurance accepted")
				}
			}
		})
	}
}
