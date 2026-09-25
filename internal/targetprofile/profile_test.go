package targetprofile

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

var example = []byte(`{"api_version":"operator.dev/target-profile/v1alpha1","id":"local","target_id":"delivery-example","adapter":"interceptor/v1","allow_target_stop":false,"scopes":{"operation_ids":["invoke"],"caller_principal_ids":[],"routes":[],"allow_retained_injections":false},"feedback":{"ceiling":"black-box","allowed_kinds":["target_output"],"max_attempt_bytes":1048576},"operation_timeout_ms":30000}`)

func TestProfilePrivateFrozenAndClosed(t *testing.T) {
	name := filepath.Join(t.TempDir(), "target.json")
	if err := os.WriteFile(name, example, 0600); err != nil {
		t.Fatal(err)
	}
	p, err := Load(name)
	if err != nil {
		t.Fatal(err)
	}
	settings := p.Settings()
	settings.Scopes.OperationIDs[0] = "changed"
	raw := p.JSON()
	raw[0] = 'x'
	if p.Settings().Scopes.OperationIDs[0] != "invoke" || p.JSON()[0] != '{' {
		t.Fatal("mutable policy")
	}
	for _, raw := range [][]byte{bytes.Replace(example, []byte(`"black-box"`), []byte(`"unknown"`), 1), bytes.Replace(example, []byte(`30000`), []byte(`30001`), 1), bytes.Replace(example, []byte(`"target_output"`), []byte(`"oracle_outcome"`), 1), bytes.Replace(example, []byte(`"id":"local"`), []byte(`"id":"local","id":"other"`), 1), bytes.Replace(example, []byte(`"id":"local"`), []byte(`"id":"local","shell":"exec"`), 1)} {
		if _, err := Parse(raw); err == nil {
			t.Fatal("invalid profile accepted")
		}
	}
	if err = os.Chmod(name, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err = Load(name); err == nil {
		t.Fatal("public policy file accepted")
	}
}
