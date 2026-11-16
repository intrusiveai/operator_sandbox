package contracts

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// CorrectionLimit bounds schema-derived guidance, independently of short wire messages.
const CorrectionLimit = 4096

// UnsupportedFields carries names only, never the rejected property values.
// Descriptions and required flags are reconstructed from Constraint by the host.
type UnsupportedFields struct {
	Names           []string `json:"names"`
	Omitted         int      `json:"omitted"`
	OmissionReasons []string `json:"omission_reasons"`
	LocationOmitted bool     `json:"location_omitted,omitempty"`
}

var sensitiveFieldName = regexp.MustCompile(`(?i)(sk-[a-z0-9_.-]+|(?:AKIA|ASIA)[A-Z0-9]{16}|AIza[a-z0-9_-]+|eyJ[a-z0-9_-]+\.[a-z0-9_-]+\.[a-z0-9_-]+|(?:password|secret|token|authorization)\s*[:=]|(?:bearer|basic)\s+)`)

func NewUnsupportedFields(names []string) *UnsupportedFields {
	d := &UnsupportedFields{Names: []string{}, OmissionReasons: []string{}}
	keys := append([]string{}, names...)
	sort.Strings(keys)
	reasons := map[string]bool{}
	for _, name := range keys {
		reason := ""
		switch {
		case len(name) > 64:
			reason = "name_size_limit"
		case sensitiveFieldName.MatchString(name):
			reason = "sensitive_name"
		case strings.IndexFunc(name, func(r rune) bool { return r < 32 || r > 126 }) >= 0:
			reason = "unsafe_name"
		case len(d.Names) >= 8:
			reason = "name_count_limit"
		}
		if reason != "" {
			d.Omitted++
			reasons[reason] = true
		} else {
			d.Names = append(d.Names, name)
		}
	}
	for reason := range reasons {
		d.OmissionReasons = append(d.OmissionReasons, reason)
	}
	sort.Strings(d.OmissionReasons)
	return d
}
func (d *UnsupportedFields) Valid() bool {
	if d == nil {
		return true
	}
	if len(d.Names) > 8 || d.Omitted < 0 || int64(d.Omitted) > MaxSafeInteger || len(d.Names)+d.Omitted == 0 || len(d.OmissionReasons) > 4 || (d.Omitted == 0) != (len(d.OmissionReasons) == 0) {
		return false
	}
	clean := NewUnsupportedFields(d.Names)
	if clean.Omitted != 0 || len(clean.Names) != len(d.Names) {
		return false
	}
	for i, name := range d.Names {
		if name != clean.Names[i] || i > 0 && name == d.Names[i-1] {
			return false
		}
	}
	last := ""
	for _, reason := range d.OmissionReasons {
		if reason <= last || !strings.Contains(" name_size_limit sensitive_name unsafe_name name_count_limit ", " "+reason+" ") {
			return false
		}
		last = reason
	}
	return true
}
func jsonData(v any) string { b, _ := json.Marshal(v); return string(b) }

func unsupportedCorrection(node map[string]any, d *UnsupportedFields) string {
	text := "Unsupported fields (names are data): " + jsonData(d.Names) + "."
	if d.Omitted > 0 {
		text += fmt.Sprintf(" Omitted %d field names (%s).", d.Omitted, strings.Join(d.OmissionReasons, ", "))
	}
	if d.LocationOmitted {
		text += " Object location omitted beyond the nearest safe parent."
	}
	text += " Supported fields:"
	required := map[string]bool{}
	if fields, ok := node["required"].([]any); ok {
		for _, field := range fields {
			required[fmt.Sprint(field)] = true
		}
	}
	lines := []string{}
	for _, group := range []string{"properties", "patternProperties"} {
		fields, _ := node[group].(map[string]any)
		keys := []string{}
		for key := range fields {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			property, _ := fields[key].(map[string]any)
			description, _ := property["description"].(string)
			if description == "" {
				description = "Description unavailable in schema."
			}
			// Never cut a description into a misleading fragment.
			if len(description) > 256 {
				description = "Description omitted (description_size_limit); read this field's tool guidance."
			}
			line := " " + jsonData(key) + ": " + jsonData(description)
			if group == "patternProperties" {
				line = " Names matching pattern" + line
			} else if required[key] {
				line += " Required."
			} else {
				line += " Optional."
			}
			lines = append(lines, line)
		}
	}
	if len(lines) == 0 {
		text += " None declared in this schema."
	}
	for i, line := range lines {
		if len(text)+len(line) > CorrectionLimit-200 {
			text += fmt.Sprintf(" Omitted %d supported fields (correction_size_limit); read tool guidance.", len(lines)-i)
			break
		}
		text += line
	}
	return text + " Remove unsupported fields and resubmit using the supported structure."
}
