package contracts

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// SafeArgumentPath validates a bounded JSON Pointer without a field-name catalog.
// Renderers must quote paths as data, never treat their segments as instructions.
func SafeArgumentPath(path string) (string, bool) {
	if len(path) > 256 || !utf8.ValidString(path) || path != "" && !strings.HasPrefix(path, "/") || sensitiveFieldName.MatchString(path) {
		return "", false
	}
	if strings.IndexFunc(path, func(r rune) bool { return unicode.IsControl(r) || unicode.In(r, unicode.Cf, unicode.Zl, unicode.Zp) }) >= 0 {
		return "", false
	}
	for i := 0; i < len(path); i++ {
		if path[i] == '~' {
			i++
			if i >= len(path) || (path[i] != '0' && path[i] != '1') {
				return "", false
			}
		}
	}
	return path, true
}
