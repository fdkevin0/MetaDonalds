// Package core defines provider-independent content and submission capabilities.
package core

import (
	"context"
	"fmt"
	"regexp"
	"strings"
)

type Item struct {
	Tags     []string `json:"tags,omitempty"`
	Size     string   `json:"size,omitempty"`
	Seeders  int      `json:"seeders,omitempty"`
	SourceID string   `json:"source_id"`
	ID       string   `json:"id"`
	Title    string   `json:"title"`
	Code     string   `json:"code,omitempty"`
	Summary  string   `json:"summary,omitempty"`
	Score    string   `json:"score,omitempty"`
}
type Page struct {
	Items      []Item `json:"items"`
	NextCursor string `json:"next_cursor,omitempty"`
}
type Resource struct {
	ID       string `json:"id"`
	Kind     string `json:"kind"`
	Title    string `json:"title"`
	InfoHash string `json:"info_hash"`
	Size     string `json:"size,omitempty"`
	Seeders  int    `json:"seeders,omitempty"`
}
type Submission struct {
	Status   string `json:"status"`
	InfoHash string `json:"info_hash"`
	Message  string `json:"message,omitempty"`
}
type Plugin struct {
	ID            string   `json:"id"`
	Type          string   `json:"type"`
	Kind          string   `json:"kind"`
	Capabilities  []string `json:"capabilities"`
	ResourceKinds []string `json:"resource_kinds"`
}
type Source interface {
	Search(context.Context, string, string) (Page, error)
	Detail(context.Context, string) (Item, error)
	Resources(context.Context, string) ([]Resource, error)
}
type FilteredSearcher interface {
	SearchFiltered(context.Context, string, string, *int, *string) (Page, error)
}

type Downloader interface {
	Submit(context.Context, Resource) Submission
}
type Registry struct {
	Plugins     []Plugin
	Sources     map[string]Source
	Downloaders map[string]Downloader
}

func NewRegistry() *Registry {
	return &Registry{Plugins: []Plugin{}, Sources: map[string]Source{}, Downloaders: map[string]Downloader{}}
}
func (r *Registry) AddSource(id, kind string, resourceKinds []string, s Source) {
	r.Sources[id] = s
	r.Plugins = append(r.Plugins, Plugin{id, kind, "source", []string{"search", "detail", "resources"}, resourceKinds})
}
func (r *Registry) AddDownloader(id, kind string, resourceKinds []string, d Downloader) {
	r.Downloaders[id] = d
	r.Plugins = append(r.Plugins, Plugin{id, kind, "downloader", []string{"submit"}, resourceKinds})
}

var HashPattern = regexp.MustCompile(`^[0-9a-fA-F]{40}$`)

func NormalizeResource(r Resource) (Resource, error) {
	if r.Kind != "magnet" || !HashPattern.MatchString(r.InfoHash) {
		return Resource{}, fmt.Errorf("resource must be a magnet with a 40-character hexadecimal info_hash")
	}
	r.InfoHash = strings.ToLower(r.InfoHash)
	return r, nil
}
