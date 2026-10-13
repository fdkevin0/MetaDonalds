package tui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/fdkevin0/MetaDonalds/internal/client"
	"github.com/fdkevin0/MetaDonalds/internal/core"
)

func press(code rune) tea.KeyPressMsg { return tea.KeyPressMsg{Code: code} }

func TestSubmissionRequiresConfirmationAndDoesNotRepeat(t *testing.T) {
	m := newModel(context.Background(), nil)
	next, _ := m.Update(searchDone{items: []client.Torrent{{Title: "test", InfoHash: "hash"}}})
	m = next.(model)
	next, cmd := m.Update(press(tea.KeyEnter))
	m = next.(model)
	if m.phase != "confirm" || cmd != nil {
		t.Fatal("selection submitted without confirmation")
	}
	next, cmd = m.Update(press('y'))
	m = next.(model)
	if !m.busy || cmd == nil {
		t.Fatal("confirmation did not schedule submission")
	}
	_, cmd = m.Update(press('y'))
	if cmd != nil {
		t.Fatal("repeated key scheduled duplicate submission")
	}
	next, cmd = m.Update(submitDone{hash: "hash"})
	m = next.(model)
	if m.busy || !m.accepted["hash"] || cmd != nil {
		t.Fatal("submission completion not recorded")
	}
	_, cmd = m.Update(press(tea.KeyEnter))
	if cmd != nil {
		t.Fatal("accepted resource submitted again")
	}
	next, cmd = m.Update(submitDone{hash: "other", err: errors.New("timeout")})
	m = next.(model)
	if m.accepted["other"] || cmd != nil {
		t.Fatal("failure was marked accepted or automatically retried")
	}
	if got := plain("a\x1b]52;c;secret\a\nb"); got != "a ]52;c;secret  b" {
		t.Fatalf("unsafe terminal output: %q", got)
	}
}

func TestSearchResultsVisible(t *testing.T) {
	m := newModel(context.Background(), nil)
	next, _ := m.Update(tea.WindowSizeMsg{Width: 90, Height: 28})
	next, _ = next.Update(searchDone{items: []client.Torrent{{Title: "Visible search result", InfoHash: "hash", Seeders: 42}}})
	if view := next.View().Content; !strings.Contains(view, "Visible search result") {
		t.Fatalf("search result missing from view:\n%s", view)
	}
}

func TestPluginScoresPreservedWhenScrolling(t *testing.T) {
	m := newModel(context.Background(), nil)
	m.pluginsReady = true
	m.table.SetHeight(3)
	next, _ := m.Update(pageDone{page: core.Page{Items: []core.Item{
		{Title: "Unrated"},
		{Title: "Integer score", Score: "27"},
		{Title: "Decimal score", Score: "3.84"},
	}}})
	m = next.(model)
	if got := m.table.Rows()[0][3]; got != "" {
		t.Fatalf("missing score displayed as %q", got)
	}
	m.selectRow(2)
	for i, want := range []string{"27", "3.84"} {
		if got := m.table.Rows()[i][3]; got != want {
			t.Fatalf("row %d score = %q, want %q", i, got, want)
		}
	}
	if view := m.View().Content; !strings.Contains(view, "3.84") {
		t.Fatalf("decimal score missing from view:\n%s", view)
	}
}

func clickText(t *testing.T, m model, text string) tea.MouseClickMsg {
	t.Helper()
	for y, line := range strings.Split(m.View().Content, "\n") {
		if x := strings.Index(line, text); x >= 0 {
			return tea.MouseClickMsg{X: lipgloss.Width(line[:x]), Y: y, Button: tea.MouseLeft}
		}
	}
	t.Fatalf("click target %q not visible:\n%s", text, m.View().Content)
	return tea.MouseClickMsg{}
}

