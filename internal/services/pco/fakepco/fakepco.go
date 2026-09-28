// Package fakepco is an in-memory PCO deployment behind httptest, shaped like pco/service/http_api.py
// and batch_api.py, so the client and the batch engine are tested without a GPU. Documents
// complete after two status polls; a filename containing "bad" fails with a document error, one
// containing "flaky" fails its first submission with a 503; converted formats answer 202 once
// before 200.
package fakepco

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
)

type document struct {
	id, filename string
	pages, polls int
	formats      map[string]bool
	failed       bool
	served       map[string]int
}

type job struct {
	id    string
	spec  map[string]any
	polls int
	files []map[string]any
	state string
}

// Deployment is the fake. Fields are read by tests after a run.
type Deployment struct {
	Server    *httptest.Server
	mu        sync.Mutex
	documents map[string]*document
	jobs      map[string]*job
	Submits   int
	// TextRequests counts POST /v3/text calls.
	TextRequests int
	flakySeen    map[string]bool
	GPUWorkers   int
	APIVersion   int
	PagesPerDoc  int
}

// New starts the fake; call Close when done.
func New() *Deployment {
	d := &Deployment{documents: map[string]*document{}, jobs: map[string]*job{}, flakySeen: map[string]bool{},
		GPUWorkers: 1, APIVersion: 1, PagesPerDoc: 3}
	mux := http.NewServeMux()
	mux.HandleFunc("/health", d.health)
	mux.HandleFunc("/pco/v1/status", d.status)
	mux.HandleFunc("/pco/v1/license", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, 200, map[string]any{"status": "not_enforced", "license_mode": "airgapped", "deployment_id": "fake-1"})
	})
	mux.HandleFunc("/pco/v1/usage", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]any{"from": r.URL.Query().Get("from"), "to": r.URL.Query().Get("to"),
			"days":   []map[string]any{{"day": "2026-09-15", "pages_billable": 60, "images_billable": 0}},
			"totals": map[string]any{"pages_billable": 60, "images_billable": 0, "documents_billable": 1}})
	})
	mux.HandleFunc("/pco/v1/usage/export", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]any{"statement_version": 1, "deployment_id": "fake-1", "days": []any{}})
	})
	mux.HandleFunc("/v3/pdf", d.submit)
	mux.HandleFunc("/v3/text", d.text)
	mux.HandleFunc("/v3/pdf/", d.documentRoutes)
	mux.HandleFunc("/pco/v1/jobs", d.jobsCollection)
	mux.HandleFunc("/pco/v1/jobs/", d.jobRoutes)
	d.Server = httptest.NewServer(mux)
	return d
}

// URL is the deployment's base URL.
func (d *Deployment) URL() string { return d.Server.URL }

// Close stops the server.
func (d *Deployment) Close() { d.Server.Close() }

func writeJSON(w http.ResponseWriter, code int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(body)
}

func writeError(w http.ResponseWriter, code int, id, message string) {
	writeJSON(w, code, map[string]any{"error": message, "error_info": map[string]any{"id": id, "message": message}})
}

func (d *Deployment) health(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, 200, map[string]any{"status": "ok", "redis_connected": true, "storage_connected": true,
		"gpu_workers_alive": d.GPUWorkers, "gpu_workers_expected": d.GPUWorkers, "document_workers_alive": 2,
		"document_workers_expected": 2, "text_requests_in_flight": 0, "text_requests_max": 8 * d.GPUWorkers,
		"license": map[string]any{"status": "not_enforced", "last_verified_at": nil}})
}

