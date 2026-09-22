package pcoapi

import (
	"fmt"
	"sort"
	"strings"
)

// ExtensionByFormat maps a conversion_formats name to the extension it downloads as; the
// deployment's pco/pipeline/document_outputs.py is the source of truth. `latex` is the one
// whose extension differs from its name.
var ExtensionByFormat = map[string]string{
	"md": "md", "html": "html", "latex": "tex", "docx": "docx", "pptx": "pptx", "xlsx": "xlsx",
	"tex.zip": "tex.zip", "md.zip": "md.zip", "mmd.zip": "mmd.zip", "html.zip": "html.zip",
}

// DirectExtensions are written for every completed document without being requested.
var DirectExtensions = []string{"mmd", "lines.json"}

// ParseFormats validates a comma-separated --formats value and returns the canonical names in
// stable order. `tex` is accepted for `latex`.
func ParseFormats(value string) ([]string, error) {
	if strings.TrimSpace(value) == "" {
		return nil, nil
	}
	seen := map[string]bool{}
	for _, raw := range strings.Split(value, ",") {
		name := strings.TrimSpace(strings.ToLower(raw))
		if name == "" {
			continue
		}
		if name == "tex" {
			name = "latex"
		}
		if _, ok := ExtensionByFormat[name]; !ok {
			known := make([]string, 0, len(ExtensionByFormat))
			for format := range ExtensionByFormat {
				known = append(known, format)
			}
			sort.Strings(known)
			return nil, fmt.Errorf("unknown format %q; known: %s", raw, strings.Join(known, ", "))
		}
		seen[name] = true
	}
	formats := make([]string, 0, len(seen))
	for name := range seen {
		formats = append(formats, name)
	}
	sort.Strings(formats)
	return formats, nil
}

// ConversionFormats is the request field for a list of formats: {"docx": true, "md": true}.
func ConversionFormats(formats []string) map[string]bool {
	fields := map[string]bool{}
	for _, name := range formats {
		fields[name] = true
	}
	return fields
}
