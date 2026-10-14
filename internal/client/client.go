// Package client connects user interfaces to an existing MetaDonalds service.
package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/fdkevin0/MetaDonalds/internal/core"
	"io"
	"net/http"
	"net/url"
	"time"
)

const DefaultURL = "http://127.0.0.1:8080"

func ParseURL(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return nil, fmt.Errorf("server must be an HTTP(S) origin, e.g. %s", DefaultURL)
	}
	u.Path = ""
	return u, nil
}

type Client struct {
	base string
	http *http.Client
}

func New(raw string) (*Client, error) {
	u, err := ParseURL(raw)
	if err != nil {
		return nil, err
	}
	return &Client{base: u.String(), http: &http.Client{
		Timeout:       60 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}, nil
}

func (c *Client) request(ctx context.Context, method, path string, body []byte, out any) error {
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		var problem struct{ Detail, Title string }
		_ = json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&problem)
		message := problem.Detail
		if message == "" {
			message = problem.Title
		}
		return &HTTPError{Status: resp.StatusCode, Message: message}
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(out)
}

// Plugins discovers enabled instances, including search-only installations.
func (c *Client) Plugins(ctx context.Context) ([]core.Plugin, error) {
	var out struct {
		Plugins []core.Plugin `json:"plugins"`
	}
	err := c.request(ctx, http.MethodGet, "/api/v1/plugins", nil, &out)
	if err == nil && out.Plugins == nil {
		err = fmt.Errorf("invalid plugin response")
	}
	return out.Plugins, err
}
func (c *Client) SearchSource(ctx context.Context, source, query, cursor, filter, category string) (core.Page, error) {
	params := url.Values{"q": {query}}
	if cursor != "" {
		params.Set("cursor", cursor)
	}
	if filter != "" {
		params.Set("f", filter)
	}
	if category != "" {
		params.Set("c", category)
	}
	var out core.Page
	err := c.request(ctx, http.MethodGet, "/api/v1/sources/"+url.PathEscape(source)+"/search?"+params.Encode(), nil, &out)
	if err == nil && out.Items == nil {
		err = fmt.Errorf("invalid search response")
	}
	return out, err
}
func (c *Client) Detail(ctx context.Context, source, id string) (core.Item, error) {
	var out core.Item
	err := c.request(ctx, http.MethodGet, itemPath(source, id), nil, &out)
	if err == nil && (out.SourceID != source || out.ID != id) {
		err = fmt.Errorf("invalid item response")
	}
	return out, err
}
func (c *Client) Resources(ctx context.Context, source, id string) ([]core.Resource, error) {
	var out struct {
		Resources []core.Resource `json:"resources"`
	}
	err := c.request(ctx, http.MethodGet, itemPath(source, id)+"/resources", nil, &out)
	if err == nil && out.Resources == nil {
		err = fmt.Errorf("invalid resource response")
	}
	return out.Resources, err
}
func itemPath(source, id string) string {
	return "/api/v1/sources/" + url.PathEscape(source) + "/items/" + url.PathEscape(id)
}
func (c *Client) SubmitResource(ctx context.Context, downloader string, r core.Resource) (core.Submission, error) {
	normalized, err := core.NormalizeResource(r)
	if err != nil {
		return core.Submission{Status: "rejected"}, err
	}
	body, err := json.Marshal(map[string]core.Resource{"resource": normalized})
	if err != nil {
		return core.Submission{Status: "rejected"}, err
	}
	var out core.Submission
	err = c.request(ctx, http.MethodPost, "/api/v1/downloaders/"+url.PathEscape(downloader)+"/submissions", body, &out)
	if err != nil {
		var responseError *HTTPError
		if errors.As(err, &responseError) {
			switch responseError.Status {
			case 400, 403, 404, 405, 413, 415, 422:
				return core.Submission{Status: "rejected", InfoHash: normalized.InfoHash, Message: responseError.Message}, err
			}
		}
		return core.Submission{Status: "unknown", InfoHash: normalized.InfoHash}, fmt.Errorf("submission unconfirmed; check downloader before retrying: %w", err)
	}
	if (out.Status != "accepted" && out.Status != "rejected" && out.Status != "unknown") || out.InfoHash != normalized.InfoHash {
		return core.Submission{Status: "unknown", InfoHash: normalized.InfoHash}, fmt.Errorf("invalid submission response; check downloader before retrying")
	}
	return out, nil
}

type HTTPError struct {
	Status  int
	Message string
}

func (e *HTTPError) Error() string { return fmt.Sprintf("HTTP %d: %s", e.Status, e.Message) }