func (d *Deployment) status(w http.ResponseWriter, _ *http.Request) {
	body := map[string]any{"ocr_version": "SuperNet-200", "pdf_version": "SuperNet-200p2", "build_id": "fake",
		"customer": "dev", "license_mode": "airgapped", "metering_mode": "unmetered", "deployment_id": "fake-1",
		"uptime_seconds": 10, "queue_depths": map[string]int{"documents": 0, "pages": 0, "jobs": 0},
		"workers":         map[string]int{"gpu_alive": d.GPUWorkers, "gpu_expected": d.GPUWorkers, "document_alive": 2, "document_expected": 2},
		"redis_connected": true, "storage_connected": true, "license": map[string]any{"status": "not_enforced"}}
	if d.APIVersion > 0 {
		body["api_version"] = d.APIVersion
	}
	writeJSON(w, 200, body)
}

func (d *Deployment) submit(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, 405, "method_not_allowed", "POST only")
		return
	}
	var filename string
	options := map[string]any{}
	if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data") {
		if err := r.ParseMultipartForm(64 << 20); err != nil {
			writeError(w, 400, "bad_request", err.Error())
			return
		}
		_, header, err := r.FormFile("file")
		if err != nil {
			writeError(w, 400, "pdf_missing", "No file uploaded and no url given")
			return
		}
		filename = header.Filename
		if raw := r.FormValue("options_json"); raw != "" {
			_ = json.Unmarshal([]byte(raw), &options)
		}
	} else {
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &options)
		url, _ := options["url"].(string)
		if url == "" {
			writeError(w, 400, "pdf_missing", "No file uploaded and no url given")
			return
		}
		filename = url[strings.LastIndex(url, "/")+1:]
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	d.Submits++
	if strings.Contains(filename, "flaky") && !d.flakySeen[filename] {
		d.flakySeen[filename] = true
		writeError(w, 503, "sys_exception", "transient")
		return
	}
	id := fmt.Sprintf("doc%04d", len(d.documents)+1)
	doc := &document{id: id, filename: filename, pages: d.PagesPerDoc, formats: map[string]bool{}, served: map[string]int{},
		failed: strings.Contains(filename, "bad")}
	if formats, ok := options["conversion_formats"].(map[string]any); ok {
		for name, enabled := range formats {
			if enabled == true {
				doc.formats[name] = true
			}
		}
	}
	d.documents[id] = doc
	writeJSON(w, 200, map[string]any{"pdf_id": id, "status": "processing"})
}

// text is POST /v3/text: one multipart image in, the hosted body out. A `region` wider than 1000
// is the deployment's refusal, so a test can see the error path.
func (d *Deployment) text(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, 405, "method_not_allowed", "POST only")
		return
	}
	if err := r.ParseMultipartForm(64 << 20); err != nil {
		writeError(w, 400, "bad_request", err.Error())
		return
	}
	_, header, err := r.FormFile("file")
	if err != nil {
		writeError(w, 400, "image_missing", "No image uploaded")
		return
	}
	options := map[string]any{}
	if raw := r.FormValue("options_json"); raw != "" {
		_ = json.Unmarshal([]byte(raw), &options)
	}
	if region, ok := options["region"].(map[string]any); ok {
		if width, _ := region["width"].(float64); width > 1000 {
			writeError(w, 400, "opts_value_out_of_range", "Region must not exceed image dimensions")
			return
		}
	}
	d.mu.Lock()
	d.TextRequests++
	d.mu.Unlock()
	writeJSON(w, 200, map[string]any{"request_id": "text0001", "version": "RSK-M133", "text": "fake text for " + header.Filename,
		"latex_styled": "x", "confidence": 0.9, "confidence_rate": 0.8, "is_printed": true, "is_handwritten": false})
}

var extensionByFormat = map[string]string{"md": "md", "html": "html", "latex": "tex", "docx": "docx", "pptx": "pptx",
	"xlsx": "xlsx", "tex.zip": "tex.zip", "md.zip": "md.zip", "mmd.zip": "mmd.zip", "html.zip": "html.zip"}

