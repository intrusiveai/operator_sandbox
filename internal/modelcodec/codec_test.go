package modelcodec

import (
	"encoding/json"
	"github.com/intrusiveai/operator_sandbox/contracts"
	"os"
	"testing"
)

func samples(t *testing.T) []map[string]any {
	t.Helper()
	var all []map[string]any
	for _, name := range []string{"model-codec", "responses-model-codec", "anthropic-model-codec", "bedrock-model-codec", "gemini-model-codec"} {
		raw, e := os.ReadFile("../../schemas/fixtures/" + name + ".json")
		if e != nil {
			t.Fatal(e)
		}
		v, e := contracts.Decode(raw, contracts.OrdinaryLimit)
		if e != nil {
			t.Fatal(e)
		}
		for _, c := range v.([]any) {
			m := c.(map[string]any)
			if m["valid"] == true {
				all = append(all, m)
			}
		}
	}
	return all
}
func TestConfiguredRequestsAndOpaqueConversation(t *testing.T) {
	seen := map[string]bool{}
	for _, f := range samples(t) {
		if f["mode"] != "context" {
			continue
		}
		p := object(f["policy"])
		codec := str(p["codec_id"])
		if seen[codec] {
			continue
		}
		seen[codec] = true
		native := object(object(f["request"])["request"])
		field := "messages"
		if codec == Responses {
			field = "input"
		}
		if codec == Gemini {
			field = "contents"
		}
		history := array(native[field])
		if codec == Chat {
			history = history[1:]
		}
		history = append(history, map[string]any{"metadata": map[string]any{"url": "https://untrusted.example", "tools": []string{"forbidden"}, "model": "other-model"}, "native_future_field": true})
		for _, purpose := range []string{"generation", "compaction"} {
			t.Run(codec+purpose, func(t *testing.T) {
				q, e := Build(raw(p), raw(map[string]any{"purpose": purpose, "conversation": history}))
				if e != nil {
					t.Fatal(e)
				}
				v, e := contracts.Decode(q, contracts.OrdinaryLimit)
				if e != nil {
					t.Fatal(e)
				}
				m := object(v)
				if m["endpoint"] != nil || m["url"] != nil || m["metadata"] != nil {
					t.Fatal("conversation promoted into settings")
				}
				if codec != Bedrock && codec != Gemini && m["model"] != p["request_model"] {
					t.Fatal("model not host selected")
				}
				got := array(m[field])
				if codec == Chat {
					if object(got[0])["content"] != p["prompt"] {
						t.Fatal("prompt changed")
					}
					got = got[1:]
				}
				if string(raw(got)) != string(raw(history)) {
					t.Fatal("conversation changed")
				}
				if purpose == "compaction" && (len(array(m["tools"])) > 0 || m["toolConfig"] != nil) {
					t.Fatal("compaction enabled tools")
				}
			})
		}
		if _, e := Build(raw(p), []byte(`{"purpose":"generation","conversation":[],"model":"other"}`)); e == nil {
			t.Fatal("accepted extra settings")
		}
	}
	if len(seen) != 5 {
		t.Fatal(seen)
	}
}
func TestProviderAccountingAndAdditionalMetadata(t *testing.T) {
	tested := map[string]bool{}
	for _, f := range samples(t) {
		if f["mode"] != "bound" {
			continue
		}
		r := object(f["result"])
		codec := str(r["codec_id"])
		if r == nil {
			continue
		}
		response := object(r["response"])
		response["future_metadata"] = map[string]any{"provider_option": []any{true, nil, json.Number("1.25")}}
		response["model"] = "a-new-provider-version" // informational response label is not an authority pin
		parsed, e := Parse(codec, raw(response))
		if e != nil {
			t.Fatalf("%v: %v", f["name"], e)
		}
		if metrics := object(f["metrics"]); metrics != nil && string(raw(parsed.Metrics)) != string(raw(metrics)) {
			actual, _ := contracts.Decode(raw(parsed.Metrics), contracts.OrdinaryLimit)
			if string(raw(actual)) != string(raw(metrics)) {
				t.Fatalf("%v: %#v expected %v", f["name"], parsed.Metrics, metrics)
			}
		}
		if expected := str(f["disposition"]); expected != "" && parsed.Disposition != expected {
			t.Fatalf("%v: disposition %s expected %s", f["name"], parsed.Disposition, expected)
		}
		tested[codec] = true
	}
	if len(tested) != 5 {
		t.Fatal(tested)
	}
}
func TestConsumedFieldErrors(t *testing.T) {
	for _, f := range samples(t) {
		if f["name"] != "native text and usage preserved" {
			continue
		}
		original := raw(object(f["result"])["response"])
		for _, mode := range []string{"unknown", "string", "negative", "fraction", "sum", "shape"} {
			t.Run(mode, func(t *testing.T) {
				v, _ := contracts.Decode(original, contracts.OrdinaryLimit)
				r := object(v)
				u := object(r["usage"])
				switch mode {
				case "unknown":
					delete(r, "usage")
				case "string":
					u["prompt_tokens"] = "secret"
				case "negative":
					u["prompt_tokens"] = json.Number("-1")
				case "fraction":
					u["prompt_tokens"] = json.Number("1.5")
				case "sum":
					u["total_tokens"] = json.Number("999")
				case "shape":
					r["choices"] = true
				}
				parsed, e := Parse(Chat, raw(r))
				if mode == "unknown" {
					if e != nil || parsed.Metrics.Known {
						t.Fatal(e, parsed)
					}
				} else if e == nil {
					t.Fatal("accepted invalid consumed field")
				}
			})
		}
		return
	}
	t.Fatal("missing fixture")
}
