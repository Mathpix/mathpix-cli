// Package scsapi is the typed HTTP client for the Mathpix OCR / document API (the "scs" service):
// POST /v3/pdf for documents, POST /v3/converter for Mathpix Markdown, POST /v3/text for one image,
// their status polls and their format downloads. It depends only on the standard library, so the
// binary carries no third-party HTTP stack, and it doubles as a reference client for the API.
package scsapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// Options construct a Client. AppID/AppKey are the hosted-API credentials sent as lowercase headers.
type Options struct {
	Endpoint  string
	AppID     string
	AppKey    string
	UserAgent string
	Timeout   time.Duration
}

// Client talks to one Mathpix API endpoint.
type Client struct {
	baseURL   string
	http      *http.Client
	appID     string
	appKey    string
	userAgent string
}

// New builds a Client. The default timeout is generous because a large upload streams through it.
func New(o Options) *Client {
	timeout := o.Timeout
	if timeout == 0 {
		timeout = 10 * time.Minute
	}
	base := o.Endpoint
	for len(base) > 0 && base[len(base)-1] == '/' {
		base = base[:len(base)-1]
	}
	ua := o.UserAgent
	if ua == "" {
		ua = "mpx"
	}
	return &Client{baseURL: base, http: &http.Client{Timeout: timeout}, appID: o.AppID, appKey: o.AppKey, userAgent: ua}
}

// APIError is a failed request, whether reported by an HTTP status or by the error envelope the API
// returns inside a 200 body.
type APIError struct {
	StatusCode int
	ID         string
	Message    string
}

func (e *APIError) Error() string {
	if e.ID != "" {
		return fmt.Sprintf("%s: %s", e.ID, e.Message)
	}
	if e.Message != "" {
		return e.Message
	}
	return fmt.Sprintf("request failed with status %d", e.StatusCode)
}

// Transient reports whether an error is worth retrying: rate limiting or a server-side failure.
func Transient(err error) bool {
	var apiErr *APIError
	if !asAPIError(err, &apiErr) {
		return true
	}
	return apiErr.StatusCode == http.StatusTooManyRequests || apiErr.StatusCode >= 500
}

// Status is the GET /v3/pdf/{id} body: the document's OCR progress and per-format conversion state.
type Status struct {
	PDFID            string                    `json:"pdf_id"`
	Status           string                    `json:"status"`
	NumPages         int                       `json:"num_pages"`
	NumPagesComplete int                       `json:"num_pages_completed"`
	PercentDone      float64                   `json:"percent_done"`
	ConversionStatus map[string]ConversionInfo `json:"conversion_status"`
	Error            string                    `json:"error"`
	ErrorInfo        *ErrorInfo                `json:"error_info"`
	DeletedAt        string                    `json:"deleted_at"`
}

// Terminal reports whether OCR has finished, one way or the other. Individual conversion formats may
// still be assembling after this is true.
func (s *Status) Terminal() bool { return s.Status == "completed" || s.Status == "error" }

// ConversionInfo is one format's state within conversion_status.
type ConversionInfo struct {
	Status    string     `json:"status"`
	ErrorInfo *ErrorInfo `json:"error_info"`
}

// ErrorInfo is the API's structured error; ID is the stable programmatic identifier.
type ErrorInfo struct {
	ID      string `json:"id"`
	Message string `json:"message"`
}

// SubmitFile uploads a local document to POST /v3/pdf and returns its pdf_id. The file streams
// through an io.Pipe so a large document is never held in memory.
func (c *Client) SubmitFile(ctx context.Context, r io.Reader, filename string, options map[string]any) (string, error) {
	optionsJSON, err := json.Marshal(options)
	if err != nil {
		return "", err
	}
	pr, pw := io.Pipe()
	writer := multipart.NewWriter(pw)
	go func() {
		if err := writer.WriteField("options_json", string(optionsJSON)); err != nil {
			pw.CloseWithError(err)
			return
		}
		part, err := writer.CreateFormFile("file", filename)
		if err != nil {
			pw.CloseWithError(err)
			return
		}
		if _, err := io.Copy(part, r); err != nil {
			pw.CloseWithError(err)
			return
		}
		pw.CloseWithError(writer.Close())
	}()
	var out struct {
		PDFID     string     `json:"pdf_id"`
		Error     string     `json:"error"`
		ErrorInfo *ErrorInfo `json:"error_info"`
	}
	if err := c.do(ctx, http.MethodPost, "/v3/pdf", pr, writer.FormDataContentType(), &out); err != nil {
		return "", err
	}
	if err := envelopeError(0, out.Error, out.ErrorInfo); err != nil {
		return "", err
	}
	return out.PDFID, nil
}

