package progress_test

import (
	"bytes"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mathpix/mathpix-cli/internal/progress"
)

func TestIndicatorDisabledIsSilent(t *testing.T) {
	var buf bytes.Buffer
	ind := progress.NewIndicator(&buf, false)
	ind.Start("converting paper.pdf")
	ind.Set(50, "1/2 pages")
	ind.Stop()
	if buf.Len() != 0 {
		t.Errorf("disabled indicator wrote %q, want nothing", buf.String())
	}
}

func TestIndicatorEnabledRendersBar(t *testing.T) {
	w := &safeBuffer{}
	ind := progress.NewIndicator(w, true)
	ind.Start("converting paper.pdf")
	ind.Set(50, "3/6 pages")
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && !strings.Contains(w.String(), "50%") {
		time.Sleep(10 * time.Millisecond)
	}
	ind.Stop()
	out := w.String()
	for _, want := range []string{"converting paper.pdf", "50%", "3/6 pages"} {
		if !strings.Contains(out, want) {
			t.Errorf("rendered output %q missing %q", out, want)
		}
	}
}

type safeBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *safeBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *safeBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}
