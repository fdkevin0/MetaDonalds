package tui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/fdkevin0/MetaDonalds/internal/core"
)

type pluginsDone struct {
	plugins []core.Plugin
	err     error
}
type pageDone struct {
	cursor          string
	previousCursors []string
	page            core.Page
	err             error
}
type detailDone struct {
	item      core.Item
	resources []core.Resource
	err       error
}
type resourceSubmitDone struct {
	key    string
	result core.Submission
	err    error
}

func (m model) discover() tea.Msg { p, e := m.client.Plugins(m.ctx); return pluginsDone{p, e} }
func (m model) sourceID() string {
	if len(m.sources) == 0 {
		return ""
	}
	return m.sources[m.sourceIndex].ID
}
func (m model) downloaderID() string {
	if len(m.downloaders) == 0 {
		return ""
	}
	return m.downloaders[m.downloaderIndex].ID
}
func (m model) acceptedKey(hash string) string {
	return m.downloaderID() + ":" + strings.ToLower(hash)
}
func (m model) searchSource(cursor string) tea.Cmd {
	q, f, c := m.activeQuery, m.activeF, m.activeC
	if len(m.sources) > 0 && m.sources[m.sourceIndex].Type != "nyaa" {
		f, c = "", ""
	}
	return func() tea.Msg {
		p, e := m.client.SearchSource(m.ctx, m.sourceID(), q, cursor, f, c)
		return pageDone{page: p, err: e, cursor: cursor, previousCursors: append([]string(nil), m.pendingHistory...)}
	}
}
func (m model) updatePlugins(msg tea.Msg) (model, tea.Cmd, bool) {
	switch v := msg.(type) {
	case pluginsDone:
		m.discoveryPending = false
		if v.err != nil {
			m.status = "Plugin discovery failed. Ctrl+R retries. " + plain(v.err.Error())
			m.discoveryFailed = true
			return m, nil, true
		}
		m.discoveryFailed = false
		m.sources = nil
		m.downloaders = nil
		for _, p := range v.plugins {
			if p.Kind == "source" {
				m.sources = append(m.sources, p)
			} else if p.Kind == "downloader" {
				m.downloaders = append(m.downloaders, p)
			}
		}
		m.sourceIndex, m.downloaderIndex = 0, 0
		m.status = "Ctrl+S: change source · Ctrl+D: change downloader. Search, open details, then choose a resource."
		if len(m.sources) == 0 {
			m.status = "No enabled sources. Configure plugins and restart the service."
		}
		return m, nil, true
	case pageDone:
		m.busy = false
		if v.err != nil {
			m.status = "Search failed: " + plain(v.err.Error())
			return m, nil, true
		}
		m.currentCursor = v.cursor
		m.previousCursors = v.previousCursors
		m.sourceItems = v.page.Items
		m.nextCursor = v.page.NextCursor
		m.resourceMode = false
		m.resources = nil
		m.items = v.page.Items
		m.top = 0
		m.selectRow(0)
		m.phase = "results"
		m.inputs[m.focus].Blur()
		m.status = fmt.Sprintf("%d items. Enter: details/resources · n/p: next/previous page · /: search", len(m.items))
		return m, nil, true
	case detailDone:
		m.busy = false
		if v.err != nil {
			m.status = "Details failed: " + plain(v.err.Error())
			return m, nil, true
		}
		m.detail = v.item
		m.resources = v.resources
		m.resourceMode = true
		m.items = nil
		for _, r := range v.resources {
			m.items = append(m.items, core.Item{Title: r.Title, ID: r.InfoHash, Size: r.Size, Seeders: r.Seeders})
		}
		m.top = 0
		m.selectRow(0)
		m.phase = "results"
		m.status = plain(v.item.Code+" "+v.item.Title+" · "+v.item.Summary) + " · Esc: back to items"
		if len(v.resources) == 0 {
			m.status += " · No downloadable resources."
		}
		return m, nil, true
	case resourceSubmitDone:
		m.busy = false
		m.phase = "results"
		if v.result.Status == "rejected" {
			m.status = "Submission rejected: " + plain(v.result.Message)
		} else if v.err != nil || v.result.Status == "unknown" {
			m.status = "Outcome uncertain. Check downloader before retrying; no automatic retry."
		} else if v.result.Status == "accepted" {
			m.accepted[v.key] = true
			m.status = "Command accepted. Download is not necessarily complete."
		} else {
			m.status = "Submission rejected: " + plain(v.result.Message)
		}
		return m, nil, true
	}
	return m, nil, false
}