// SubmitURL submits a remote document to POST /v3/pdf by URL.
func (c *Client) SubmitURL(ctx context.Context, fileURL string, options map[string]any) (string, error) {
	body := map[string]any{"url": fileURL}
	for k, v := range options {
		body[k] = v
	}
	var out struct {
		PDFID     string     `json:"pdf_id"`
		Error     string     `json:"error"`
		ErrorInfo *ErrorInfo `json:"error_info"`
	}
	if err := c.doJSON(ctx, http.MethodPost, "/v3/pdf", body, &out); err != nil {
		return "", err
	}
	if err := envelopeError(0, out.Error, out.ErrorInfo); err != nil {
		return "", err
	}
	return out.PDFID, nil
}

// GetStatus polls GET /v3/pdf/{id}.
func (c *Client) GetStatus(ctx context.Context, pdfID string) (*Status, error) {
	var s Status
	if err := c.do(ctx, http.MethodGet, "/v3/pdf/"+url.PathEscape(pdfID), nil, "", &s); err != nil {
		return nil, err
	}
	return &s, nil
}

// Delete removes a document's outputs and input via DELETE /v3/pdf/{id}.
func (c *Client) Delete(ctx context.Context, pdfID string) (*Status, error) {
	var s Status
	if err := c.do(ctx, http.MethodDelete, "/v3/pdf/"+url.PathEscape(pdfID), nil, "", &s); err != nil {
		return nil, err
	}
	return &s, nil
}

// Download streams one output format to w. It reports pending=true with a Retry-After delay when the
// format is still converting (HTTP 202), so the caller can loop; a not-yet-written format (404 on a
// still-processing document) is reported the same way.
func (c *Client) Download(ctx context.Context, pdfID, ext string, w io.Writer) (pending bool, retryAfter time.Duration, err error) {
	return c.downloadFrom(ctx, "/v3/pdf/"+url.PathEscape(pdfID)+"."+ext, w)
}

// SubmitConverter converts Mathpix Markdown through POST /v3/converter and returns the conversion_id.
func (c *Client) SubmitConverter(ctx context.Context, mmd string, formats map[string]bool, options map[string]any) (string, error) {
	body := map[string]any{"mmd": mmd, "formats": formats}
	if len(options) > 0 {
		body["conversion_options"] = options
	}
	var out struct {
		ConversionID string     `json:"conversion_id"`
		Error        string     `json:"error"`
		ErrorInfo    *ErrorInfo `json:"error_info"`
	}
	if err := c.doJSON(ctx, http.MethodPost, "/v3/converter", body, &out); err != nil {
		return "", err
	}
	if err := envelopeError(0, out.Error, out.ErrorInfo); err != nil {
		return "", err
	}
	return out.ConversionID, nil
}

// GetConverterStatus polls GET /v3/converter/{id}.
func (c *Client) GetConverterStatus(ctx context.Context, id string) (*Status, error) {
	var s Status
	if err := c.do(ctx, http.MethodGet, "/v3/converter/"+url.PathEscape(id), nil, "", &s); err != nil {
		return nil, err
	}
	return &s, nil
}

// DownloadConverter streams one converted format for a /v3/converter job.
func (c *Client) DownloadConverter(ctx context.Context, id, ext string, w io.Writer) (pending bool, retryAfter time.Duration, err error) {
	return c.downloadFrom(ctx, "/v3/converter/"+url.PathEscape(id)+"."+ext, w)
}

