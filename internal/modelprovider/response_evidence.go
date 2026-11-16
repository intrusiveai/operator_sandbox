//go:build linux || darwin

package modelprovider

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/intrusiveai/operator_sandbox/contracts"
)

// ResponseEvidence is a request-local, credential-redacted copy for private
// administrator evidence only. It is never a usable model result.
type ResponseEvidence struct {
	Body     []byte
	Omission string
	Redacted bool
}

// RejectedResponse is the durable, content-free description of that copy.
// Digest addresses retained bytes, which may differ from the received body.
type RejectedResponse struct {
	State     string `json:"state,omitempty"`
	Digest    string `json:"digest,omitempty"`
	SizeBytes int64  `json:"size_bytes,omitempty"`
	Redacted  bool   `json:"redacted,omitempty"`
	Omission  string `json:"omission,omitempty"`
}

func (r RejectedResponse) valid() bool {
	switch r.State {
	case "":
		return r == (RejectedResponse{})
	case "retained":
		return r.SizeBytes > 0 && r.SizeBytes <= contracts.OrdinaryLimit && len(r.Digest) == 71 && strings.HasPrefix(r.Digest, "sha256:") && strings.Trim(r.Digest[7:], "0123456789abcdef") == "" && r.Omission == ""
	case "omitted", "unconfirmed":
		if r.Digest != "" || r.SizeBytes != 0 || r.Redacted {
			return false
		}
		switch r.Omission {
		case "no_complete_response", "response_too_large", "unsupported_encoding", "invalid_json_redaction_unavailable", "redacted_size_limit", "provider_evidence_unavailable", "http_rejection", "retention_failed":
			return true
		}
	}
	return false
}
func (d Diagnostic) ValidationDetailsText() string {
	if !d.Valid() {
		return ""
	}
	var lines []string
	if d.ValidationStage != "" {
		lines = append(lines, "Validation stage: "+d.ValidationStage)
	}
	if d.Category == "response_validation" {
		lines = append(lines, "Validation rule: "+d.Reason)
	}
	if d.FieldPath != "" {
		lines = append(lines, "Field: "+d.FieldPath)
	} else if d.FieldPathOmitted {
		lines = append(lines, "Field omitted: unsafe or unavailable field path")
	}
	r := d.RejectedResponse
	switch r.State {
	case "retained":
		line := fmt.Sprintf("Rejected, untrusted response: administrator evidence content/%s (%d bytes)", strings.TrimPrefix(r.Digest, "sha256:"), r.SizeBytes)
		if r.Redacted {
			line += "; credential/routing content redacted"
		}
		lines = append(lines, line)
	case "omitted", "unconfirmed":
		lines = append(lines, "Rejected response evidence "+r.State+": "+r.Omission)
	}
	return strings.Join(lines, "\n")
}

// Preserve original JSON spelling and duplicate keys. Redact decoded JSON string
// tokens (including escaped credentials) rather than decoding into a map, which
// would destroy useful evidence about duplicate keys. Malformed JSON is omitted
// when we cannot safely identify/redact its strings. No headers are retained.
func privateResponse(raw []byte, secrets []string) ResponseEvidence {
	if len(raw) == 0 || !utf8.Valid(raw) || !json.Valid(raw) {
		return ResponseEvidence{Omission: "invalid_json_redaction_unavailable"}
	}
	redact := newDetailRedactor(secrets)
	var out bytes.Buffer
	sensitiveValue := false
	for i := 0; i < len(raw); {
		if sensitiveValue && raw[i] != ' ' && raw[i] != '\r' && raw[i] != '\n' && raw[i] != '\t' {
			dec := json.NewDecoder(bytes.NewReader(raw[i:]))
			var value json.RawMessage
			if dec.Decode(&value) != nil {
				return ResponseEvidence{Omission: "invalid_json_redaction_unavailable"}
			}
			i += int(dec.InputOffset())
			out.WriteString(`"[REDACTED]"`)
			sensitiveValue = false
			continue
		}
		if raw[i] != '"' {
			out.WriteByte(raw[i])
			i++
			continue
		}
		end := i + 1
		for end < len(raw) {
			if raw[end] == '\\' {
				end += 2
				continue
			}
			if raw[end] == '"' {
				end++
				break
			}
			end++
		}
		var text string
		if json.Unmarshal(raw[i:end], &text) != nil {
			return ResponseEvidence{Omission: "invalid_json_redaction_unavailable"}
		}
		j := end
		for j < len(raw) && (raw[j] == ' ' || raw[j] == '\r' || raw[j] == '\n' || raw[j] == '\t') {
			j++
		}
		selected := redact(text)
		if selected == text {
			out.Write(raw[i:end])
		} else {
			encoded, _ := json.Marshal(selected)
			out.Write(encoded)
		}
		i = end
		if j < len(raw) && raw[j] == ':' && sensitiveResponseField(text) {
			out.Write(raw[i : j+1])
			i = j + 1
			sensitiveValue = true
		}
		if out.Len() > contracts.OrdinaryLimit {
			return ResponseEvidence{Omission: "redacted_size_limit"}
		}
	}
	if out.Len() > contracts.OrdinaryLimit {
		return ResponseEvidence{Omission: "redacted_size_limit"}
	}
	body := out.Bytes()
	return ResponseEvidence{Body: body, Redacted: !bytes.Equal(body, raw)}
}
func sensitiveResponseField(field string) bool {
	field = strings.NewReplacer("-", "", "_", "", " ", "").Replace(strings.ToLower(field))
	switch field {
	case "authorization", "apikey", "accesskeyid", "secretaccesskey", "accesstoken", "refreshtoken", "sessiontoken", "password", "credential", "credentials":
		return true
	}
	return false
}
