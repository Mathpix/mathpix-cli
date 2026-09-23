package selfupdate

import "testing"

func TestNewer(t *testing.T) {
	cases := []struct {
		current, candidate string
		want               bool
	}{
		{"0.1.1", "0.1.2", true},
		{"0.1.1", "0.2.0", true},
		{"0.9.9", "1.0.0", true},
		{"0.1.1", "0.1.1", false},
		{"0.2.0", "0.1.9", false},
		{"1.0.0", "0.9.9", false},
		{"0.1.1", "v0.2.0", true},
		{"0.1.1", "0.2.0-rc1", true},
		{"dev", "0.2.0", false},
		{"0.1.1", "garbage", false},
	}
	for _, c := range cases {
		if got := newer(c.current, c.candidate); got != c.want {
			t.Errorf("newer(%q, %q) = %v, want %v", c.current, c.candidate, got, c.want)
		}
	}
}

func TestChecksumFor(t *testing.T) {
	sums := "abc123  mpx_0.2.0_linux_amd64.tar.gz\ndef456  mpx_0.2.0_darwin_arm64.tar.gz\n"
	if got := checksumFor(sums, "mpx_0.2.0_darwin_arm64.tar.gz"); got != "def456" {
		t.Errorf("checksumFor = %q, want def456", got)
	}
	if got := checksumFor(sums, "mpx_0.2.0_windows_amd64.zip"); got != "" {
		t.Errorf("checksumFor for missing file = %q, want empty", got)
	}
}

func TestOptedOut(t *testing.T) {
	t.Setenv("MPX_CONFIG_DIR", t.TempDir())
	t.Setenv("MPX_NO_UPDATE", "")
	t.Setenv("MPX_AUTO_UPDATE", "")
	if optedOut() {
		t.Error("optedOut() = true with no opt-out set, want false")
	}
	t.Setenv("MPX_AUTO_UPDATE", "off")
	if !optedOut() {
		t.Error("optedOut() = false with MPX_AUTO_UPDATE=off, want true")
	}
	t.Setenv("MPX_AUTO_UPDATE", "")
	t.Setenv("MPX_NO_UPDATE", "1")
	if !optedOut() {
		t.Error("optedOut() = false with MPX_NO_UPDATE set, want true")
	}
}
