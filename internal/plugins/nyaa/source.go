package nyaa

import (
	"context"
	"fmt"
	"github.com/fdkevin0/MetaDonalds/internal/core"
	"net/http"
)

type Source struct {
	ID     string
	Config Config
	Client *http.Client
}

func (s *Source) Search(ctx context.Context, q, cursor string) (core.Page, error) {
	if cursor != "" {
		return core.Page{}, fmt.Errorf("Nyaa does not support pagination")
	}
	ts, err := Search(ctx, s.Client, s.Config, q)
	if err != nil {
		return core.Page{}, err
	}
	p := core.Page{Items: []core.Item{}}
	for _, t := range ts {
		tags := []string{}
		if t.Trusted {
			tags = append(tags, "可信")
		}
		if t.Remake {
			tags = append(tags, "重制")
		}
		p.Items = append(p.Items, core.Item{Tags: tags, SourceID: s.ID, ID: t.InfoHash, Title: t.Title, Size: t.Size, Seeders: t.Seeders, Summary: fmt.Sprintf("%s · %d seeders", t.Size, t.Seeders), Score: fmt.Sprint(t.Score)})
	}
	return p, nil
}

// RSS has no durable detail endpoint; the content address is the stable item identity.
func (s *Source) Detail(_ context.Context, id string) (core.Item, error) {
	if !core.HashPattern.MatchString(id) {
		return core.Item{}, fmt.Errorf("invalid Nyaa item")
	}
	return core.Item{SourceID: s.ID, ID: id, Title: id, Summary: "Nyaa RSS resource"}, nil
}
func (s *Source) Resources(ctx context.Context, id string) ([]core.Resource, error) {
	_, err := s.Detail(ctx, id)
	if err != nil {
		return nil, err
	}
	return []core.Resource{{ID: id, Kind: "magnet", Title: "Torrent", InfoHash: id}}, nil
}

func (s *Source) SearchFiltered(ctx context.Context, q, cursor string, f *int, c *string) (core.Page, error) {
	copy := *s
	if f != nil {
		copy.Config.F = *f
	}
	if c != nil {
		copy.Config.C = *c
	}
	return copy.Search(ctx, q, cursor)
}