func (d *Deployment) documentRoutes(w http.ResponseWriter, r *http.Request) {
	tail := strings.TrimPrefix(r.URL.Path, "/v3/pdf/")
	id, ext := tail, ""
	if dot := strings.Index(tail, "."); dot > 0 {
		id, ext = tail[:dot], tail[dot+1:]
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	doc, ok := d.documents[id]
	if !ok {
		writeError(w, 404, "pdf_missing", "Unknown pdf_id")
		return
	}
	if ext == "" {
		doc.polls++
		body := map[string]any{"pdf_id": id, "version": "SuperNet-200p2", "input_file": doc.filename, "num_pages": doc.pages}
		switch {
		case doc.failed && doc.polls >= 2:
			body["status"], body["num_pages_completed"], body["percent_done"] = "error", 0, 0.0
			body["error_info"] = map[string]any{"id": "pdf_content_type", "message": "Unsupported document"}
		case doc.polls >= 2:
			body["status"], body["num_pages_completed"], body["percent_done"] = "completed", doc.pages, 100.0
			conversion := map[string]map[string]string{} // the hosted shape: an object per format
			for name := range doc.formats {
				conversion[name] = map[string]string{"status": "completed"}
			}
			body["conversion_status"] = conversion
		default:
			body["status"], body["num_pages_completed"], body["percent_done"] = "processing", doc.pages-1, 50.0
		}
		writeJSON(w, 200, body)
		return
	}
	if doc.polls < 2 || doc.failed {
		writeError(w, 404, "pdf_missing", "Output not ready")
		return
	}
	switch ext {
	case "mmd":
		w.WriteHeader(200)
		fmt.Fprintf(w, "# %s\n\ncontent\n", doc.filename)
	case "lines.json":
		writeJSON(w, 200, map[string]any{"pages": []map[string]any{{"page": 1, "lines": []any{}}}})
	default:
		format := ""
		for name, e := range extensionByFormat {
			if e == ext {
				format = name
			}
		}
		if format == "" || !doc.formats[format] {
			writeError(w, 415, "unsupported_format", fmt.Sprintf("Format .%s was not requested via conversion_formats or is not a supported output format", ext))
			return
		}
		doc.served[ext]++
		if doc.served[ext] == 1 {
			w.Header().Set("Retry-After", "0")
			writeJSON(w, 202, map[string]any{"pdf_id": id, "status": "completed", "conversion_status": map[string]string{format: "processing"}})
			return
		}
		w.WriteHeader(200)
		fmt.Fprintf(w, "%s output of %s", ext, doc.filename)
	}
}

func (d *Deployment) jobsCollection(w http.ResponseWriter, r *http.Request) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if r.Method == http.MethodGet {
		jobs := []map[string]any{}
		for _, j := range d.jobs {
			jobs = append(jobs, d.jobBody(j))
		}
		writeJSON(w, 200, map[string]any{"jobs": jobs, "next_cursor": nil})
		return
	}
	var spec map[string]any
	if err := json.NewDecoder(r.Body).Decode(&spec); err != nil {
		writeError(w, 400, "bad_request", "Request body is not JSON")
		return
	}
	input, _ := spec["input"].(map[string]any)
	folder, _ := input["folder"].(string)
	if folder == "" {
		writeError(w, 400, "bad_request", "input.folder is required")
		return
	}
	if r.URL.Query().Get("dry_run") == "true" {
		writeJSON(w, 200, map[string]any{"files_discovered": 3, "files_excluded": 1, "pages_estimated": 4, "by_type": map[string]int{"pdf": 2, "docx": 1}})
		return
	}
	id := "j_" + strconv.Itoa(len(d.jobs)+1)
	for _, existing := range d.jobs {
		if fmt.Sprint(existing.spec["input"]) == fmt.Sprint(spec["input"]) && fmt.Sprint(existing.spec["client_reference"]) == fmt.Sprint(spec["client_reference"]) {
			writeJSON(w, 200, d.jobBody(existing))
			return
		}
	}
	j := &job{id: id, spec: spec, state: "processing", files: []map[string]any{
		{"document_id": "a1", "key": "a.pdf", "status": "completed", "pages": 2, "pages_failed": 0, "attempts": 1, "error_id": nil, "error_message": nil, "outputs": map[string]string{"mmd": folder + "a.mmd"}, "started_at": nil, "finished_at": nil},
		{"document_id": "b1", "key": "b.pdf", "status": "completed", "pages": 2, "pages_failed": 0, "attempts": 1, "error_id": nil, "error_message": nil, "outputs": map[string]string{"mmd": folder + "b.mmd"}, "started_at": nil, "finished_at": nil},
		{"document_id": "c1", "key": "c.pdf", "status": "failed", "pages": 0, "pages_failed": 0, "attempts": 1, "error_id": "corrupt_pdf_file", "error_message": "cannot open", "outputs": map[string]string{}, "started_at": nil, "finished_at": nil},
	}}
	d.jobs[id] = j
	writeJSON(w, 201, d.jobBody(j))
}

