package batch

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/mathpix/mathpix-cli/internal/pcoapi"
)

// Record is one report line. It is the deployment's own per-file record type, so the local and
// the server report decode with the same struct by construction.
type Record = pcoapi.FileRecord

const (
	StatusProcessing = "processing"
	StatusCompleted  = "completed"
	StatusFailed     = "failed"
	StatusSkipped    = "skipped"
)

// Report is `_mathpix/<run_id>/report.jsonl`. While a run is going it is append-only: a file's
// `processing` line lands the moment the deployment accepts it, so a crash or Ctrl-C leaves the
// document id on disk for --resume. When the run ends, Compact rewrites it to one line per file,
// the same shape as the deployment's own report.
type Report struct {
	Dir     string
	mu      sync.Mutex
	entries map[string]*Record
	file    *os.File
}

// OpenReport creates the run directory if needed and loads the existing lines.
func OpenReport(dir string) (*Report, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	report := &Report{Dir: dir, entries: map[string]*Record{}}
	reportPath := filepath.Join(dir, "report.jsonl")
	if existing, err := os.Open(reportPath); err == nil {
		scanner := bufio.NewScanner(existing)
		scanner.Buffer(make([]byte, 1<<20), 1<<20)
		for scanner.Scan() {
			var record Record
			if json.Unmarshal(scanner.Bytes(), &record) == nil && record.Key != "" {
				copied := record
				report.entries[record.Key] = &copied
			}
		}
		existing.Close()
	}
	file, err := os.OpenFile(reportPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, err
	}
	report.file = file
	return report, nil
}

// Path is the report file.
func (r *Report) Path() string { return filepath.Join(r.Dir, "report.jsonl") }

// Get returns the last recorded state of a key, or nil.
func (r *Report) Get(key string) *Record {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.entries[key]
}

// Len is the number of keys with an entry.
func (r *Report) Len() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.entries)
}

// Append writes one line and updates the in-memory state.
func (r *Report) Append(record *Record) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	line, err := json.Marshal(record)
	if err != nil {
		return err
	}
	if _, err := r.file.Write(append(line, '\n')); err != nil {
		return err
	}
	copied := *record
	r.entries[record.Key] = &copied
	return nil
}

// Compact rewrites the file as one line per key, in key order, from the in-memory state.
func (r *Report) Compact() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	keys := make([]string, 0, len(r.entries))
	for key := range r.entries {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	temp := r.Path() + ".tmp"
	file, err := os.Create(temp)
	if err != nil {
		return err
	}
	for _, key := range keys {
		line, err := json.Marshal(r.entries[key])
		if err != nil {
			file.Close()
			return err
		}
		if _, err := file.Write(append(line, '\n')); err != nil {
			file.Close()
			return err
		}
	}
	if err := file.Close(); err != nil {
		return err
	}
	if err := r.file.Close(); err != nil {
		return err
	}
	if err := os.Rename(temp, r.Path()); err != nil {
		return err
	}
	r.file, err = os.OpenFile(r.Path(), os.O_APPEND|os.O_WRONLY, 0o644)
	return err
}

// Close flushes the file.
func (r *Report) Close() error { return r.file.Close() }

// Manifest is `_mathpix/<run_id>/manifest.json`: what was asked for and how it ended.
type Manifest struct {
	RunID          string         `json:"run_id"`
	CreatedAt      string         `json:"created_at"`
	UpdatedAt      string         `json:"updated_at"`
	Endpoint       string         `json:"endpoint"`
	Inputs         []string       `json:"inputs"`
	OutDir         string         `json:"out_dir,omitempty"`
	Formats        []string       `json:"formats"`
	Include        []string       `json:"include,omitempty"`
	Exclude        []string       `json:"exclude,omitempty"`
	RequestOptions map[string]any `json:"request_options,omitempty"`
	Counts         map[string]int `json:"counts"`
}

// WriteManifest writes or rewrites manifest.json.
func (r *Report) WriteManifest(manifest *Manifest) error {
	manifest.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	if manifest.CreatedAt == "" {
		manifest.CreatedAt = manifest.UpdatedAt
	}
	raw, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(r.Dir, "manifest.json"), raw, 0o644)
}

// LoadManifest reads an existing manifest, or returns nil when there is none.
func LoadManifest(dir string) *Manifest {
	raw, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil {
		return nil
	}
	var manifest Manifest
	if json.Unmarshal(raw, &manifest) != nil {
		return nil
	}
	return &manifest
}
