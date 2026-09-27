package campaignlimits

import (
	"encoding/json"
	"testing"

	"github.com/intrusiveai/operator_sandbox/contracts"
)

func TestEffectiveLimitsNarrowWithoutResettingHost(t *testing.T) {
	p, err := localProtocol()
	if err != nil {
		t.Fatal(err)
	}
	host, err := ParseHost([]byte(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	baseline, err := Resolve(p, host, []byte(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]int64{"campaign_time_ms": 1800000, "attempt_admissions": 100, "model_tokens": 250000, "model_turns": 300, "artifact_bytes": 1 << 30, "artifact_objects": 4096, "snapshot_admissions": 20, "snapshot_bytes": 1 << 30, "observation_reads": 2000, "observation_bytes": 1 << 28}
	for k, v := range want {
		if baseline.Campaign[k] != v {
			t.Fatal(k, baseline.Campaign)
		}
	}
	narrowed, err := Resolve(p, host, []byte(`{"attempt_admissions":3,"active_seconds":40,"model_tokens":900,"snapshot_creations":2,"snapshot_committed_bytes":4096,"harness":{"max_model_turns":4,"max_read_bytes":32}}`))
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range map[string]int64{"attempt_admissions": 3, "campaign_time_ms": 40000, "model_tokens": 900, "snapshot_admissions": 2, "snapshot_bytes": 4096, "model_turns": 4, "observation_bytes": 32} {
		if narrowed.Campaign[k] != v {
			t.Fatal(k, narrowed.Campaign)
		}
	}
	widened, err := Resolve(p, host, []byte(`{"attempt_admissions":9007199254740991,"active_seconds":9007199254740991,"snapshot_creations":9007199254740991,"harness":{"max_model_turns":9007199254740991,"max_read_bytes":9007199254740991}}`))
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range want {
		if widened.Campaign[k] != v {
			t.Fatal("requested widening", k)
		}
	}
	narrowed.Harness["max_model_turns"] = 1
	narrowed.Campaign["model_tokens"] = 1
	again, err := Resolve(p, host, []byte(`{}`))
	if err != nil || again.Campaign["model_tokens"] != 250000 || again.Harness["max_model_turns"] != 300 {
		t.Fatal("host changed")
	}
}
func TestSnapshotsCanBeDisabledAndAllCountersRemainExact(t *testing.T) {
	p, _ := localProtocol()
	for _, raw := range []string{`{"max_snapshot_admissions":0}`, `{"max_snapshot_bytes":0}`, `{"max_snapshot_bytes":9007199254740991}`, `{"max_snapshot_admissions":9007199254740991}`} {
		host, err := ParseHost([]byte(raw))
		if err != nil {
			t.Fatal(err)
		}
		result, err := Resolve(p, host, []byte(`{"snapshot_creations":1,"snapshot_committed_bytes":1}`))
		if err != nil {
			t.Fatal(err)
		}
		var values map[string]int64
		json.Unmarshal([]byte(raw), &values)
		for k, v := range values {
			if result.Campaign[hostFields[k]] != min(v, 1) {
				t.Fatal("zero silently replaced or counter widened")
			}
		}
		encoded, err := json.Marshal(host)
		if err != nil {
			t.Fatal(err)
		}
		decoded, err := ParseHost(encoded)
		if err != nil {
			t.Fatal(err)
		}
		for k, v := range host.values {
			if decoded.values[k] != v {
				t.Fatal("roundtrip changed")
			}
		}
	}
	if _, err := Resolve(p, Host{}, []byte(`{}`)); err == nil {
		t.Fatal("uninitialized host")
	}
	if _, err := Resolve(nil, Host{}, []byte(`{}`)); err == nil {
		t.Fatal("missing protocol")
	}
}
func TestLimitsRejectMalformedAndUnknownConfiguration(t *testing.T) {
	p, _ := localProtocol()
	host, _ := ParseHost([]byte(`{}`))
	for _, raw := range []string{`null`, `[]`, `{"max_active_seconds":1801}`, `{"max_active_seconds":0}`, `{"max_attempt_admissions":0}`, `{"max_snapshot_bytes":-1}`, `{"max_snapshot_bytes":9007199254740992}`, `{"max_snapshot_bytes":1.5}`, `{"max_snapshot_bytes":"1"}`, `{"max_snapshot_bytes":1,"max_snapshot_bytes":2}`, `{"harness":null}`, `{"harness":{"max_model_turns":0}}`, `{"harness":{"shell":1}}`, `{"unknown":1}`} {
		if _, err := ParseHost([]byte(raw)); err == nil {
			t.Fatal("accepted host", raw)
		}
	}
	for _, raw := range []string{`null`, `[]`, `{"max_snapshot_bytes":1}`, `{"active_seconds":0}`, `{"snapshot_creations":0}`, `{"model_tokens":1.5}`, `{"model_tokens":9007199254740992}`, `{"harness":[]}`, `{"harness":{"max_read_bytes":0}}`, `{"unknown":1}`} {
		if _, err := Resolve(p, host, []byte(raw)); err == nil {
			t.Fatal("accepted request", raw)
		}
	}
	if contracts.MaxSafeInteger != 9007199254740991 {
		t.Fatal("shared integer bound changed")
	}
}
