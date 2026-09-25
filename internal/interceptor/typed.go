package interceptor

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"time"
)

// DecodeTypedBody enforces native object framing and exact struct field names,
// including nested objects. Unlike encoding/json alone, null scalar values and
// case-insensitive aliases are rejected. It does not validate result semantics.
func DecodeTypedBody(raw []byte, target any, maximum int) error {
	t := reflect.TypeOf(target)
	if maximum <= 0 || len(raw) > maximum || t == nil || t.Kind() != reflect.Pointer || reflect.ValueOf(target).IsNil() {
		return ErrRequest
	}
	if _, err := object(raw); err != nil || !typedShape(raw, t.Elem()) {
		return ErrRequest
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	d.DisallowUnknownFields()
	if d.Decode(target) != nil {
		return ErrRequest
	}
	return nil
}

func typedShape(raw json.RawMessage, t reflect.Type) bool {
	raw = bytes.TrimSpace(raw)
	if t == reflect.TypeOf(json.RawMessage{}) || t.Kind() == reflect.Interface {
		return true
	}
	if t.Kind() == reflect.Pointer {
		return !bytes.Equal(raw, []byte("null")) && typedShape(raw, t.Elem())
	}
	if t == reflect.TypeOf(time.Time{}) {
		return len(raw) > 1 && raw[0] == '"'
	}
	switch t.Kind() {
	case reflect.Struct:
		m, err := object(raw)
		if err != nil {
			return false
		}
		for i := 0; i < t.NumField(); i++ {
			f := t.Field(i)
			tag := strings.Split(f.Tag.Get("json"), ",")
			v, exists := m[tag[0]]
			if !exists {
				if len(tag) < 2 || tag[1] != "omitempty" {
					return false
				}
				continue
			}
			if !typedShape(v, f.Type) {
				return false
			}
			delete(m, tag[0])
		}
		return len(m) == 0
	case reflect.Map:
		m, err := object(raw)
		if err != nil {
			return false
		}
		for _, v := range m {
			if !typedShape(v, t.Elem()) {
				return false
			}
		}
		return true
	case reflect.Slice:
		if t.Elem().Kind() == reflect.Uint8 {
			return len(raw) > 1 && raw[0] == '"'
		}
		if bytes.Equal(raw, []byte("null")) {
			return true
		} // native nil collections
		var a []json.RawMessage
		if json.Unmarshal(raw, &a) != nil {
			return false
		}
		for _, v := range a {
			if !typedShape(v, t.Elem()) {
				return false
			}
		}
		return true
	default:
		return !bytes.Equal(raw, []byte("null"))
	}
}

func AttemptContextDigest(a AttemptContext) string {
	a.Digest = ""
	raw, err := json.Marshal(a)
	if err != nil {
		return ""
	}
	return rawDigest(raw)
}

func ObservationViewDigest(v ObservationView) string {
	v.Hash = ""
	raw, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	return rawDigest(raw)
}

func FeedbackReceiptID(session, turn string) string {
	return "feedback-" + strings.TrimPrefix(rawDigest([]byte(session+"\x00"+turn)), "sha256:")
}
