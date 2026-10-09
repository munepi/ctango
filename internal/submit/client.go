package submit

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// DefaultBaseURL is the CTAN submission endpoint. It can be overridden with
// the CTAN_SUBMIT_URL environment variable, e.g. to point at a test server.
const DefaultBaseURL = "https://www.ctan.org/submit"

// Severity levels used by the API.
const (
	SeverityError   = "ERROR"
	SeverityWarning = "WARNING"
	SeverityInfo    = "INFO"
)

// Message is one entry of the server response. On the wire it is an array
// ["ERROR", "Missing field", "pkg", ...].
type Message struct {
	Severity string
	Text     string
	Args     []any
}

func (m *Message) UnmarshalJSON(b []byte) error {
	var raw []any
	if err := json.Unmarshal(b, &raw); err != nil {
		return err
	}
	if len(raw) < 2 {
		return fmt.Errorf("malformed message %s", b)
	}
	m.Severity = fmt.Sprint(raw[0])
	m.Text = fmt.Sprint(raw[1])
	m.Args = raw[2:]
	return nil
}

func (m Message) MarshalJSON() ([]byte, error) {
	return json.Marshal(append([]any{m.Severity, m.Text}, m.Args...))
}

// String renders the message like "Missing field: pkg".
func (m Message) String() string {
	if len(m.Args) == 0 {
		return m.Text
	}
	args := make([]string, len(m.Args))
	for i, a := range m.Args {
		args[i] = fmt.Sprint(a)
	}
	return m.Text + ": " + strings.Join(args, ", ")
}

// Result is the parsed response of validate or upload.
type Result struct {
	StatusCode int
	Messages   []Message
	Raw        []byte
}

// HasErrors reports whether the server rejected the submission.
func (r *Result) HasErrors() bool {
	if r.StatusCode != http.StatusOK {
		return true
	}
	for _, m := range r.Messages {
		if m.Severity == SeverityError {
			return true
		}
	}
	return false
}

// Client is a CTAN submission API client.
type Client struct {
	BaseURL   string
	HTTP      *http.Client
	UserAgent string
}

// NewClient returns a client for the CTAN submission API.
func NewClient(userAgent string) *Client {
	base := os.Getenv("CTAN_SUBMIT_URL")
	if base == "" {
		base = DefaultBaseURL
	}
	return &Client{
		BaseURL:   strings.TrimRight(base, "/"),
		HTTP:      &http.Client{Timeout: 10 * time.Minute},
		UserAgent: userAgent,
	}
}

// Validate checks the submission. Nothing is stored on the server.
func (c *Client) Validate(ctx context.Context, s *Submission) (*Result, error) {
	return c.post(ctx, "validate", s)
}

// Upload validates the submission and, if there are no errors, hands it
// over to the CTAN team.
func (c *Client) Upload(ctx context.Context, s *Submission) (*Result, error) {
	return c.post(ctx, "upload", s)
}

// FieldInfo describes a field as reported by /submit/fields.
type FieldInfo map[string]any

// Fields returns the field definitions known to the server together with
// the raw JSON.
func (c *Client) Fields(ctx context.Context) (map[string]FieldInfo, []byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/fields", nil)
	if err != nil {
		return nil, nil, err
	}
	body, status, err := c.do(req)
	if err != nil {
		return nil, nil, err
	}
	if status != http.StatusOK {
		return nil, body, fmt.Errorf("%s/fields: HTTP %d: %s", c.BaseURL, status, snippet(body))
	}
	var fields map[string]FieldInfo
	if err := json.Unmarshal(body, &fields); err != nil {
		return nil, body, fmt.Errorf("%s/fields: %w", c.BaseURL, err)
	}
	return fields, body, nil
}

func (c *Client) post(ctx context.Context, action string, s *Submission) (*Result, error) {
	body, contentType, err := encode(s)
	if err != nil {
		return nil, err
	}
	url := c.BaseURL + "/" + action
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("Accept", "application/json")

	raw, status, err := c.do(req)
	if err != nil {
		return nil, err
	}
	res := &Result{StatusCode: status, Raw: raw}
	switch status {
	case http.StatusOK, http.StatusConflict:
		if err := json.Unmarshal(raw, &res.Messages); err != nil {
			return res, fmt.Errorf("%s: unexpected response (HTTP %d): %s", url, status, snippet(raw))
		}
		return res, nil
	default:
		// 404 (bad API version or method) may carry messages too.
		if json.Unmarshal(raw, &res.Messages) == nil && len(res.Messages) > 0 {
			return res, nil
		}
		return res, fmt.Errorf("%s: HTTP %d: %s", url, status, snippet(raw))
	}
}

func (c *Client) do(req *http.Request) ([]byte, int, error) {
	if c.UserAgent != "" {
		req.Header.Set("User-Agent", c.UserAgent)
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	return b, resp.StatusCode, err
}

// encode builds the multipart/form-data body. Text values are sent
// literally (like curl --form-string), the archive as the "file" part.
// Archives are small enough to be buffered, which also gives the server a
// Content-Length.
func encode(s *Submission) (io.Reader, string, error) {
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	for _, f := range s.Fields() {
		if err := w.WriteField(f.Name, f.Value); err != nil {
			return nil, "", err
		}
	}
	if s.Archive != "" {
		f, err := os.Open(s.Archive)
		if err != nil {
			return nil, "", err
		}
		defer f.Close()
		part, err := w.CreateFormFile("file", filepath.Base(s.Archive))
		if err != nil {
			return nil, "", err
		}
		if _, err := io.Copy(part, f); err != nil {
			return nil, "", err
		}
	}
	if err := w.Close(); err != nil {
		return nil, "", err
	}
	return &buf, w.FormDataContentType(), nil
}

func snippet(b []byte) string {
	s := strings.TrimSpace(string(b))
	if len(s) > 500 {
		s = s[:500] + "..."
	}
	if s == "" {
		s = "(empty body)"
	}
	return s
}
