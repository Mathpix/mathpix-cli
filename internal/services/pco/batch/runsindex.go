package batch

import (
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mathpix/mathpix-cli/internal/config"
)

// The runs index remembers where each local run's record lives, one small file per run id under
// the mpx config directory, so `mpx pco convert --resume RUN_ID` needs nothing else on the machine
// that ran it. It is a convenience cache: a failure here only costs the shortcut, never the run.

// runIndexMaxAge bounds the index: the deployment keeps a document's status for 7 days, so an
// older run cannot be resumed and its entry is dead weight.
const runIndexMaxAge = 7 * 24 * time.Hour

func runsDir() (string, error) {
	dir, err := config.Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "pco-runs"), nil
}

// rememberRun writes the record directory of a run under its id and prunes entries that can no
// longer serve a resume: older than runIndexMaxAge, or pointing at a record directory that is gone.
func rememberRun(runID, recordDir string) {
	dir, err := runsDir()
	if err != nil {
		return
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return
	}
	pruneRuns(dir)
	_ = os.WriteFile(filepath.Join(dir, runID), []byte(recordDir+"\n"), 0o600)
}

// forgetRun removes a run's entry once nothing is left to fetch, so the index holds only runs that
// still have documents processing.
func forgetRun(runID string) {
	dir, err := runsDir()
	if err != nil {
		return
	}
	_ = os.Remove(filepath.Join(dir, runID))
}

func pruneRuns(dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		path := filepath.Join(dir, entry.Name())
		info, err := entry.Info()
		if err != nil {
			continue
		}
		raw, _ := os.ReadFile(path)
		recordDir := strings.TrimSpace(string(raw))
		if time.Since(info.ModTime()) > runIndexMaxAge || recordDir == "" || !dirExists(recordDir) {
			_ = os.Remove(path)
		}
	}
}

func dirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

// recordDirOf returns the remembered record directory of a run id, or "" when unknown here.
func recordDirOf(runID string) string {
	dir, err := runsDir()
	if err != nil {
		return ""
	}
	raw, err := os.ReadFile(filepath.Join(dir, runID))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(raw))
}
