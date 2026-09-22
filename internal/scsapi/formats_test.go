package scsapi

import "testing"

func TestFormatOfFile(t *testing.T) {
	cases := map[string]string{
		"paper.mmd":            "mmd",
		"paper.tex.zip":        "tex.zip",
		"scan.lines.json":      "lines.json",
		"out.docx":             "docx",
		"book.mmd.overlay.pdf": "mmd.overlay.pdf",
		"note.txt":             "",
	}
	for name, want := range cases {
		if got := FormatOfFile(name); got != want {
			t.Errorf("FormatOfFile(%q) = %q, want %q", name, got, want)
		}
	}
}

func TestConversionFormatsField(t *testing.T) {
	field := ConversionFormatsField([]string{"docx", "mmd", "tex.zip", "lines.json"})
	if !field["docx"] || !field["tex.zip"] {
		t.Errorf("expected docx and tex.zip in %v", field)
	}
	if field["mmd"] || field["lines.json"] {
		t.Errorf("always-generated formats must not appear in conversion_formats: %v", field)
	}
}