func TestMouseSearchScrollAndConfirm(t *testing.T) {
	var submissions int
	const hash = "0123456789abcdef0123456789abcdef01234567"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/search":
			if r.URL.Query().Get("q") != "example" {
				t.Errorf("query = %q", r.URL.RawQuery)
			}
			items := make([]client.Torrent, 30)
			for i := range items {
				items[i] = client.Torrent{Title: fmt.Sprintf("Result-%02d", i), InfoHash: hash}
			}
			json.NewEncoder(w).Encode(map[string]any{"results": items})
		case "/api/v1/submit":
			submissions++
			json.NewEncoder(w).Encode(map[string]string{"status": "submitted", "info_hash": hash})
		default:
			t.Errorf("unexpected request: %s", r.URL.Path)
		}
	}))
	defer server.Close()
	c, err := client.New(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	m := newModel(context.Background(), c)
	m.discoveryPending = false // This fixture exercises the retained legacy interaction path.
	update := func(msg tea.Msg) tea.Cmd { next, cmd := m.Update(msg); m = next.(model); return cmd }
	update(tea.WindowSizeMsg{Width: 90, Height: 28})
	if m.View().MouseMode != tea.MouseModeCellMotion {
		t.Fatal("mouse disabled")
	}
	update(clickText(t, m, "Category"))
	if m.focus != 2 || m.phase != "form" {
		t.Fatal("input click did not focus category")
	}
	update(clickText(t, m, "Keywords"))
	m.inputs[0].SetValue("example")
	cmd := update(clickText(t, m, "[ Search ]"))
	if cmd == nil || !m.busy {
		t.Fatal("search click did not start search")
	}
	update(cmd())
	if !strings.Contains(m.View().Content, "Result-00") {
		t.Fatal("HTTP results not displayed")
	}
	for range 8 {
		update(tea.MouseWheelMsg{X: 5, Y: 11, Button: tea.MouseWheelDown})
	}
	if m.top == 0 {
		t.Fatal("wheel did not scroll")
	}
	target := m.top + 1
	cmd = update(clickText(t, m, fmt.Sprintf("Result-%02d", target)))
	if cmd != nil || m.phase != "confirm" || m.cursor != target || submissions != 0 {
		t.Fatal("result click must only open confirmation for clicked row")
	}
	update(clickText(t, m, "[n / Esc] Cancel"))
	if m.phase != "results" {
		t.Fatal("cancel click did not return to results")
	}
	update(clickText(t, m, fmt.Sprintf("Result-%02d", target)))
	confirm := clickText(t, m, "[y] Confirm")
	cmd = update(confirm)
	if cmd == nil || !m.busy {
		t.Fatal("confirm click did not schedule submission")
	}
	if update(confirm) != nil {
		t.Fatal("busy click scheduled duplicate submission")
	}
	update(cmd())
	if submissions != 1 || !m.accepted[hash] {
		t.Fatal("submission did not complete once")
	}
	if update(clickText(t, m, fmt.Sprintf("Result-%02d", target))) != nil || m.phase == "confirm" {
		t.Fatal("accepted resource can be resubmitted")
	}
	update(press(tea.KeyEnd))
	if m.cursor != 29 || !strings.Contains(m.View().Content, "Result-29") {
		t.Fatal("last result not reachable")
	}
	update(press(tea.KeyHome))
	if m.cursor != 0 || !strings.Contains(m.View().Content, "Result-00") {
		t.Fatal("first result not reachable")
	}
	update(tea.WindowSizeMsg{Width: 60, Height: 20})
	if lipgloss.Height(m.View().Content) > 20 {
		t.Fatalf("view overflows small terminal: %d", lipgloss.Height(m.View().Content))
	}
	update(searchDone{})
	if m.top != 0 || len(m.table.Rows()) != 0 {
		t.Fatal("empty search retained stale rows")
	}
}

func TestMouseBoundsAndWrappedConfirmation(t *testing.T) {
	m := newModel(context.Background(), nil)
	next, _ := m.Update(tea.WindowSizeMsg{Width: 60, Height: 20})
	next, _ = next.Update(searchDone{items: []client.Torrent{{Title: strings.Repeat("长标题", 50), InfoHash: "hash"}}})
	m = next.(model)
	for _, msg := range []tea.MouseClickMsg{
		{X: 1, Y: 10, Button: tea.MouseLeft},  // header
		{X: 59, Y: 11, Button: tea.MouseLeft}, // outside table
		{X: 1, Y: 11, Button: tea.MouseRight},
		{X: 1, Y: 12, Button: tea.MouseLeft}, // blank space
	} {
		next, cmd := m.Update(msg)
		if next.(model).phase != "results" || cmd != nil {
			t.Fatalf("unexpected action for %+v", msg)
		}
	}
	next, _ = m.Update(tea.MouseClickMsg{X: 1, Y: 11, Button: tea.MouseLeft})
	m = next.(model)
	next, cmd := m.Update(clickText(t, m, "[n / Esc] Cancel"))
	if next.(model).phase != "results" || cmd != nil {
		t.Fatal("wrapped title shifted cancel hitbox")
	}
	next, _ = m.Update(tea.WindowSizeMsg{Width: 40, Height: 10})
	next, cmd = next.Update(tea.MouseClickMsg{X: 1, Y: 7, Button: tea.MouseLeft})
	if next.(model).busy || cmd != nil {
		t.Fatal("undersized terminal accepted hidden action")
	}
}

