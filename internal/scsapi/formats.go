package scsapi

import (
	"sort"
	"strings"
)

// alwaysFormats are written for every completed document without being requested; they are
// download-only, never named in conversion_formats.
var alwaysFormats = map[string]bool{
	"mmd":            true,
	"lines.json":     true,
	"lines.mmd.json": true,
}

// conversionFormats must be named in conversion_formats on the submit body before they can be
// downloaded. The download extension equals the format name (GET /v3/pdf/{id}.{format}).
var conversionFormats = map[string]bool{
	"md":              true,
	"docx":            true,
	"tex.zip":         true,
	"html":            true,
	"pptx":            true,
	"xlsx":            true,
	"pdf":             true,
	"latex.pdf":       true,
	"mmd.overlay.pdf": true,
	"mmd.zip":         true,
	"md.zip":          true,
	"html.zip":        true,
}

// KnownFormat reports whether name is a format the API produces.
func KnownFormat(name string) bool {
	return alwaysFormats[name] || conversionFormats[name]
}

// IsConversionFormat reports whether a format must be requested in conversion_formats (as opposed to
// being generated automatically).
func IsConversionFormat(name string) bool { return conversionFormats[name] }

// KnownFormats returns every format name, longest first, so extension matching prefers the most
// specific (e.g. "tex.zip" over a hypothetical "zip", "lines.mmd.json" over "json").
func KnownFormats() []string {
	names := make([]string, 0, len(alwaysFormats)+len(conversionFormats))
	for n := range alwaysFormats {
		names = append(names, n)
	}
	for n := range conversionFormats {
		names = append(names, n)
	}
	sort.Slice(names, func(i, j int) bool { return len(names[i]) > len(names[j]) })
	return names
}

// FormatOfFile returns the API format a filename names, by longest matching known extension.
// "paper.tex.zip" → "tex.zip", "scan.mmd" → "mmd". Returns "" when nothing matches.
func FormatOfFile(name string) string {
	lower := strings.ToLower(name)
	for _, f := range KnownFormats() {
		if strings.HasSuffix(lower, "."+f) {
			return f
		}
	}
	return ""
}

// ConversionFormatsField builds the conversion_formats request object from a set of formats,
// dropping the always-generated ones (which are not valid keys there).
func ConversionFormatsField(formats []string) map[string]bool {
	field := map[string]bool{}
	for _, f := range formats {
		if conversionFormats[f] {
			field[f] = true
		}
	}
	return field
}
