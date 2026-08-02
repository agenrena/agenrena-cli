package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"
)

type APIClient struct {
	baseURL    string
	apiKey     string
	httpClient *http.Client
}

func newAPIClient(creds *Credentials) *APIClient {
	return &APIClient{
		baseURL: strings.TrimRight(apiBaseFromEnv(), "/"),
		apiKey:  creds.APIKey,
		httpClient: &http.Client{
			Timeout: 60 * time.Second,
		},
	}
}

func (c *APIClient) get(ctx context.Context, endpoint string, out any) error {
	return c.doJSON(ctx, http.MethodGet, endpoint, nil, out)
}

func (c *APIClient) post(ctx context.Context, endpoint string, body any, out any) error {
	return c.doJSON(ctx, http.MethodPost, endpoint, body, out)
}

func (c *APIClient) patch(ctx context.Context, endpoint string, body any, out any) error {
	return c.doJSON(ctx, http.MethodPatch, endpoint, body, out)
}

func (c *APIClient) put(ctx context.Context, endpoint string, body any, out any) error {
	return c.doJSON(ctx, http.MethodPut, endpoint, body, out)
}

func (c *APIClient) delete(ctx context.Context, endpoint string, body any, out any) error {
	return c.doJSON(ctx, http.MethodDelete, endpoint, body, out)
}

func (c *APIClient) doJSON(ctx context.Context, method, endpoint string, body any, out any) error {
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(raw)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.endpointURL(endpoint), reader)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "agenrena-cli/"+cliVersion)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return wrapError("NETWORK_ERROR", "request failed", err)
	}
	defer resp.Body.Close()
	return decodeAPIResponse(resp, out)
}

func (c *APIClient) endpointURL(endpoint string) string {
	base, err := url.Parse(c.baseURL)
	if err != nil {
		return c.baseURL + "/" + strings.TrimLeft(endpoint, "/")
	}
	relative, err := url.Parse(endpoint)
	if err != nil {
		return c.baseURL + "/" + strings.TrimLeft(endpoint, "/")
	}
	joined := path.Join(base.Path, strings.TrimLeft(relative.Path, "/"))
	if strings.HasSuffix(relative.Path, "/") && !strings.HasSuffix(joined, "/") {
		joined += "/"
	}
	base.Path = joined
	base.RawQuery = relative.RawQuery
	base.Fragment = relative.Fragment
	return base.String()
}

func decodeAPIResponse(resp *http.Response, out any) error {
	raw, readErr := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		if readErr != nil {
			return readErr
		}
		return decodeAPIError(resp, raw)
	}
	if readErr != nil {
		return readErr
	}
	if out == nil || len(raw) == 0 {
		return nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return wrapError("API_RESPONSE_INVALID", "failed to parse API response", err)
	}
	return nil
}

func decodeAPIError(resp *http.Response, raw []byte) error {
	code := fmt.Sprintf("HTTP_%d", resp.StatusCode)
	message := strings.TrimSpace(string(raw))
	if message == "" {
		message = resp.Status
	}

	var params any
	var fields any
	var apiBody struct {
		Code    string          `json:"code"`
		Detail  string          `json:"detail"`
		Message string          `json:"message"`
		Error   json.RawMessage `json:"error"`
	}
	if json.Unmarshal(raw, &apiBody) == nil {
		if apiBody.Code != "" {
			code = apiBody.Code
		}
		if apiBody.Detail != "" {
			message = apiBody.Detail
		} else if apiBody.Message != "" {
			message = apiBody.Message
		}

		if len(apiBody.Error) > 0 && string(apiBody.Error) != "null" {
			var nested struct {
				Code    string `json:"code"`
				Message string `json:"message"`
				Params  any    `json:"params"`
				Fields  any    `json:"fields"`
			}
			if json.Unmarshal(apiBody.Error, &nested) == nil {
				if nested.Code != "" {
					code = nested.Code
				}
				if nested.Message != "" {
					message = nested.Message
				} else if nested.Code != "" && apiBody.Detail == "" && apiBody.Message == "" {
					message = nested.Code
				}
				params = nested.Params
				fields = nested.Fields
			} else {
				var legacyMessage string
				if json.Unmarshal(apiBody.Error, &legacyMessage) == nil && legacyMessage != "" {
					message = legacyMessage
				}
			}
		} else if apiBody.Code != "" && apiBody.Detail == "" && apiBody.Message == "" {
			message = apiBody.Code
		}
	}

	return &cliError{
		Code:        code,
		Message:     message,
		Recoverable: resp.StatusCode == 409 || resp.StatusCode == 429 || resp.StatusCode >= 500,
		Params:      params,
		Fields:      fields,
	}
}

func uploadMultipart(ctx context.Context, uploadURL string, fields map[string]string, fileField, fileName, contentType string, content []byte) error {
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	for k, v := range fields {
		if err := writer.WriteField(k, v); err != nil {
			return err
		}
	}
	part, err := writer.CreateFormFile(fileField, fileName)
	if err != nil {
		return err
	}
	if _, err := part.Write(content); err != nil {
		return err
	}
	if err := writer.Close(); err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, uploadURL, &body)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("User-Agent", "agenrena-cli/"+cliVersion)

	client := &http.Client{Timeout: 90 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return wrapError("UPLOAD_FAILED", "upload request failed", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		message := strings.TrimSpace(string(raw))
		if message == "" {
			message = resp.Status
		}
		return apiError(fmt.Sprintf("UPLOAD_HTTP_%d", resp.StatusCode), message, resp.StatusCode >= 500)
	}
	return nil
}

func uploadPUT(ctx context.Context, uploadURL string, contentType string, content []byte) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, uploadURL, bytes.NewReader(content))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("User-Agent", "agenrena-cli/"+cliVersion)

	client := &http.Client{Timeout: 90 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return wrapError("UPLOAD_FAILED", "upload request failed", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		message := strings.TrimSpace(string(raw))
		if message == "" {
			message = resp.Status
		}
		return apiError(fmt.Sprintf("UPLOAD_HTTP_%d", resp.StatusCode), message, resp.StatusCode >= 500)
	}
	return nil
}
