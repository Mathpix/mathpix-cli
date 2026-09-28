package scsmigrate_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/mathpix/mathpix-cli/internal/scsmigrate"
)

func TestTranslateProducesTheSection48Spec(t *testing.T) {
	dir := t.TempDir()
	conversion := filepath.Join(dir, "conversion_options.json")
	ocr := filepath.Join(dir, "ocr_options.json")
	os.WriteFile(conversion, []byte(`{"md": true, "docx": true}`), 0o644)
	os.WriteFile(ocr, []byte(`{"v3/pdf": {"math_inline_delimiters": ["\\(", "\\)"], "include_smiles": true, "streaming": true}}`), 0o644)
	result, err := scsmigrate.Translate(scsmigrate.Inputs{ConversionOptionsPath: conversion, OCROptionsPath: ocr,
		ExtList: ".lines.json,.lines.mmd.json", InputFolder: "s3://acme-docs/batch1/", MaxPDFPages: 5000, PerPageTimeoutSeconds: 15, MaxRetries: 2})
	if err != nil {
		t.Fatal(err)
	}
	got, _ := json.Marshal(result.Spec)
	want := `{"input":{"folder":"s3://acme-docs/batch1/"},"options":{"conversion_formats":{"docx":true,"md":true},"extra_outputs":[".lines.json"],"ocr":{"include_smiles":true,"math_inline_delimiters":["\\(","\\)"]}},"output":{"folder":"s3://acme-docs/batch1/","layout":"alongside","on_existing":"skip"},"policy":{"max_pages_per_document":5000,"max_retries":2,"per_page_timeout_seconds":15}}`
	if string(got) != want {
		t.Fatalf("spec\n got %s\nwant %s", got, want)
	}
	if len(result.Notes) != 3 {
		t.Fatalf("expected notes for streaming, .lines.mmd.json and the dropped flags, got %v", result.Notes)
	}
	if _, err := scsmigrate.Translate(scsmigrate.Inputs{}); err == nil {
		t.Fatal("missing input folder must be an error")
	}
}
