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
