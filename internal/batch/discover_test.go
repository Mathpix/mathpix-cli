package batch

import (
	"reflect"
	"testing"
)

func TestParseMap(t *testing.T) {
	cases := []struct {
		in   string
		want Map
	}{
		{"pdf:docx,md:docx", Map{"pdf": {"docx"}, "md": {"docx"}}},
		{"pdf:docx+mmd, tiff:mmd", Map{"pdf": {"docx", "mmd"}, "tiff": {"mmd"}}},
		{".PDF:DOCX", Map{"pdf": {"docx"}}},
		{"", Map{}},
	}
	for _, c := range cases {
		got, err := ParseMap(c.in)
		if err != nil {
			t.Fatalf("ParseMap(%q): %v", c.in, err)
		}
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("ParseMap(%q) = %v, want %v", c.in, got, c.want)
		}
	}
	for _, bad := range []string{"pdf", "pdf:", ":docx"} {
		if _, err := ParseMap(bad); err == nil {
			t.Errorf("ParseMap(%q) expected error", bad)
		}
	}
}