func TestSmallTerminalBlocksHiddenKeyboardActions(t *testing.T) {
	m := newModel(context.Background(), nil)
	next, _ := m.Update(searchDone{items: []client.Torrent{{Title: "test", InfoHash: "hash"}}})
	next, _ = next.Update(press(tea.KeyEnter))
	next, _ = next.Update(tea.WindowSizeMsg{Width: 40, Height: 10})
	next, cmd := next.Update(press('y'))
	if next.(model).busy || cmd != nil {
		t.Fatal("hidden confirmation submitted from the keyboard")
	}
	_, cmd = next.Update(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	if cmd == nil {
		t.Fatal("small terminal must still allow quitting")
	}
	next, _ = next.Update(tea.WindowSizeMsg{Width: 90, Height: 28})
	next, cmd = next.Update(press('y'))
	if !next.(model).busy || cmd == nil {
		t.Fatal("visible confirmation no longer works after resizing")
	}
}

func TestPluginSearchDetailsAndDownloaderSelection(t *testing.T) {
	const hash = "0123456789abcdef0123456789abcdef01234567"
	var calls []string
	var queries []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.URL.Path)
		switch {
		case r.URL.Path == "/api/v1/plugins":
			fmt.Fprint(w, `{"plugins":[{"id":"catalog","type":"javdb","kind":"source","capabilities":["search","detail","resources"],"resource_kinds":["magnet"]},{"id":"one","type":"pikpak-rclone","kind":"downloader","capabilities":["submit"],"resource_kinds":["magnet"]},{"id":"two","type":"pikpak-rclone","kind":"downloader","capabilities":["submit"],"resource_kinds":["magnet"]}]}`)
		case strings.HasSuffix(r.URL.Path, "/search"):
			queries = append(queries, r.URL.Query().Get("q"))
			fmt.Fprint(w, `{"items":[{"source_id":"catalog","id":"movie","title":"Example"}],"next_cursor":"2"}`)
		case strings.HasSuffix(r.URL.Path, "/resources"):
			fmt.Fprintf(w, `{"resources":[{"id":"resource","kind":"magnet","title":"Download","info_hash":"%s"}]}`, hash)
		case strings.Contains(r.URL.Path, "/items/"):
			fmt.Fprint(w, `{"source_id":"catalog","id":"movie","title":"Example","summary":"Description"}`)
		case strings.HasSuffix(r.URL.Path, "/submissions"):
			fmt.Fprintf(w, `{"status":"accepted","info_hash":"%s"}`, hash)
		default:
			t.Errorf("unexpected request %s", r.URL.Path)
		}
	}))
	defer srv.Close()
	c, _ := client.New(srv.URL)
	m := newModel(context.Background(), c)
	update := func(msg tea.Msg) tea.Cmd { next, cmd := m.Update(msg); m = next.(model); return cmd }
	update(m.discover())
	if !m.pluginsReady || m.sourceID() != "catalog" {
		t.Fatal("plugins not loaded")
	}
	m.inputs[0].SetValue("query")
	cmd := update(press(tea.KeyEnter))
	update(cmd())
	m.inputs[0].SetValue("edited but not searched")
	cmd = update(press('n'))
	update(cmd())
	cmd = update(press('p'))
	update(cmd())
	if len(queries) != 3 || queries[1] != "query" || queries[2] != "query" {
		t.Fatalf("pagination mixed search inputs: %v", queries)
	}
	cmd = update(press(tea.KeyEnter))
	if cmd == nil || m.phase == "confirm" {
		t.Fatal("item should load details before confirmation")
	}
	update(cmd())
	if !m.resourceMode || !strings.Contains(m.status, "Description") {
		t.Fatal("detail missing")
	}
	if cmd = update(press(tea.KeyEnter)); cmd != nil || m.phase != "confirm" {
		t.Fatal("resource bypassed confirmation")
	}
	cmd = update(press('y'))
	if update(press('y')) != nil {
		t.Fatal("duplicate submission while busy")
	}
	update(cmd())
	if !m.accepted["one:"+hash] {
		t.Fatal("accepted identity missing downloader")
	}
	update(tea.KeyPressMsg{Code: 'd', Mod: tea.ModCtrl})
	if m.downloaderID() != "two" {
		t.Fatal("downloader selection failed")
	}
	update(press(tea.KeyEnter))
	if m.phase != "confirm" {
		t.Fatal("second downloader incorrectly suppressed")
	}
	cmd = update(press('y'))
	update(cmd())
	if !m.accepted["two:"+hash] {
		t.Fatal("second downloader submission failed")
	}
	if calls[len(calls)-1] != "/api/v1/downloaders/two/submissions" {
		t.Fatal(calls)
	}
	update(press(tea.KeyEsc))
	if m.resourceMode || m.items[0].Title != "Example" {
		t.Fatal("back to item list failed")
	}
	update(resourceSubmitDone{key: "unknown", result: core.Submission{Status: "unknown"}})
	if m.accepted["unknown"] || !strings.Contains(m.status, "uncertain") {
		t.Fatal("uncertain marked accepted")
	}
}

