package tools

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// HTTPRequestTool allows the agent to make HTTP requests.
type HTTPRequestTool struct{}

func NewHTTPRequestTool() *HTTPRequestTool {
	return &HTTPRequestTool{}
}

func (t *HTTPRequestTool) Name() string { return "http_request" }

func (t *HTTPRequestTool) Description() string {
	return "Make HTTP requests (GET, POST, PUT, DELETE, PATCH). Useful for calling APIs, checking endpoints, or fetching data. Localhost and private IPs are blocked for security."
}

func (t *HTTPRequestTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"method": map[string]interface{}{
				"type":        "string",
				"enum":        []string{"GET", "POST", "PUT", "DELETE", "PATCH"},
				"description": "HTTP method",
			},
			"url": map[string]interface{}{
				"type":        "string",
				"description": "The URL to request",
			},
			"headers": map[string]interface{}{
				"type":        "object",
				"description": "Optional HTTP headers as key-value pairs",
			},
			"body": map[string]interface{}{
				"type":        "string",
				"description": "Optional request body (for POST/PUT/PATCH)",
			},
			"timeout": map[string]interface{}{
				"type":        "integer",
				"description": "Request timeout in seconds (default 30, max 120)",
			},
		},
		"required": []string{"method", "url"},
	}
}

func (t *HTTPRequestTool) Execute(ctx context.Context, args map[string]interface{}) *ToolResult {
	method, _ := args["method"].(string)
	rawURL, _ := args["url"].(string)

	if method == "" || rawURL == "" {
		return ErrorResult("method and url are required")
	}

	method = strings.ToUpper(method)

	// Validate URL and SSRF protection
	if err := validateURL(rawURL); err != nil {
		return ErrorResult(fmt.Sprintf("URL blocked: %v", err))
	}

	// Parse timeout
	timeout := 30
	if t, ok := args["timeout"].(float64); ok {
		timeout = int(t)
	}
	if timeout < 1 {
		timeout = 1
	}
	if timeout > 120 {
		timeout = 120
	}

	// Build request body
	var bodyReader io.Reader
	if body, ok := args["body"].(string); ok && body != "" {
		bodyReader = strings.NewReader(body)
	}

	req, err := http.NewRequestWithContext(ctx, method, rawURL, bodyReader)
	if err != nil {
		return ErrorResult(fmt.Sprintf("failed to create request: %v", err))
	}

	// Set headers
	if headers, ok := args["headers"].(map[string]interface{}); ok {
		for k, v := range headers {
			if vs, ok := v.(string); ok {
				req.Header.Set(k, vs)
			}
		}
	}

	client := &http.Client{
		Timeout: time.Duration(timeout) * time.Second,
	}

	resp, err := client.Do(req)
	if err != nil {
		return ErrorResult(fmt.Sprintf("request failed: %v", err))
	}
	defer resp.Body.Close()

	// Read body with size limit
	const maxBody = 50000
	limitedReader := io.LimitReader(resp.Body, maxBody+1)
	respBody, err := io.ReadAll(limitedReader)
	if err != nil {
		return ErrorResult(fmt.Sprintf("failed to read response: %v", err))
	}

	bodyStr := string(respBody)
	truncated := false
	if len(bodyStr) > maxBody {
		bodyStr = bodyStr[:maxBody]
		truncated = true
	}

	// Format response headers (selected)
	var headerLines []string
	for _, key := range []string{"Content-Type", "Content-Length", "Location", "Set-Cookie"} {
		if v := resp.Header.Get(key); v != "" {
			headerLines = append(headerLines, fmt.Sprintf("%s: %s", key, v))
		}
	}
	headersStr := strings.Join(headerLines, "\n")

	result := fmt.Sprintf("Status: %d\nHeaders:\n%s\n\nBody:\n%s", resp.StatusCode, headersStr, bodyStr)
	if truncated {
		result += "\n\n[...body truncated at 50000 chars]"
	}

	return SilentResult(result)
}

// validateURL checks the URL is valid and not targeting private/local addresses.
func validateURL(rawURL string) error {
	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("invalid URL: %v", err)
	}

	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("only http/https schemes allowed")
	}

	host := u.Hostname()

	// Block localhost variants
	if host == "localhost" || host == "127.0.0.1" || host == "::1" || host == "0.0.0.0" {
		return fmt.Errorf("localhost access not allowed")
	}

	// Check if it's an IP address in private ranges
	ip := net.ParseIP(host)
	if ip != nil {
		if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
			return fmt.Errorf("private/local IP access not allowed")
		}
	}

	return nil
}