func (d *Deployment) jobBody(j *job) map[string]any {
	completed, failed := 0, 0
	for _, f := range j.files {
		switch f["status"] {
		case "completed":
			completed++
		case "failed":
			failed++
		}
	}
	done := completed + failed
	if j.polls >= 2 && j.state == "processing" {
		if failed > 0 {
			j.state = "completed_with_failures"
		} else {
			j.state = "completed"
		}
	}
	shown := done
	if j.state == "processing" {
		shown = j.polls
		if shown > done {
			shown = done
		}
	}
	return map[string]any{"job_id": j.id, "name": j.spec["name"], "status": j.state, "created_at": "2026-09-15T00:00:00Z",
		"files":                       map[string]int{"discovered": len(j.files), "queued": len(j.files) - shown, "processing": 0, "completed": minInt(shown, completed), "failed": maxInt(0, shown-completed), "skipped": 0},
		"pages":                       map[string]int{"completed": 2 * minInt(shown, completed), "failed": 0},
		"throughput_pages_per_minute": 17.2, "eta_seconds": nil,
		"output":   map[string]any{"folder": j.spec["input"].(map[string]any)["folder"], "layout": "alongside"},
		"versions": map[string]string{"ocr_version": "SuperNet-200", "pdf_version": "SuperNet-200p2"}}
}

func (d *Deployment) jobRoutes(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/pco/v1/jobs/"), "/")
	d.mu.Lock()
	defer d.mu.Unlock()
	j, ok := d.jobs[parts[0]]
	if !ok {
		writeError(w, 404, "not_found", "Unknown job "+parts[0])
		return
	}
	if len(parts) == 1 {
		j.polls++
		writeJSON(w, 200, d.jobBody(j))
		return
	}
	switch parts[1] {
	case "files":
		status := r.URL.Query().Get("status")
		files := []map[string]any{}
		for _, f := range j.files {
			if status == "" || f["status"] == status {
				files = append(files, f)
			}
		}
		writeJSON(w, 200, map[string]any{"files": files, "next_cursor": nil})
	case "report":
		w.Header().Set("Content-Type", "application/x-ndjson")
		for _, f := range j.files {
			line, _ := json.Marshal(f)
			w.Write(append(line, '\n'))
		}
	case "retry":
		var body struct {
			ErrorIDs []string `json:"error_ids"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		requeued := 0
		for _, f := range j.files {
			if f["status"] == "failed" && (len(body.ErrorIDs) == 0 || contains(body.ErrorIDs, f["error_id"].(string))) {
				f["status"], f["error_id"], f["error_message"], f["pages"] = "completed", nil, nil, 1
				requeued++
			}
		}
		j.state, j.polls = "processing", 0
		response := d.jobBody(j)
		response["requeued"] = requeued
		writeJSON(w, 200, response)
	case "cancel":
		j.state = "cancelled"
		writeJSON(w, 200, d.jobBody(j))
	default:
		writeError(w, 404, "not_found", "unknown route")
	}
}

func contains(list []string, value string) bool {
	for _, item := range list {
		if item == value {
			return true
		}
	}
	return false
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
