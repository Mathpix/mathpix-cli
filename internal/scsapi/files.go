package scsapi

import (
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"time"
)

// This file adds the Files API (async, large-file and bucket-output document processing) and the
// account query endpoints (app tokens, past results, usage) to the client. The Files API is the same
// OCR product as /v3/pdf, in an async/bucket-output shape, so it lives in the same client under the
// scs service.

// getRaw performs a GET with optional query params and returns the raw JSON body.
func (c *Client) getRaw(ctx context.Context, path string, query url.Values) (json.RawMessage, error) {
	if len(query) > 0 {
		path += "?" + query.Encode()
	}
	var raw json.RawMessage
	if err := c.do(ctx, http.MethodGet, path, nil, "", &raw); err != nil {
		return nil, err
	}
	return raw, nil
}

// postRaw performs a JSON POST and returns the raw JSON body.
func (c *Client) postRaw(ctx context.Context, path string, body any) (json.RawMessage, error) {
	var raw json.RawMessage
	if err := c.doJSON(ctx, http.MethodPost, path, body, &raw); err != nil {
		return nil, err
	}
	return raw, nil
}

// FilesSubmitURI submits one document to POST /files/v1/uri by remote URI (https/s3/gs/azure).
func (c *Client) FilesSubmitURI(ctx context.Context, body map[string]any) (json.RawMessage, error) {
	return c.postRaw(ctx, "/files/v1/uri", body)
}

// FilesSubmitUpload uploads one local document to POST /files/v1 as multipart file + options_json,
// mirroring the /v3/pdf multipart shape.
func (c *Client) FilesSubmitUpload(ctx context.Context, r io.Reader, filename string, options map[string]any) (json.RawMessage, error) {
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
	if err := c.do(ctx, http.MethodPost, "/files/v1", pr, writer.FormDataContentType(), &raw); err != nil {
		return nil, err
	}
	return raw, nil
}

// FilesStatus polls GET /files/v1/{file_id}. The returned Status reuses the shared fields; the Files
// API reports per-format progress in a `formats` map rather than conversion_status.
func (c *Client) FilesStatus(ctx context.Context, fileID string) (json.RawMessage, error) {
	return c.getRaw(ctx, "/files/v1/"+url.PathEscape(fileID), nil)
}

// FilesDownload streams one output format for a file. A still-converting format returns 404
// format_not_ready, which downloadFrom treats as pending so the caller can retry.
func (c *Client) FilesDownload(ctx context.Context, fileID, ext string, w io.Writer) (pending bool, retryAfter time.Duration, err error) {
	return c.downloadFrom(ctx, "/files/v1/"+url.PathEscape(fileID)+"."+ext, w)
}

// FilesDelete removes a file and its Mathpix-stored results.
func (c *Client) FilesDelete(ctx context.Context, fileID string) (json.RawMessage, error) {
	var raw json.RawMessage
	if err := c.do(ctx, http.MethodDelete, "/files/v1/"+url.PathEscape(fileID), nil, "", &raw); err != nil {
		return nil, err
	}
	return raw, nil
}

// FilesCreateJob submits a batch of documents (POST /files/v1/jobs).
func (c *Client) FilesCreateJob(ctx context.Context, body map[string]any) (json.RawMessage, error) {
	return c.postRaw(ctx, "/files/v1/jobs", body)
}

// FilesListJobs lists jobs (GET /files/v1/jobs).
func (c *Client) FilesListJobs(ctx context.Context, query url.Values) (json.RawMessage, error) {
	return c.getRaw(ctx, "/files/v1/jobs", query)
}

// FilesGetJob polls one job (GET /files/v1/jobs/{id}).
func (c *Client) FilesGetJob(ctx context.Context, jobID string) (json.RawMessage, error) {
	return c.getRaw(ctx, "/files/v1/jobs/"+url.PathEscape(jobID), nil)
}

// FilesJobFiles lists the files in a job (GET /files/v1/jobs/{id}/files).
func (c *Client) FilesJobFiles(ctx context.Context, jobID string, query url.Values) (json.RawMessage, error) {
	return c.getRaw(ctx, "/files/v1/jobs/"+url.PathEscape(jobID)+"/files", query)
}

