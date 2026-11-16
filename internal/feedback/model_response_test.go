package feedback

import (
	"bytes"
	"github.com/intrusiveai/operator_sandbox/internal/interceptor"
	"testing"
)

func TestModelResponseFeedbackVisibilityAndMeaning(t *testing.T) {
	for _, state := range []string{"applied", "failed", "response_written", "write_failed"} {
		for _, profile := range []string{"black-box", "diagnostic", "oracle-assisted"} {
			t.Run(profile+"/"+state, func(t *testing.T) {
				allowed := []string{"injection_delivery"}
				if profile == "black-box" {
					allowed = []string{"target_output"}
				}
				p, err := New("oracle-assisted", profile, allowed, nil)
				if err != nil {
					t.Fatal(err)
				}
				s, v := view(p)
				body := encoded(map[string]any{"injection_id": "native-id", "state": state, "scope": "turn", "surface": "model_response", "direction": "model_to_application", "secret": "PRIVATE", "event_seq": 12})
				entry(&v, "delivery", "injection_delivery", body, interceptor.HarnessVisible)
				r, err := Project(catalog(t), p, s, "receipt", seal(v), map[string][]byte{"delivery": body}, map[string]string{"native-id": "setup-1"}, 4096)
				if err != nil {
					t.Fatal(err)
				}
				if profile == "black-box" {
					if len(r.entries) != 0 {
						t.Fatal("hidden feedback exposed")
					}
					return
				}
				if len(r.entries) != 1 {
					t.Fatal("missing delivery")
				}
				got, _ := r.Content(r.entries[0].entry.ID)
				for _, want := range []string{state, "model_response", "model_to_application", "setup-1"} {
					if !bytes.Contains(got, []byte(want)) {
						t.Fatal(string(got))
					}
				}
				for _, hidden := range []string{"PRIVATE", "native-id", "event_seq", "succeeded"} {
					if bytes.Contains(got, []byte(hidden)) {
						t.Fatal(string(got))
					}
				}
				if _, err = r.Read("campaign", "receipt", r.entries[0].entry.ID, 0, 4096, true, allowed); err != nil {
					t.Fatal(err)
				}
			})
		}
	}
	for _, body := range []string{
		`{"injection_id":"native-id","state":"response_written","scope":"turn"}`,
		`{"injection_id":"native-id","state":"response_written","scope":"turn","surface":"model_response","direction":"application_to_model"}`,
		`{"injection_id":"native-id","state":"succeeded","scope":"turn","surface":"model_response","direction":"model_to_application"}`,
	} {
		if _, err := normalize("injection_delivery", []byte(body), false, map[string]string{"native-id": "setup-1"}, "session"); err == nil {
			t.Fatal("invalid delivery accepted")
		}
	}
}
