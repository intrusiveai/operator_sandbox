package contracts

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"math"
	"sort"
	"strconv"
	"strings"
	"unicode/utf16"
)

// RawDigest hashes exact bytes. It does not parse, normalize or verify content.
func RawDigest(raw []byte) string {
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// Canonicalize emits RFC 8785 bytes for the contract's stricter JSON domain.
// Schema validation must precede canonicalization when a schema applies: binary64
// rounding is an identity rule, not permission to repair invalid numeric fields.
// Both the input and output are bounded by maximum; no fields are omitted.
func Canonicalize(raw []byte, maximum int) ([]byte, error) {
	value, err := Decode(raw, maximum)
	if err != nil {
		return nil, err
	}
	return canonicalValue(value, maximum)
}

func CanonicalDigest(raw []byte, maximum int) (string, error) {
	canonical, err := Canonicalize(raw, maximum)
	if err != nil {
		return "", err
	}
	return RawDigest(canonical), nil
}

// canonicalValue accepts only values produced by Decode and private projections
// of those values. It is intentionally not an arbitrary Go-object serializer.
func canonicalValue(value any, maximum int) ([]byte, error) {
	var output bytes.Buffer
	write := func(s string) error {
		if len(s) > maximum-output.Len() {
			return ErrLimit
		}
		output.WriteString(s)
		return nil
	}
	var emit func(any) error
	emit = func(value any) error {
		switch v := value.(type) {
		case nil:
			return write("null")
		case bool:
			if v {
				return write("true")
			}
			return write("false")
		case string:
			return write(canonicalString(v))
		case json.Number:
			f, err := strconv.ParseFloat(string(v), 64)
			if err != nil {
				return ErrJSON
			}
			if f == 0 {
				return write("0")
			}
			if math.Abs(f) >= 1e-6 && math.Abs(f) < 1e21 {
				return write(strconv.FormatFloat(f, 'f', -1, 64))
			}
			s := strconv.FormatFloat(f, 'e', -1, 64)
			at := strings.IndexByte(s, 'e')
			exponent, err := strconv.Atoi(s[at+1:])
			if err != nil {
				return ErrJSON
			}
			sign := ""
			if exponent >= 0 {
				sign = "+"
			}
			return write(s[:at+1] + sign + strconv.Itoa(exponent))
		case []any:
			if err := write("["); err != nil {
				return err
			}
			for i, child := range v {
				if i > 0 {
					if err := write(","); err != nil {
						return err
					}
				}
				if err := emit(child); err != nil {
					return err
				}
			}
			return write("]")
		case map[string]any:
			type key struct {
				name  string
				units []uint16
			}
			keys := make([]key, 0, len(v))
			for name := range v {
				keys = append(keys, key{name, utf16.Encode([]rune(name))})
			}
			sort.Slice(keys, func(i, j int) bool {
				a, b := keys[i].units, keys[j].units
				for k := 0; k < len(a) && k < len(b); k++ {
					if a[k] != b[k] {
						return a[k] < b[k]
					}
				}
				return len(a) < len(b)
			})
			if err := write("{"); err != nil {
				return err
			}
			for i, k := range keys {
				if i > 0 {
					if err := write(","); err != nil {
						return err
					}
				}
				if err := write(canonicalString(k.name)); err != nil {
					return err
				}
				if err := write(":"); err != nil {
					return err
				}
				if err := emit(v[k.name]); err != nil {
					return err
				}
			}
			return write("}")
		default:
			return ErrJSON
		}
	}
	if err := emit(value); err != nil {
		return nil, err
	}
	return output.Bytes(), nil
}

func canonicalString(value string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range value {
		switch r {
		case '"', '\\':
			b.WriteByte('\\')
			b.WriteRune(r)
		case '\b':
			b.WriteString(`\b`)
		case '\t':
			b.WriteString(`\t`)
		case '\n':
			b.WriteString(`\n`)
		case '\f':
			b.WriteString(`\f`)
		case '\r':
			b.WriteString(`\r`)
		default:
			if r < 32 {
				const hex = "0123456789abcdef"
				b.WriteString(`\u00`)
				b.WriteByte(hex[r>>4])
				b.WriteByte(hex[r&15])
			} else {
				b.WriteRune(r)
			}
		}
	}
	b.WriteByte('"')
	return b.String()
}

func objectDigest(value any, maximum int) (string, error) {
	canonical, err := canonicalValue(value, maximum)
	if err != nil {
		return "", err
	}
	return RawDigest(canonical), nil
}
