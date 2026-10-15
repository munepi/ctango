// Package ctanapi is a small client for the read-only CTAN JSON API
// (https://ctan.org/help/json/2.0).
package ctanapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// DefaultBaseURL can be overridden with the CTANGO_API_URL environment variable.
const DefaultBaseURL = "https://ctan.org/json/2.0"

// ErrNotFound is returned when the package or author does not exist.
var ErrNotFound = errors.New("not found on CTAN")

// StringList accepts both "x" and ["x", "y"], since the JSON API uses either
// form depending on how many values there are.
type StringList []string

func (l *StringList) UnmarshalJSON(b []byte) error {
	var one string
	if err := json.Unmarshal(b, &one); err == nil {
		if one == "" {
			*l = nil
		} else {
			*l = StringList{one}
		}
		return nil
	}
	var many []string
	if err := json.Unmarshal(b, &many); err != nil {
		return err
	}
	*l = many
	return nil
}

// Package is the subset of /pkg/<name> used by ctan.
type Package struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Caption string `json:"caption"`
	Authors []struct {
		ID     string `json:"id"`
		Active bool   `json:"active"`
	} `json:"authors"`
	License StringList `json:"license"`
	Version struct {
		Number string `json:"number"`
		Date   string `json:"date"`
	} `json:"version"`
	Descriptions []struct {
		Language *string `json:"language"`
		Text     string  `json:"text"`
	} `json:"descriptions"`
	Home        StringList `json:"home"`
	Bugs        StringList `json:"bugs"`
	Support     StringList `json:"support"`
	Repository  StringList `json:"repository"`
	Development StringList `json:"development"`
	Announce    StringList `json:"announce"`
	CTAN        *struct {
		Path string `json:"path"`
		File bool   `json:"file"`
	} `json:"ctan"`
	Topics  []string `json:"topics"`
	TeXLive string   `json:"texlive"`
	MiKTeX  string   `json:"miktex"`
}

// Description returns the language-neutral (or else the first) description.
func (p *Package) Description() string {
	for _, d := range p.Descriptions {
		if d.Language == nil {
			return d.Text
		}
	}
	if len(p.Descriptions) > 0 {
		return p.Descriptions[0].Text
	}
	return ""
}

// CTANPath returns the package directory below /tex-archive.
func (p *Package) CTANPath() string {
	if p.CTAN == nil {
		return ""
	}
	return p.CTAN.Path
}

// Author is the subset of /author/<id> used by ctan.
type Author struct {
	Key        string `json:"key"`
	GivenName  string `json:"givenname"`
	Von        string `json:"von"`
	FamilyName string `json:"familyname"`
	Junior     string `json:"junior"`
	Pseudonym  string `json:"pseudonym"`
}

// Name returns the display name, e.g. "Munehiro Yamamoto".
func (a *Author) Name() string {
	var parts []string
	for _, s := range []string{a.GivenName, a.Von, a.FamilyName} {
		if s != "" {
			parts = append(parts, s)
		}
	}
	name := strings.Join(parts, " ")
	if a.Junior != "" {
		name += ", " + a.Junior
	}
	if name == "" {
		name = a.Pseudonym
	}
	if name == "" {
		name = a.Key
	}
	return name
}

// Client is a CTAN JSON API client.
type Client struct {
	BaseURL   string
	HTTP      *http.Client
	UserAgent string
}

// NewClient returns a client for the CTAN JSON API.
func NewClient(userAgent string) *Client {
	base := os.Getenv("CTANGO_API_URL")
	if base == "" {
		base = DefaultBaseURL
	}
	return &Client{
		BaseURL:   strings.TrimRight(base, "/"),
		HTTP:      &http.Client{Timeout: 30 * time.Second},
		UserAgent: userAgent,
	}
}

// Package fetches /pkg/<name>. It returns ErrNotFound for unknown packages.
func (c *Client) Package(ctx context.Context, name string) (*Package, []byte, error) {
	var p Package
	raw, err := c.get(ctx, "/pkg/"+url.PathEscape(name), &p)
	if err != nil {
		return nil, raw, err
	}
	return &p, raw, nil
}

// Author fetches /author/<id>.
func (c *Client) Author(ctx context.Context, id string) (*Author, error) {
	var a Author
	if _, err := c.get(ctx, "/author/"+url.PathEscape(id), &a); err != nil {
		return nil, err
	}
	return &a, nil
}

func (c *Client) get(ctx context.Context, path string, v any) ([]byte, error) {
	u := c.BaseURL + path
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	if c.UserAgent != "" {
		req.Header.Set("User-Agent", c.UserAgent)
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusNotFound:
		return raw, ErrNotFound
	default:
		return raw, fmt.Errorf("%s: HTTP %d", u, resp.StatusCode)
	}
	if err := json.Unmarshal(raw, v); err != nil {
		return raw, fmt.Errorf("%s: %w", u, err)
	}
	return raw, nil
}
