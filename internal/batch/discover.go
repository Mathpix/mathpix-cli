// Package batch converts a folder of documents: it discovers the input files, maps each input
// extension to the output formats to produce, and runs the conversions concurrently with a per-file
// progress line and a JSONL report.
package batch

import (
	"fmt"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"
)

// documentInputExts go to /v3/pdf. mmdInputExts go to /v3/converter. These decide how a discovered
// file is submitted; the output format is decided by the Map.
var documentInputExts = map[string]bool{
	"pdf": true, "png": true, "jpg": true, "jpeg": true, "tif": true, "tiff": true,
	"webp": true, "gif": true, "bmp": true, "docx": true, "pptx": true, "xlsx": true, "epub": true,
}

var mmdInputExts = map[string]bool{"md": true, "mmd": true}

// Map records which output formats to produce for each input extension. It is built from the --map
// flag, e.g. "pdf:docx,md:docx" or "pdf:docx+md,tiff:mmd".
type Map map[string][]string

// ParseMap parses the --map value. Pairs are comma-separated; each pair is inputExt:format[+format].
// Extensions and formats are lowercased; a leading dot on the input extension is tolerated.
func ParseMap(value string) (Map, error) {
	m := Map{}
	value = strings.TrimSpace(value)
	if value == "" {
		return m, nil
	}
	for _, pair := range strings.Split(value, ",") {
		pair = strings.TrimSpace(pair)
		if pair == "" {
			continue
		}
		ext, formats, found := strings.Cut(pair, ":")
		if !found {
			return nil, fmt.Errorf("bad --map entry %q, expected inputExt:format", pair)
		}
		ext = strings.ToLower(strings.TrimPrefix(strings.TrimSpace(ext), "."))
		if ext == "" {
			return nil, fmt.Errorf("bad --map entry %q, empty input extension", pair)
		}
		for _, f := range strings.Split(formats, "+") {
			f = strings.ToLower(strings.TrimSpace(f))
			if f != "" {
				m[ext] = append(m[ext], f)
			}
		}
		if len(m[ext]) == 0 {
			return nil, fmt.Errorf("bad --map entry %q, no output format", pair)
		}
	}
	return m, nil
}

// Item is one file to convert.
type Item struct {
	Key        string   // slash-relative path under the input root, for display and the report
	Path       string   // absolute source path
	Ext        string   // lowercase input extension
	Formats    []string // output formats to produce
	OutputBase string   // output path without extension (under --out, or beside the input)
}

// IsMMDInput reports whether this item converts through /v3/converter rather than /v3/pdf.
func (it Item) IsMMDInput() bool { return mmdInputExts[it.Ext] }

// Discover walks inputDir and returns the items to convert. When m is empty, every supported input
// is converted to mmd. outDir, when set, is where outputs go (mirroring the input tree); otherwise
// outputs land beside their inputs.
func Discover(inputDir, outDir string, m Map) ([]Item, error) {
	root, err := filepath.Abs(inputDir)
	if err != nil {
		return nil, err
	}
	var items []Item
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == "_mathpix" || (strings.HasPrefix(d.Name(), ".") && path != root) {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasPrefix(d.Name(), ".") {
			return nil
		}
		ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(path), "."))
		if !documentInputExts[ext] && !mmdInputExts[ext] {
			return nil
		}
		formats := formatsFor(ext, m)
		if len(formats) == 0 {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		key := filepath.ToSlash(rel)
		base := strings.TrimSuffix(path, filepath.Ext(path))
		if outDir != "" {
			base = strings.TrimSuffix(filepath.Join(outDir, rel), filepath.Ext(rel))
		}
		items = append(items, Item{Key: key, Path: path, Ext: ext, Formats: formats, OutputBase: base})
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(items, func(i, j int) bool { return items[i].Key < items[j].Key })
	return items, nil
}

func formatsFor(ext string, m Map) []string {
	if len(m) == 0 {
		return []string{"mmd"}
	}
	return m[ext]
}