func TestDiscoveryLocksSearchAndConfirmation(t *testing.T) {
	c, _ := client.New("http://127.0.0.1:1")
	m := newModel(context.Background(), c)
	next, cmd := m.Update(press(tea.KeyEnter))
	if cmd != nil || !next.(model).discoveryPending {
		t.Fatal("search started before discovery")
	}
	next, _ = next.Update(pluginsDone{err: errors.New("offline")})
	m = next.(model)
	next, cmd = m.Update(press(tea.KeyEnter))
	if cmd != nil {
		t.Fatal("failed discovery fell back to legacy search")
	}
	next, cmd = next.Update(tea.KeyPressMsg{Code: 'r', Mod: tea.ModCtrl})
	m = next.(model)
	if cmd == nil || !m.discoveryPending {
		t.Fatal("retry not locked")
	}
	next, _ = m.Update(pluginsDone{plugins: []core.Plugin{{ID: "one", Kind: "downloader"}, {ID: "two", Kind: "downloader"}}})
	m = next.(model)
	m.phase = "confirm"
	m.downloaderIndex = 1
	next, cmd = m.Update(tea.KeyPressMsg{Code: 'r', Mod: tea.ModCtrl})
	if cmd != nil || next.(model).downloaderID() != "two" {
		t.Fatal("confirmation target changed on refresh")
	}
}

func TestPaginationFailurePreservesNavigationAndNewSearchClearsOldItems(t *testing.T) {
	c, _ := client.New("http://127.0.0.1:1")
	m := newModel(context.Background(), c)
	m.discoveryPending = false
	m.pluginsReady = true
	m.sources = []core.Plugin{{ID: "source", Type: "javdb", Kind: "source"}}
	m.sourceItems = []core.Item{{ID: "old", Title: "Old", SourceID: "source"}}
	m.items = itemRows(m.sourceItems)
	m.phase = "results"
	m.currentCursor = "2"
	m.previousCursors = []string{""}
	m.nextCursor = "3"
	for _, key := range []rune{'p', 'n'} {
		next, cmd := m.Update(press(key))
		if cmd == nil {
			t.Fatal("missing navigation request")
		}
		next, _ = next.Update(pageDone{err: errors.New("failed")})
		m = next.(model)
		if m.currentCursor != "2" || len(m.previousCursors) != 1 || m.nextCursor != "3" || m.items[0].Title != "Old" {
			t.Fatalf("failed page changed state: %+v", m)
		}
	}
	next, _ := m.Update(press('/'))
	m = next.(model)
	m.inputs[0].SetValue("new query")
	next, cmd := m.Update(press(tea.KeyEnter))
	m = next.(model)
	if cmd == nil || len(m.items) != 0 || m.nextCursor != "" || len(m.sourceItems) != 0 {
		t.Fatal("new search kept old results/cursor")
	}
	next, _ = m.Update(pageDone{err: errors.New("failed")})
	m = next.(model)
	if len(m.items) != 0 || m.nextCursor != "" {
		t.Fatal("failed new search restored stale results")
	}
}
