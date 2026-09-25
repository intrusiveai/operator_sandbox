package interceptor

import (
	"bytes"
	"encoding/json"
	"os"
	"testing"
)

func TestNativeAttemptAndObservationGoldenDigests(t *testing.T) {
	aRaw, err := os.ReadFile("testdata/attempt-context.json")
	if err != nil {
		t.Fatal(err)
	}
	var a AttemptContext
	if err = DecodeTypedBody(aRaw, &a, JSONLimit); err != nil {
		t.Fatal(err)
	}
	if AttemptContextDigest(a) != a.Digest {
		t.Fatal("native attempt digest mismatch")
	}
	vRaw, err := os.ReadFile("testdata/observation-view.json")
	if err != nil {
		t.Fatal(err)
	}
	var v ObservationView
	if err = DecodeTypedBody(vRaw, &v, JSONLimit); err != nil {
		t.Fatal(err)
	}
	if ObservationViewDigest(v) != v.Hash || v.AttemptContextDigest != a.Digest || v.ThroughEventSeq != 9007199254740993 || FeedbackReceiptID(v.SessionID, v.TurnID) != v.ReceiptID {
		t.Fatal("native observation identity mismatch")
	}
	for _, raw := range [][]byte{
		bytes.Replace(aRaw, []byte(`"feedback_profile"`), []byte(`"FEEDBACK_PROFILE"`), 1),
		bytes.Replace(aRaw, []byte(`"size_bytes": 2`), []byte(`"size_bytes": null`), 1),
		bytes.Replace(aRaw, []byte(`"size_bytes": 2`), []byte(`"size_bytes": 2, "size_bytes": 3`), 1),
		bytes.Replace(aRaw, []byte(`"size_bytes": 2`), []byte(`"size_bytes": 2, "extra": true`), 1),
	} {
		var invalid AttemptContext
		if DecodeTypedBody(raw, &invalid, JSONLimit) == nil {
			t.Fatal("invalid nested field accepted")
		}
	}
	// json.Marshal is the native digest preimage, not JCS. The captured fixture
	// includes a sequence above the shared safe-integer domain to pin separation.
	b, _ := json.Marshal(v)
	if !bytes.Contains(b, []byte("9007199254740993")) {
		t.Fatal("native integer lost precision")
	}
}
