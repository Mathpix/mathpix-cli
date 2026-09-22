package batch

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

// Report accumulates one Result per file and writes them as JSON lines under
// _mathpix/<run_id>/report.jsonl, so a folder run leaves an auditable record beside the outputs.
type Report struct {
	mu      sync.Mutex
	path    string
	results []Result
}

// NewReport creates the report directory under root and returns a Report, plus the run id.
func NewReport(root string, items []Item, m Map) (*Report, string, error) {
	runID := runID(root, items, m)
	dir := filepath.Join(root, "_mathpix", runID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, "", err
	}
	return &Report{path: filepath.Join(dir, "report.jsonl")}, runID, nil
}

// Add records one result and appends it to the report file.
func (r *Report) Add(result Result) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.results = append(r.results, result)
	line, err := json.Marshal(result)
	if err != nil {
		return
	}
	f, err := os.OpenFile(r.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	f.Write(append(line, '\n'))
}

// Path is where the report was written.
func (r *Report) Path() string { return r.path }

// runID derives a short, stable id from the input keys and the map, so re-running the same folder
// with the same map reuses the same report directory.
func runID(root string, items []Item, m Map) string {
	keys := make([]string, len(items))
	for i, it := range items {
		keys[i] = it.Key
	}
	sort.Strings(keys)
	mapKeys := make([]string, 0, len(m))
	for k, v := range m {
		mapKeys = append(mapKeys, k+"="+strings.Join(v, "+"))
	}
	sort.Strings(mapKeys)
	sum := sha256.Sum256([]byte(root + "\x00" + strings.Join(keys, "\x00") + "\x00" + strings.Join(mapKeys, "\x00")))
	return hex.EncodeToString(sum[:])[:12]
}
