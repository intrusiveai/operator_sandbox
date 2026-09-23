// Package contracts validates installed shared schemas without network access.
package contracts

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"math"
	"strconv"
	"strings"
	"unicode/utf8"
)

const OrdinaryLimit = 4 << 20
const ControlLimit = 64 << 10
const MaxDepth = 32
const MaxSafeInteger = 1<<53 - 1

var ErrJSON = errors.New("invalid contract JSON")
var ErrLimit = errors.New("contract encoding limit exceeded")

// Decode checks the wire representation before schema validation. The caller
// chooses the channel/message byte ceiling; numbers retain their decimal spelling.
func Decode(raw []byte, maximum int) (any, error) {
	if maximum <= 0 || len(raw) > maximum {
		return nil, ErrLimit
	}
	if !utf8.Valid(raw) {
		return nil, ErrJSON
	}
	if err := lexicalCheck(raw); err != nil {
		return nil, err
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	value, err := decodeValue(decoder, 0)
	if err != nil {
		return nil, err
	}
	if _, err = decoder.Token(); err != io.EOF {
		return nil, ErrJSON
	}
	return value, nil
}
func decodeValue(d *json.Decoder, depth int) (any, error) {
	token, err := d.Token()
	if err != nil {
		return nil, ErrJSON
	}
	switch v := token.(type) {
	case json.Delim:
		if depth >= MaxDepth {
			return nil, ErrLimit
		}
		switch v {
		case '{':
			object := map[string]any{}
			for d.More() {
				key, err := d.Token()
				if err != nil {
					return nil, ErrJSON
				}
				name, ok := key.(string)
				if !ok {
					return nil, ErrJSON
				}
				if _, exists := object[name]; exists {
					return nil, ErrJSON
				}
				value, err := decodeValue(d, depth+1)
				if err != nil {
					return nil, err
				}
				object[name] = value
			}
			end, err := d.Token()
			if err != nil || end != json.Delim('}') {
				return nil, ErrJSON
			}
			return object, nil
		case '[':
			array := []any{}
			for d.More() {
				value, err := decodeValue(d, depth+1)
				if err != nil {
					return nil, err
				}
				array = append(array, value)
			}
			end, err := d.Token()
			if err != nil || end != json.Delim(']') {
				return nil, ErrJSON
			}
			return array, nil
		}
		return nil, ErrJSON
	case json.Number:
		// Huge exponents and nonfinite binary64 values cannot enter jcs-v1.
		number, err := strconv.ParseFloat(string(v), 64)
		if err != nil || math.IsInf(number, 0) || math.IsNaN(number) {
			return nil, ErrJSON
		}
		if math.Trunc(number) == number && math.Abs(number) > MaxSafeInteger {
			return nil, ErrJSON
		}
		// Normalize numerical zero so validators never expand a huge zero exponent.
		if number == 0 {
			mantissa := strings.FieldsFunc(string(v), func(r rune) bool { return r == 'e' || r == 'E' })[0]
			if strings.ContainsAny(mantissa, "123456789") {
				return nil, ErrJSON
			} // Underflow.
			return json.Number("0"), nil
		}
		return v, nil
	default:
		return token, nil
	}
}

// Reject depth before object allocation, and lone UTF-16 surrogate escapes before
// encoding/json can replace them with U+FFFD. Ordinary escape syntax is decoded later.
func lexicalCheck(raw []byte) error {
	depth := 0
	quoted := false
	for i := 0; i < len(raw); i++ {
		c := raw[i]
		if quoted {
			if c == '"' {
				quoted = false
				continue
			}
			if c != '\\' {
				continue
			}
			i++
			if i >= len(raw) {
				return ErrJSON
			}
			if raw[i] != 'u' {
				continue
			}
			if i+4 >= len(raw) {
				return ErrJSON
			}
			code, err := strconv.ParseUint(string(raw[i+1:i+5]), 16, 16)
			if err != nil {
				return ErrJSON
			}
			i += 4
			if code >= 0xDC00 && code <= 0xDFFF {
				return ErrJSON
			}
			if code >= 0xD800 && code <= 0xDBFF {
				if i+6 >= len(raw) || raw[i+1] != '\\' || raw[i+2] != 'u' {
					return ErrJSON
				}
				low, err := strconv.ParseUint(string(raw[i+3:i+7]), 16, 16)
				if err != nil || low < 0xDC00 || low > 0xDFFF {
					return ErrJSON
				}
				i += 6
			}
		} else {
			switch c {
			case '"':
				quoted = true
			case '{', '[':
				depth++
				if depth > MaxDepth {
					return ErrLimit
				}
			case '}', ']':
				depth--
			}
		}
	}
	return nil
}
