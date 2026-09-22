package pco

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
)

// imageInputExts are single images the synchronous /v3/text endpoint can OCR.
var imageInputExts = map[string]bool{
	"png": true, "jpg": true, "jpeg": true, "tif": true, "tiff": true, "webp": true, "gif": true, "bmp": true,
}

// textField maps a download extension to the /v3/text response field that holds it and the `formats`
// the request must ask for. An empty field means "write the whole JSON result". ok is false for an
// extension /v3/text cannot produce, so an image asked for those routes to /v3/pdf instead.
func textField(ext string) (field string, request []string, ok bool) {
	switch ext {
	case "mmd", "md", "txt":
		return "text", []string{"text"}, true
	case "html":
		return "html", []string{"text", "html"}, true
	case "tex":
		return "latex_styled", []string{"text", "latex_styled"}, true
	case "lines.json":
		return "", []string{"text", "data"}, true
	}
	return "", nil, false
}

func allTextNative(exts []string) bool {
	for _, e := range exts {
		if _, _, ok := textField(e); !ok {
			return false
		}
	}
	return len(exts) > 0
}

// convertImageOne OCRs one image through /v3/text and writes each requested text-style output.
func (e *pcoEngine) convertImageOne(ctx context.Context, path, outputBase string) ([]string, error) {
	requested := map[string]bool{}
	for _, ext := range e.extensions() {
		_, req, _ := textField(ext)
		for _, r := range req {
			requested[r] = true
		}
	}
	options := map[string]any{}
	for k, v := range e.options {
		options[k] = v
	}
	if len(requested) > 0 {
		formats := make([]string, 0, len(requested))
		for r := range requested {
			formats = append(formats, r)
		}
		options["formats"] = formats
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	result, err := e.client.TextImage(ctx, f, filepath.Base(path), options)
	f.Close()
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(outputBase), 0o755); err != nil {
		return nil, err
	}
	var written []string
	for _, ext := range e.extensions() {
		out := outputBase + "." + ext
		field, _, _ := textField(ext)
		var data []byte
		if field == "" {
			data, _ = json.MarshalIndent(result, "", "  ")
		} else if s, ok := result[field].(string); ok {
			data = []byte(s)
		}
		if err := os.WriteFile(out, data, 0o644); err != nil {
			return written, err
		}
		written = append(written, out)
	}
	return written, nil
}
