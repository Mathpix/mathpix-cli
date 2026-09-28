package batch

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Options are the run's inputs and knobs, straight from the convert flags.
type Options struct {
	Inputs         []string
	OutDir         string
	Formats        []string       // canonical conversion_formats names
	RequestOptions map[string]any // other options_json fields, passed through unchanged
	Concurrency    int
	Include        []string
	Exclude        []string
	Detach         bool
	DryRun         bool
	// Overwrite reprocesses everything: a fresh run record, outputs rewritten in place, no skips.
	// For a new image or model on the deployment.
	Overwrite bool
	// Resume is "" (a record of this same command is used if present), ResumeSameCommand
	// (the same, but refuse when there is none), or a run id whose manifest supplies OutDir,
	// Formats, Include, Exclude and RequestOptions.
	Resume string
}

// ResumeSameCommand is the value of a bare --resume.
const ResumeSameCommand = "same-command"

// Item is one document to convert: its key in the report, where it is, and where its outputs go
// (a path without extension; `.mmd`, `.lines.json` and the formats are appended).
type Item struct {
	Key        string
	Path       string
	OutputBase string
}

// DocumentExtensions are what the deployment accepts (docs.mathpix.com, Private Cloud OCR overview).
var DocumentExtensions = map[string]bool{
	".pdf": true, ".docx": true, ".doc": true, ".odt": true, ".pptx": true, ".ppt": true, ".xlsx": true, ".xls": true,
	".epub": true, ".mobi": true, ".azw": true, ".azw3": true, ".tiff": true, ".tif": true,
	".png": true, ".jpg": true, ".jpeg": true, ".gif": true, ".bmp": true, ".webp": true,
}

// RunDirName is the folder under the output root that holds the report and manifest, the same
// name the deployment uses in a bucket.
const RunDirName = "_mathpix"

// Discover walks the inputs and returns the items in stable order plus the output root, the
// directory the run record lives under.
func Discover(opts Options) ([]Item, string, error) {
	if len(opts.Inputs) == 0 {
		return nil, "", fmt.Errorf("nothing to convert: the run record names no inputs")
	}
	absInputs := make([]string, 0, len(opts.Inputs))
	for _, input := range opts.Inputs {
		abs, err := filepath.Abs(input)
		if err != nil {
			return nil, "", err
		}
		if _, err := os.Stat(abs); err != nil {
			return nil, "", err
		}
		absInputs = append(absInputs, abs)
	}
	root := opts.OutDir
	if root == "" {
		if info, _ := os.Stat(absInputs[0]); info != nil && info.IsDir() {
			root = absInputs[0]
		} else {
			root = filepath.Dir(absInputs[0])
		}
	}
	root, err := filepath.Abs(root)
	if err != nil {
		return nil, "", err
	}
	var items []Item
	for _, input := range absInputs {
		info, err := os.Stat(input)
		if err != nil {
			return nil, "", err
		}
		if !info.IsDir() {
			key := filepath.Base(input)
			if selected(key, opts) {
				items = append(items, Item{Key: key, Path: input, OutputBase: outputBase(opts.OutDir, input, key)})
			}
			continue
		}
		err = filepath.WalkDir(input, func(current string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			name := entry.Name()
			if entry.IsDir() {
				if current != input && (name == RunDirName || strings.HasPrefix(name, ".")) {
					return filepath.SkipDir
				}
				return nil
			}
			if strings.HasPrefix(name, ".") || !DocumentExtensions[strings.ToLower(filepath.Ext(name))] {
				return nil
			}
			rel, err := filepath.Rel(input, current)
			if err != nil {
				return err
			}
			key := filepath.ToSlash(rel)
			if len(absInputs) > 1 {
				key = filepath.Base(input) + "/" + key
			}
			if selected(key, opts) {
				items = append(items, Item{Key: key, Path: current, OutputBase: outputBase(opts.OutDir, current, key)})
			}
			return nil
		})
		if err != nil {
			return nil, "", err
		}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Key < items[j].Key })
	return items, root, nil
}

func outputBase(outDir, filePath, key string) string {
	if outDir == "" {
		return strings.TrimSuffix(filePath, filepath.Ext(filePath))
	}
	return filepath.Join(outDir, strings.TrimSuffix(filepath.FromSlash(key), filepath.Ext(key)))
}

func selected(key string, opts Options) bool {
	base := path.Base(key)
	matches := func(patterns []string) bool {
		for _, pattern := range patterns {
			if ok, _ := path.Match(pattern, key); ok {
				return true
			}
			if ok, _ := path.Match(pattern, base); ok {
				return true
			}
		}
		return false
	}
	if len(opts.Include) > 0 && !matches(opts.Include) {
		return false
	}
	return !matches(opts.Exclude)
}

// RunID names the run record: a hash of what was asked for, so the same command finds the same
// record and `--resume` needs no id, the way the deployment derives a job id from its spec.
func RunID(opts Options, absInputs []string) string {
	sorted := append([]string(nil), absInputs...)
	sort.Strings(sorted)
	outDir := opts.OutDir
	if outDir != "" {
		outDir, _ = filepath.Abs(outDir)
	}
	payload := map[string]any{"inputs": sorted, "out": outDir, "formats": opts.Formats, "options": opts.RequestOptions,
		"include": opts.Include, "exclude": opts.Exclude}
	if opts.Overwrite {
		payload["overwrite_at"] = time.Now().UnixNano() // a new record every time; nothing from before is reused
	}
	encoded, _ := json.Marshal(payload)
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:])[:12]
}
