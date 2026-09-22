// Package pcoapi is the HTTP client for a Mathpix Private Cloud OCR deployment: one method per
// endpoint, typed request and response shapes, and nothing else. Everything the CLI does goes
// through here, so this package is also the reference client of the API.
package pcoapi

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

// SupportedAPIVersion is the /pco/v1/status api_version this client was written against. A
// deployment without the field predates the field and is treated as version 1.
const SupportedAPIVersion = 1

// Options configure a Client. Everything but Endpoint is pass-through for whatever sits in front
// of the deployment: the API itself has no authentication.
type Options struct {
	Endpoint   string
	Token      string
	AppID      string // hosted-API credentials, sent as the app_id and app_key headers
	AppKey     string
	CACert     string
	ClientCert string
	ClientKey  string
	Insecure   bool
	Version    string
	Timeout    time.Duration
}

// Client talks to one deployment.
type Client struct {
	BaseURL   string
	HTTP      *http.Client
	Token     string
	AppID     string
	AppKey    string
	UserAgent string
}

// New builds a Client from Options, loading certificates from disk when named.
func New(o Options) (*Client, error) {
	if o.Endpoint == "" {
		return nil, errors.New("no endpoint: pass --endpoint, set PCO_ENDPOINT, or run `pco config set endpoint URL`")
	}
	base := strings.TrimRight(o.Endpoint, "/")
	if !strings.HasPrefix(base, "http://") && !strings.HasPrefix(base, "https://") {
		base = "http://" + base
	}
	tlsConfig := &tls.Config{InsecureSkipVerify: o.Insecure} //nolint:gosec // opt-in flag for self-signed test deployments
	if o.CACert != "" {
		pem, err := os.ReadFile(o.CACert)
		if err != nil {
			return nil, fmt.Errorf("--ca-cert: %w", err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(pem) {
			return nil, fmt.Errorf("--ca-cert: %s holds no PEM certificate", o.CACert)
		}
		tlsConfig.RootCAs = pool
	}
	if o.ClientCert != "" || o.ClientKey != "" {
		cert, err := tls.LoadX509KeyPair(o.ClientCert, o.ClientKey)
		if err != nil {
			return nil, fmt.Errorf("--client-cert/--client-key: %w", err)
		}
		tlsConfig.Certificates = []tls.Certificate{cert}
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.TLSClientConfig = tlsConfig
	timeout := o.Timeout
	if timeout == 0 {
		timeout = 10 * time.Minute // uploads of large documents; polling calls are short anyway
	}
	version := o.Version
	if version == "" {
		version = "dev"
	}
	return &Client{BaseURL: base, HTTP: &http.Client{Transport: transport, Timeout: timeout}, Token: o.Token,
		AppID: o.AppID, AppKey: o.AppKey, UserAgent: "mpx-pco/" + version}, nil
}

// APIError is the deployment's error envelope: {"error": "...", "error_info": {"id", "message"}}.
type APIError struct {
	StatusCode int
	ID         string
	Message    string
	Body       string
}

func (e *APIError) Error() string {
	if e.ID != "" {
		return fmt.Sprintf("%s: %s (HTTP %d)", e.ID, e.Message, e.StatusCode)
	}
	if e.Message != "" {
		return fmt.Sprintf("%s (HTTP %d)", e.Message, e.StatusCode)
	}
	return fmt.Sprintf("HTTP %d: %s", e.StatusCode, strings.TrimSpace(e.Body))
}

// Transient says whether a failed call is worth retrying: connection trouble, a 5xx, or the
// deployment shedding load with 429.
func Transient(err error) bool {
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		return apiErr.StatusCode == http.StatusTooManyRequests || apiErr.StatusCode >= 500
	}
	return err != nil && !errors.Is(err, context.Canceled)
}

func (c *Client) newRequest(ctx context.Context, method, path string, query url.Values, body io.Reader, contentType string) (*http.Request, error) {
	target := c.BaseURL + path
	if len(query) > 0 {
		target += "?" + query.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, method, target, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", c.UserAgent)
	req.Header.Set("Accept", "application/json, */*")
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	if c.AppID != "" {
		req.Header.Set("app_id", c.AppID)
		req.Header.Set("app_key", c.AppKey)
	}
	return req, nil
}

func decodeError(resp *http.Response) error {
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	apiErr := &APIError{StatusCode: resp.StatusCode, Body: string(raw)}
	if resp.StatusCode == http.StatusNotFound && strings.HasPrefix(resp.Request.URL.Path, "/pco/v1/") {
		apiErr.ID, apiErr.Message = "not_a_pco_deployment", "this endpoint has no /pco/v1 routes; status, usage, jobs and cloud folders need a PCO deployment, the hosted API serves only /v3"
		return apiErr
	}
	var envelope struct {
		Error     string `json:"error"`
		ErrorInfo struct {
			ID      string `json:"id"`
			Message string `json:"message"`
		} `json:"error_info"`
	}
	if json.Unmarshal(raw, &envelope) == nil {
		apiErr.ID = envelope.ErrorInfo.ID
		apiErr.Message = envelope.ErrorInfo.Message
		if apiErr.Message == "" {
			apiErr.Message = envelope.Error
		}
	}
	return apiErr
}

func (c *Client) doJSON(ctx context.Context, method, path string, query url.Values, body any, out any) (int, error) {
	var reader io.Reader
	contentType := ""
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return 0, err
		}
		reader = bytes.NewReader(encoded)
		contentType = "application/json"
	}
	req, err := c.newRequest(ctx, method, path, query, reader, contentType)
	if err != nil {
		return 0, err
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return resp.StatusCode, decodeError(resp)
	}
	if out == nil {
		return resp.StatusCode, nil
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return resp.StatusCode, fmt.Errorf("%s %s: bad JSON in response: %w", method, path, err)
	}
	return resp.StatusCode, nil
}

// -- operator surface -----------------------------------------------------------------------

// Health is the GET /health body. The endpoint answers 503 with the same body when degraded.
type Health struct {
	Status                  string `json:"status"`
	RedisConnected          bool   `json:"redis_connected"`
	StorageConnected        bool   `json:"storage_connected"`
	GPUWorkersAlive         int    `json:"gpu_workers_alive"`
	GPUWorkersExpected      int    `json:"gpu_workers_expected"`
	DocumentWorkersAlive    int    `json:"document_workers_alive"`
	DocumentWorkersExpected int    `json:"document_workers_expected"`
	TextRequestsInFlight    int    `json:"text_requests_in_flight"`
	TextRequestsMax         int    `json:"text_requests_max"`
	License                 struct {
		Status         string  `json:"status"`
		LastVerifiedAt *string `json:"last_verified_at"`
	} `json:"license"`
}

// Health returns the body and the HTTP status (200 or 503).
func (c *Client) Health(ctx context.Context) (*Health, int, error) {
	req, err := c.newRequest(ctx, http.MethodGet, "/health", nil, nil, "")
	if err != nil {
		return nil, 0, err
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusServiceUnavailable {
		return nil, resp.StatusCode, decodeError(resp)
	}
	var health Health
	if err := json.NewDecoder(resp.Body).Decode(&health); err != nil {
		return nil, resp.StatusCode, fmt.Errorf("/health: bad JSON: %w", err)
	}
	return &health, resp.StatusCode, nil
}

// Status is the GET /pco/v1/status body.
type Status struct {
	OCRVersion       string          `json:"ocr_version"`
	PDFVersion       string          `json:"pdf_version"`
	APIVersion       int             `json:"api_version"`
	BuildID          string          `json:"build_id"`
	Customer         string          `json:"customer"`
	LicenseMode      string          `json:"license_mode"`
	MeteringMode     string          `json:"metering_mode"`
	DeploymentID     string          `json:"deployment_id"`
	UptimeSeconds    int             `json:"uptime_seconds"`
	QueueDepths      map[string]int  `json:"queue_depths"`
	Workers          map[string]int  `json:"workers"`
	RedisConnected   bool            `json:"redis_connected"`
	StorageConnected bool            `json:"storage_connected"`
	License          json.RawMessage `json:"license"`
	Metering         json.RawMessage `json:"metering"`
}

func (c *Client) Status(ctx context.Context) (*Status, error) {
	var status Status
	_, err := c.doJSON(ctx, http.MethodGet, "/pco/v1/status", nil, nil, &status)
	return &status, err
}

func (c *Client) License(ctx context.Context) (json.RawMessage, error) {
	var raw json.RawMessage
	_, err := c.doJSON(ctx, http.MethodGet, "/pco/v1/license", nil, nil, &raw)
	return raw, err
}

func usageQuery(from, to string) url.Values {
	query := url.Values{}
	if from != "" {
		query.Set("from", from)
	}
	if to != "" {
		query.Set("to", to)
	}
	return query
}

// Usage is GET /pco/v1/usage: {"from", "to", "days": [...], "totals": {...}}.
func (c *Client) Usage(ctx context.Context, from, to string) (json.RawMessage, error) {
	var raw json.RawMessage
	_, err := c.doJSON(ctx, http.MethodGet, "/pco/v1/usage", usageQuery(from, to), nil, &raw)
	return raw, err
}

// UsageExport is GET /pco/v1/usage/export, the statement an airgapped customer sends to Mathpix.
func (c *Client) UsageExport(ctx context.Context, from, to string) (json.RawMessage, error) {
	var raw json.RawMessage
	_, err := c.doJSON(ctx, http.MethodGet, "/pco/v1/usage/export", usageQuery(from, to), nil, &raw)
	return raw, err
}

// -- documents ------------------------------------------------------------------------------

// SubmitResponse is the POST /v3/pdf body: {"pdf_id", "status"}.
type SubmitResponse struct {
	PDFID  string `json:"pdf_id"`
	Status string `json:"status"`
}

// SubmitFile posts one document as multipart `file` + `options_json`, streaming from r.
func (c *Client) SubmitFile(ctx context.Context, r io.Reader, filename string, options map[string]any) (*SubmitResponse, error) {
	optionsJSON, err := json.Marshal(options)
	if err != nil {
		return nil, err
	}
	pipeReader, pipeWriter := io.Pipe()
	writer := multipart.NewWriter(pipeWriter)
	go func() {
		defer pipeWriter.Close()
		if err := writer.WriteField("options_json", string(optionsJSON)); err != nil {
			pipeWriter.CloseWithError(err)
			return
		}
		part, err := writer.CreateFormFile("file", filename)
		if err != nil {
			pipeWriter.CloseWithError(err)
			return
		}
		if _, err := io.Copy(part, r); err != nil {
			pipeWriter.CloseWithError(err)
			return
		}
		pipeWriter.CloseWithError(writer.Close())
	}()
	req, err := c.newRequest(ctx, http.MethodPost, "/v3/pdf", nil, pipeReader, writer.FormDataContentType())
	if err != nil {
		return nil, err
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return nil, decodeError(resp)
	}
	var submitted SubmitResponse
	if err := json.NewDecoder(resp.Body).Decode(&submitted); err != nil {
		return nil, fmt.Errorf("POST /v3/pdf: bad JSON: %w", err)
	}
	return &submitted, nil
}

// TextImage posts one image to POST /v3/text as multipart `file` + `options_json` and returns the
// response body as the deployment sent it. The call is synchronous on the server, so the body is
// the result: text, latex_styled, confidence, and whatever `formats` and `data_options` asked for.
func (c *Client) TextImage(ctx context.Context, r io.Reader, filename string, options map[string]any) (map[string]any, error) {
	optionsJSON, err := json.Marshal(options)
	if err != nil {
		return nil, err
	}
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	if err := writer.WriteField("options_json", string(optionsJSON)); err != nil {
		return nil, err
	}
	part, err := writer.CreateFormFile("file", filename)
	if err != nil {
		return nil, err
	}
	if _, err := io.Copy(part, r); err != nil {
		return nil, err
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}
	req, err := c.newRequest(ctx, http.MethodPost, "/v3/text", nil, &body, writer.FormDataContentType())
	if err != nil {
		return nil, err
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return nil, decodeError(resp)
	}
	var result map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("POST /v3/text: bad JSON: %w", err)
	}
	return result, nil
}

// SubmitURL posts a document the deployment fetches itself: JSON {"url": ..., options...}.
func (c *Client) SubmitURL(ctx context.Context, documentURL string, options map[string]any) (*SubmitResponse, error) {
	body := map[string]any{"url": documentURL}
	for key, value := range options {
		body[key] = value
	}
	var submitted SubmitResponse
	_, err := c.doJSON(ctx, http.MethodPost, "/v3/pdf", nil, body, &submitted)
	return &submitted, err
}

// ErrorInfo is a document-level error: {"id", "message"}.
type ErrorInfo struct {
	ID      string `json:"id"`
	Message string `json:"message"`
}

// ConversionStatus is the per-format state of a document's converted outputs. The hosted API
// sends {"docx": {"status": "completed"}}; PCO images before 2026-09-16 sent {"docx": "completed"}.
// Both decode to format -> status.
type ConversionStatus map[string]string

// UnmarshalJSON accepts either shape.
func (c *ConversionStatus) UnmarshalJSON(data []byte) error {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	result := ConversionStatus{}
	for format, value := range raw {
		var asString string
		if json.Unmarshal(value, &asString) == nil {
			result[format] = asString
			continue
		}
		var asObject struct {
			Status string `json:"status"`
		}
		if err := json.Unmarshal(value, &asObject); err != nil {
			return fmt.Errorf("conversion_status.%s: %w", format, err)
		}
		result[format] = asObject.Status
	}
	*c = result
	return nil
}

// DocumentStatus is the GET /v3/pdf/{id} body.
type DocumentStatus struct {
	PDFID             string           `json:"pdf_id"`
	Status            string           `json:"status"`
	Version           string           `json:"version"`
	InputFile         string           `json:"input_file"`
	NumPages          int              `json:"num_pages"`
	NumPagesCompleted int              `json:"num_pages_completed"`
	PercentDone       float64          `json:"percent_done"`
	ConversionStatus  ConversionStatus `json:"conversion_status"`
	ErrorInfo         *ErrorInfo       `json:"error_info"`
}

const (
	DocumentCompleted = "completed"
	DocumentError     = "error"
)

// Terminal says whether polling can stop.
func (d *DocumentStatus) Terminal() bool {
	return d.Status == DocumentCompleted || d.Status == DocumentError
}

func (c *Client) DocumentStatus(ctx context.Context, documentID string) (*DocumentStatus, error) {
	var status DocumentStatus
	_, err := c.doJSON(ctx, http.MethodGet, "/v3/pdf/"+url.PathEscape(documentID), nil, nil, &status)
	return &status, err
}

// DownloadOutput streams GET /v3/pdf/{id}.{ext} into w. pending is true with a Retry-After hint
// when the format is still converting (202); the caller waits and calls again.
func (c *Client) DownloadOutput(ctx context.Context, documentID, extension string, w io.Writer) (pending bool, retryAfter time.Duration, err error) {
	req, err := c.newRequest(ctx, http.MethodGet, "/v3/pdf/"+url.PathEscape(documentID)+"."+extension, nil, nil, "")
	if err != nil {
		return false, 0, err
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return false, 0, err
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusAccepted:
		seconds, _ := strconv.Atoi(resp.Header.Get("Retry-After"))
		if seconds <= 0 {
			seconds = 2
		}
		return true, time.Duration(seconds) * time.Second, nil
	case resp.StatusCode >= 400:
		return false, 0, decodeError(resp)
	}
	_, err = io.Copy(w, resp.Body)
	return false, 0, err
}

// -- batch jobs -----------------------------------------------------------------------------

// Job is the /pco/v1/jobs job object.
type Job struct {
	JobID           string            `json:"job_id"`
	Name            string            `json:"name"`
	Status          string            `json:"status"`
	CreatedAt       string            `json:"created_at"`
	StartedAt       *string           `json:"started_at"`
	FinishedAt      *string           `json:"finished_at"`
	Enumeration     map[string]any    `json:"enumeration"`
	Files           map[string]int    `json:"files"`
	Pages           map[string]int    `json:"pages"`
	Throughput      *float64          `json:"throughput_pages_per_minute"`
	ETASeconds      *int              `json:"eta_seconds"`
	Output          map[string]any    `json:"output"`
	Versions        map[string]string `json:"versions"`
	ClientReference string            `json:"client_reference"`
	Requeued        int               `json:"requeued"`
}

// JobTerminal lists the statuses after which a job does not change.
var JobTerminal = map[string]bool{"completed": true, "completed_with_failures": true, "cancelled": true, "error": true}

// Terminal says whether watching can stop.
func (j *Job) Terminal() bool { return JobTerminal[j.Status] }

// FileRecord is one file of a job: the listing item, the single-file read, and one report line.
type FileRecord struct {
	DocumentID   string            `json:"document_id"`
	Key          string            `json:"key"`
	Status       string            `json:"status"`
	Pages        int               `json:"pages"`
	PagesFailed  int               `json:"pages_failed"`
	Attempts     int               `json:"attempts"`
	ErrorID      *string           `json:"error_id"`
	ErrorMessage *string           `json:"error_message"`
	Outputs      map[string]string `json:"outputs"`
	StartedAt    *string           `json:"started_at"`
	FinishedAt   *string           `json:"finished_at"`
}

// CreateJob posts a job spec. With dryRun the deployment enumerates and estimates without
// creating anything and the raw estimate is returned. created is true for 201 (new job) and
// false for 200 (the same spec was posted before and the existing job is returned).
func (c *Client) CreateJob(ctx context.Context, spec map[string]any, dryRun bool) (raw json.RawMessage, created bool, err error) {
	query := url.Values{}
	if dryRun {
		query.Set("dry_run", "true")
	}
	code, err := c.doJSON(ctx, http.MethodPost, "/pco/v1/jobs", query, spec, &raw)
	return raw, code == http.StatusCreated, err
}

func (c *Client) GetJob(ctx context.Context, jobID string) (*Job, error) {
	var job Job
	_, err := c.doJSON(ctx, http.MethodGet, "/pco/v1/jobs/"+url.PathEscape(jobID), nil, nil, &job)
	return &job, err
}

// JobPage is one page of the jobs listing.
type JobPage struct {
	Jobs       []Job   `json:"jobs"`
	NextCursor *string `json:"next_cursor"`
}

func (c *Client) ListJobs(ctx context.Context, status, cursor string, limit int) (*JobPage, error) {
	query := url.Values{}
	if status != "" {
		query.Set("status", status)
	}
	if cursor != "" {
		query.Set("cursor", cursor)
	}
	if limit > 0 {
		query.Set("limit", strconv.Itoa(limit))
	}
	var page JobPage
	_, err := c.doJSON(ctx, http.MethodGet, "/pco/v1/jobs", query, nil, &page)
	return &page, err
}

// FilePage is one page of a job's file listing.
type FilePage struct {
	Files      []FileRecord `json:"files"`
	NextCursor *string      `json:"next_cursor"`
}

func (c *Client) ListFiles(ctx context.Context, jobID, status, cursor string, limit int) (*FilePage, error) {
	query := url.Values{}
	if status != "" {
		query.Set("status", status)
	}
	if cursor != "" {
		query.Set("cursor", cursor)
	}
	if limit > 0 {
		query.Set("limit", strconv.Itoa(limit))
	}
	var page FilePage
	_, err := c.doJSON(ctx, http.MethodGet, "/pco/v1/jobs/"+url.PathEscape(jobID)+"/files", query, nil, &page)
	return &page, err
}

// Report streams the job's NDJSON report into w, optionally one status only.
func (c *Client) Report(ctx context.Context, jobID, status string, w io.Writer) error {
	query := url.Values{}
	if status != "" {
		query.Set("status", status)
	}
	req, err := c.newRequest(ctx, http.MethodGet, "/pco/v1/jobs/"+url.PathEscape(jobID)+"/report", query, nil, "")
	if err != nil {
		return err
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return decodeError(resp)
	}
	_, err = io.Copy(w, resp.Body)
	return err
}

// RetryJob requeues the failed files of a finished job, all of them or only the given error ids.
func (c *Client) RetryJob(ctx context.Context, jobID string, errorIDs []string) (*Job, error) {
	body := map[string]any{}
	if len(errorIDs) > 0 {
		body["error_ids"] = errorIDs
	}
	var job Job
	_, err := c.doJSON(ctx, http.MethodPost, "/pco/v1/jobs/"+url.PathEscape(jobID)+"/retry", nil, body, &job)
	return &job, err
}

func (c *Client) CancelJob(ctx context.Context, jobID string) (*Job, error) {
	var job Job
	_, err := c.doJSON(ctx, http.MethodPost, "/pco/v1/jobs/"+url.PathEscape(jobID)+"/cancel", nil, map[string]any{}, &job)
	return &job, err
}