// FilesFinalizeJob closes a job to new files so job.completed can fire (POST /files/v1/jobs/{id}/finalize).
func (c *Client) FilesFinalizeJob(ctx context.Context, jobID string) (json.RawMessage, error) {
	return c.postRaw(ctx, "/files/v1/jobs/"+url.PathEscape(jobID)+"/finalize", map[string]any{})
}

// DataSourceCreate registers a bucket/container as a data source (POST /files/v1/data-sources).
func (c *Client) DataSourceCreate(ctx context.Context, body map[string]any) (json.RawMessage, error) {
	return c.postRaw(ctx, "/files/v1/data-sources", body)
}

// DataSourceList lists registered data sources (GET /files/v1/data-sources).
func (c *Client) DataSourceList(ctx context.Context) (json.RawMessage, error) {
	return c.getRaw(ctx, "/files/v1/data-sources", nil)
}

// DataSourceTest verifies access to a data source (POST /files/v1/data-sources/{id}/test).
func (c *Client) DataSourceTest(ctx context.Context, id string) (json.RawMessage, error) {
	return c.postRaw(ctx, "/files/v1/data-sources/"+url.PathEscape(id)+"/test", map[string]any{})
}

// DataSourceDelete removes a data source (DELETE /files/v1/data-sources/{id}).
func (c *Client) DataSourceDelete(ctx context.Context, id string) (json.RawMessage, error) {
	var raw json.RawMessage
	if err := c.do(ctx, http.MethodDelete, "/files/v1/data-sources/"+url.PathEscape(id), nil, "", &raw); err != nil {
		return nil, err
	}
	return raw, nil
}

// OnboardingIdentities returns Mathpix's grant identities and per-group external_id, to configure a
// bucket's trust policy before registering it (GET /files/v1/onboarding/identities).
func (c *Client) OnboardingIdentities(ctx context.Context) (json.RawMessage, error) {
	return c.getRaw(ctx, "/files/v1/onboarding/identities", nil)
}

// WebhookConfigGet fetches (creating on first call) the application signing secret used to verify
// webhook deliveries (GET /files/v1/webhook-config).
func (c *Client) WebhookConfigGet(ctx context.Context) (json.RawMessage, error) {
	return c.getRaw(ctx, "/files/v1/webhook-config", nil)
}

// WebhookSecretRotate rotates the signing secret (POST /files/v1/webhook-config/secret).
func (c *Client) WebhookSecretRotate(ctx context.Context, force bool) (json.RawMessage, error) {
	return c.postRaw(ctx, "/files/v1/webhook-config/secret", map[string]any{"force": force})
}

// WebhookTest sends one signed test notification to a URL (POST /files/v1/webhook-config/test).
func (c *Client) WebhookTest(ctx context.Context, callbackURL string, headers map[string]string) (json.RawMessage, error) {
	body := map[string]any{"callback_url": callbackURL}
	if len(headers) > 0 {
		body["callback_headers"] = headers
	}
	return c.postRaw(ctx, "/files/v1/webhook-config/test", body)
}

// CreateAppToken mints a short-lived app_token for direct client-side v3/text calls
// (POST /v3/app-tokens).
func (c *Client) CreateAppToken(ctx context.Context, body map[string]any) (json.RawMessage, error) {
	return c.postRaw(ctx, "/v3/app-tokens", body)
}

// GetOCRResults lists past image/stroke OCR results (GET /v3/ocr-results).
func (c *Client) GetOCRResults(ctx context.Context, query url.Values) (json.RawMessage, error) {
	return c.getRaw(ctx, "/v3/ocr-results", query)
}

// GetPDFResults lists past document (v3/pdf) results (GET /v3/pdf-results).
func (c *Client) GetPDFResults(ctx context.Context, query url.Values) (json.RawMessage, error) {
	return c.getRaw(ctx, "/v3/pdf-results", query)
}

// GetOCRUsage returns aggregated usage records for the group (GET /v3/ocr-usage).
func (c *Client) GetOCRUsage(ctx context.Context, query url.Values) (json.RawMessage, error) {
	return c.getRaw(ctx, "/v3/ocr-usage", query)
}
