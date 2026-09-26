// Package scsmigrate translates an SCS classic batch's option files into a /pco/v1/jobs body,
// field by field. Pure translation: no network. The output is meant for `mpx pco convert --job-spec`.
package scsmigrate

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// Inputs are the four things an SCS classic customer has on disk plus the policy flags.
type Inputs struct {
	ConversionOptionsPath string
	OCROptionsPath        string
	ExtList               string // comma-separated, e.g. ".lines.json,.lines.mmd.json"
	InputFolder           string
	OutputFolder          string
	MaxPDFPages           int
	PerPageTimeoutSeconds int
	MaxRetries            int
}

// Result is the job body and the notes about flags that have no PCO counterpart.
type Result struct {
	Spec  map[string]any
	Notes []string
}

// Translate builds the job spec.
func Translate(in Inputs) (*Result, error) {
	if in.InputFolder == "" {
		return nil, fmt.Errorf("--input-folder is required (the bucket and folder SCS classic publishes from, e.g. s3://bucket/folder/)")
	}
	result := &Result{}
	options := map[string]any{}
	if in.ConversionOptionsPath != "" {
		conversion, err := readJSONObject(in.ConversionOptionsPath)
		if err != nil {
			return nil, fmt.Errorf("--conversion-options: %w", err)
		}
		options["conversion_formats"] = conversion
	}
	if in.OCROptionsPath != "" {
		ocr, err := readJSONObject(in.OCROptionsPath)
		if err != nil {
			return nil, fmt.Errorf("--ocr-options: %w", err)
		}
		// SCS classic option files carry the endpoint as a top-level key; the v3/pdf block is
		// what PCO's options.ocr takes. A file without that shape is used as-is.
		if block, ok := ocr["v3/pdf"].(map[string]any); ok {
			ocr = block
		}
		for _, dropped := range []string{"streaming", "improve_mathpix", "webhook", "destination_url"} {
			if _, present := ocr[dropped]; present {
				delete(ocr, dropped)
				result.Notes = append(result.Notes, fmt.Sprintf("dropped ocr option %q: not supported by PCO", dropped))
			}
		}
		options["ocr"] = ocr
	}
	if strings.TrimSpace(in.ExtList) != "" {
		extras := []string{}
		for _, ext := range strings.Split(in.ExtList, ",") {
			ext = strings.TrimSpace(ext)
			if ext == "" {
				continue
			}
			if !strings.HasPrefix(ext, ".") {
				ext = "." + ext
			}
			if ext == ".lines.mmd.json" {
				result.Notes = append(result.Notes, "dropped .lines.mmd.json from --ext-list: identical to .lines.json, which PCO writes for every document")
				continue
			}
			extras = append(extras, ext)
		}
		if len(extras) > 0 {
			options["extra_outputs"] = extras
		}
	}
	output := in.OutputFolder
	if output == "" {
		output = in.InputFolder
	}
	spec := map[string]any{
		"input":   map[string]any{"folder": in.InputFolder},
		"output":  map[string]any{"folder": output, "layout": "alongside", "on_existing": "skip"},
		"options": options,
	}
	policy := map[string]any{}
	if in.MaxPDFPages > 0 {
		policy["max_pages_per_document"] = in.MaxPDFPages
	}
	if in.PerPageTimeoutSeconds > 0 {
		policy["per_page_timeout_seconds"] = in.PerPageTimeoutSeconds
	}
	if in.MaxRetries > 0 {
		policy["max_retries"] = in.MaxRetries
	}
	if len(policy) > 0 {
		spec["policy"] = policy
	}
	result.Notes = append(result.Notes,
		"SCS classic worker-count, job-id-hashing and credential flags have no PCO counterpart: the deployment sizes its pools, `alongside` never hashes, and storage credentials are the deployment's")
	result.Spec = spec
	return result, nil
}

func readJSONObject(path string) (map[string]any, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var object map[string]any
	if err := json.Unmarshal(raw, &object); err != nil {
		return nil, fmt.Errorf("%s is not a JSON object: %w", path, err)
	}
	return object, nil
}