// TextResult is the POST /v3/text body, returned raw so the caller can print it.
type TextResult struct {
	Raw json.RawMessage
}

// SubmitTextFile OCRs one local image through POST /v3/text and returns the raw JSON result.
func (c *Client) SubmitTextFile(ctx context.Context, r io.Reader, filename string, options map[string]any) (json.RawMessage, error) {
	optionsJSON, err := json.Marshal(options)
	if err != nil {
		return nil, err
	}
	pr, pw := io.Pipe()
	writer := multipart.NewWriter(pw)
	go func() {
		if err := writer.WriteField("options_json", string(optionsJSON)); err != nil {
			pw.CloseWithError(err)
			return
		}
		part, err := writer.CreateFormFile("file", filename)
		if err != nil {
			pw.CloseWithError(err)
			return
		}
		if _, err := io.Copy(part, r); err != nil {
			pw.CloseWithError(err)
			return
		}
		pw.CloseWithError(writer.Close())
	}()
	var raw json.RawMessage
	if err := c.do(ctx, http.MethodPost, "/v3/text", pr, writer.FormDataContentType(), &raw); err != nil {
		return nil, err
	}
	return raw, envelopeErrorRaw(raw)
}

func (c *Client) downloadFrom(ctx context.Context, path string, w io.Writer) (bool, time.Duration, error) {
	req, err := c.newRequest(ctx, http.MethodGet, path, nil, "")
	if err != nil {
		return false, 0, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return false, 0, err
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusAccepted, resp.StatusCode == http.StatusNotFound:
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

func (c *Client) do(ctx context.Context, method, path string, body io.Reader, contentType string, out any) error {
	req, err := c.newRequest(ctx, method, path, body, contentType)
	if err != nil {
		return err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return decodeError(resp)
	}
	if out == nil {
		io.Copy(io.Discard, resp.Body)
		return nil
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if raw, ok := out.(*json.RawMessage); ok {
		*raw = append((*raw)[:0], data...)
		return nil
	}
	return json.Unmarshal(data, out)
}

func (c *Client) doJSON(ctx context.Context, method, path string, body, out any) error {
	data, err := json.Marshal(body)
	if err != nil {
		return err
	}
	return c.do(ctx, method, path, bytes.NewReader(data), "application/json", out)
}

func (c *Client) newRequest(ctx context.Context, method, path string, body io.Reader, contentType string) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", c.userAgent)
	req.Header.Set("Accept", "application/json, */*")
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	if c.appID != "" {
		req.Header.Set("app_id", c.appID)
		req.Header.Set("app_key", c.appKey)
	}
	return req, nil
}

func decodeError(resp *http.Response) error {
	data, _ := io.ReadAll(resp.Body)
	var env struct {
		Error     string     `json:"error"`
		ErrorInfo *ErrorInfo `json:"error_info"`
	}
	json.Unmarshal(data, &env)
	e := &APIError{StatusCode: resp.StatusCode}
	if env.ErrorInfo != nil {
		e.ID = env.ErrorInfo.ID
		e.Message = env.ErrorInfo.Message
	}
	if e.Message == "" {
		e.Message = env.Error
	}
	return e
}

func envelopeError(status int, errStr string, info *ErrorInfo) error {
	if errStr == "" && info == nil {
		return nil
	}
	e := &APIError{StatusCode: status}
	if info != nil {
		e.ID = info.ID
		e.Message = info.Message
	}
	if e.Message == "" {
		e.Message = errStr
	}
	return e
}

func envelopeErrorRaw(raw json.RawMessage) error {
	var env struct {
		Error     string     `json:"error"`
		ErrorInfo *ErrorInfo `json:"error_info"`
	}
	json.Unmarshal(raw, &env)
	return envelopeError(0, env.Error, env.ErrorInfo)
}

func asAPIError(err error, target **APIError) bool {
	for err != nil {
		if e, ok := err.(*APIError); ok {
			*target = e
			return true
		}
		type unwrapper interface{ Unwrap() error }
		u, ok := err.(unwrapper)
		if !ok {
			return false
		}
		err = u.Unwrap()
	}
	return false
}
