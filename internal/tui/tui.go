package tui

import (
	"context"
	"fmt"
	"os"
	"strings"
	"unicode"

	"charm.land/bubbles/v2/table"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/fdkevin0/MetaDonalds/internal/client"
)

type searchDone struct {
	items []client.Torrent
	err   error
}

type submitDone struct {
	hash string
	err  error
}

type model struct {
	client        *client.Client
	ctx           context.Context
	inputs        []textinput.Model
	focus         int
	table         table.Model
	items         []client.Torrent
	cursor, top   int
	phase         string
	busy          bool
	status        string
	accepted      map[string]bool
	width, height int
}

func newModel(ctx context.Context, c *client.Client) model {
	inputs := make([]textinput.Model, 3)
	for i, placeholder := range []string{"Search keywords", "Filter: blank = server default; 0 / 1 / 2", "Category: blank = server default; e.g. 0_0"} {
		inputs[i] = textinput.New()
		inputs[i].Placeholder = placeholder
		inputs[i].CharLimit = 200
		inputs[i].SetWidth(65)
	}
	inputs[0].Focus()
	return model{ctx: ctx, client: c, inputs: inputs, phase: "form", accepted: map[string]bool{}, width: 90, height: 28,
		table:  table.New(table.WithColumns(columns(90)), table.WithWidth(88), table.WithHeight(10), table.WithFocused(true)),
		status: "Search Nyaa, choose a result, then confirm submission to PikPak."}
}

func columns(width int) []table.Column {
	return []table.Column{{Title: "Title", Width: max(8, width-53)}, {Title: "Size", Width: 10}, {Title: "Seeds", Width: 7}, {Title: "Score", Width: 6}, {Title: "Flags", Width: 12}}
}

func (m model) Init() tea.Cmd { return textinput.Blink }

func (m model) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := message.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.table.SetColumns(columns(msg.Width))
		m.table.SetWidth(max(1, msg.Width-2))
		m.table.SetHeight(max(2, msg.Height-18))
		m.selectRow(m.cursor)
		for i := range m.inputs {
			m.inputs[i].SetWidth(max(10, msg.Width-8))
		}
	case searchDone:
		m.busy = false
		if msg.err != nil {
			m.status = "Search failed: " + plain(msg.err.Error())
			return m, nil
		}
		m.items = msg.items
		m.top = 0
		m.selectRow(0)
		if len(msg.items) == 0 {
			m.status = "No results. Try another query or filter."
		} else {
			m.phase = "results"
			m.inputs[m.focus].Blur()
			m.status = fmt.Sprintf("%d results. Enter selects a resource; / edits your search.", len(msg.items))
		}
		return m, nil
	case submitDone:
		m.busy = false
		m.phase = "results"
		if msg.err != nil {
			m.status = "Outcome uncertain. Check PikPak before retrying; no automatic retry. " + plain(msg.err.Error())
		} else {
			m.accepted[msg.hash] = true
			m.status = "Command accepted. This does NOT mean the download finished; check PikPak."
		}
		return m, nil
	case tea.MouseClickMsg:
		if m.busy || m.width < 60 || m.height < 20 || msg.Button != tea.MouseLeft || msg.X < 0 || msg.X >= m.width-2 {
			return m, nil
		}
		if m.phase == "confirm" {
			if msg.Y == 6+lipgloss.Height(m.confirmTitle()) {
				if msg.X < 11 {
					return m.Update(tea.KeyPressMsg{Code: 'y'})
				}
				if msg.X >= 13 && msg.X < 29 {
					return m.Update(tea.KeyPressMsg{Code: 'n'})
				}
			}
			return m, nil
		}
		if msg.Y >= 2 && msg.Y <= 7 {
			m.inputs[m.focus].Blur()
			m.focus = (msg.Y - 2) / 2
			m.phase = "form"
			return m, m.inputs[m.focus].Focus()
		}
		if msg.Y == 8 && msg.X < len("[ Search ]") {
			m.phase = "form"
			return m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
		}
		if row := msg.Y - 11; row >= 0 && row < m.table.Height() && m.top+row < len(m.items) {
			m.selectRow(m.top + row)
			m.inputs[m.focus].Blur()
			m.phase = "results"
			return m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
		}
		return m, nil
	case tea.MouseWheelMsg:
		if !m.busy && m.phase != "confirm" && m.width >= 60 && m.height >= 20 && msg.X >= 0 && msg.X < m.width-2 && msg.Y >= 10 && msg.Y < 11+m.table.Height() && len(m.items) > 0 {
			if msg.Button == tea.MouseWheelDown {
				m.selectRow(m.cursor + 3)
			}
			if msg.Button == tea.MouseWheelUp {
				m.selectRow(m.cursor - 3)
			}
			m.inputs[m.focus].Blur()
			m.phase = "results"
		}
		return m, nil
	case tea.KeyPressMsg:
		key := msg.String()
		if key == "ctrl+c" {
			return m, tea.Quit
		}
		if m.busy || m.width < 60 || m.height < 20 {
			return m, nil
		}
		switch m.phase {
		case "form":
			switch key {
			case "tab", "shift+tab":
				m.inputs[m.focus].Blur()
				step := 1
				if key == "shift+tab" {
					step = len(m.inputs) - 1
				}
				m.focus = (m.focus + step) % len(m.inputs)
				return m, m.inputs[m.focus].Focus()
			case "enter":
				m.busy = true
				m.status = "Searching…"
				q, f, c := m.inputs[0].Value(), strings.TrimSpace(m.inputs[1].Value()), strings.TrimSpace(m.inputs[2].Value())
				return m, func() tea.Msg { items, err := m.client.Search(m.ctx, q, f, c); return searchDone{items, err} }
			}
		case "results":
			switch key {
			case "/", "esc":
				m.phase = "form"
				return m, m.inputs[m.focus].Focus()
			case "enter":
				if len(m.items) == 0 {
					return m, nil
				}
				if m.accepted[m.items[m.cursor].InfoHash] {
					m.status = "Already accepted in this session. Check PikPak."
					return m, nil
				}
				m.phase = "confirm"
				return m, nil
			case "up", "k":
				m.selectRow(m.cursor - 1)
			case "down", "j":
				m.selectRow(m.cursor + 1)
			case "pgup", "b":
				m.selectRow(m.cursor - m.table.Height())
			case "pgdown", "f", "space":
				m.selectRow(m.cursor + m.table.Height())
			case "u", "ctrl+u":
				m.selectRow(m.cursor - max(1, m.table.Height()/2))
			case "d", "ctrl+d":
				m.selectRow(m.cursor + max(1, m.table.Height()/2))
			case "home", "g":
				m.selectRow(0)
			case "end", "G":
				m.selectRow(len(m.items) - 1)
			case "q":
				return m, tea.Quit
			}
		case "confirm":
			switch key {
			case "esc", "n":
				m.phase = "results"
			case "y":
				m.busy = true
				m.status = "Submitting… If interrupted, check PikPak before retrying."
				hash := m.items[m.cursor].InfoHash
				return m, func() tea.Msg { return submitDone{hash, m.client.Submit(m.ctx, hash)} }
			}
			return m, nil
		}
	}
	var cmd tea.Cmd
	if !m.busy && m.phase == "form" {
		m.inputs[m.focus], cmd = m.inputs[m.focus].Update(message)
	}
	return m, cmd
}

// Keep one visible page in the table so mouse rows and keyboard selection share
// explicit coordinates, independent of the table's private scrolling offset.
func (m *model) selectRow(index int) {
	m.cursor = max(0, min(index, len(m.items)-1))
	height := max(1, m.table.Height())
	m.top = max(0, min(m.top, len(m.items)-height))
	if m.cursor < m.top {
		m.top = m.cursor
	}
	if m.cursor >= m.top+height {
		m.top = m.cursor - height + 1
	}
	rows := make([]table.Row, 0, height)
	for _, item := range m.items[m.top:min(len(m.items), m.top+height)] {
		flags := ""
		if item.Trusted {
			flags += "trusted "
		}
		if item.Remake {
			flags += "remake"
		}
		rows = append(rows, table.Row{plain(item.Title), plain(item.Size), fmt.Sprint(item.Seeders), fmt.Sprint(item.Score), flags})
	}
	m.table.GotoTop()
	m.table.SetRows(rows)
	m.table.SetCursor(m.cursor - m.top)
}

func (m model) confirmTitle() string {
	return lipgloss.NewStyle().Width(m.width - 2).MaxHeight(3).Render(plain(m.items[m.cursor].Title))
}

// Resource titles and upstream errors are untrusted terminal output.
func plain(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, s)
}

func (m model) View() tea.View {
	var content strings.Builder
	content.WriteString("MetaDonalds  /  NYAA → PIKPAK\n\n")
	if m.width < 60 || m.height < 20 {
		content.WriteString("Please resize your terminal to at least 60 × 20. Ctrl+C quits.")
	} else if m.phase == "confirm" {
		item := m.items[m.cursor]
		title := m.confirmTitle()
		status := "Command acceptance does not mean download completion."
		if m.busy {
			status = m.status
		}
		fmt.Fprintf(&content, "Submit to PikPak?\n\n%s\n%s\n\n[y] Confirm  [n / Esc] Cancel\n\nCheck PikPak first if a previous attempt was uncertain.\n\n%s", title, plain(item.InfoHash), status)
	} else {
		for i, label := range []string{"Keywords", "Filter", "Category"} {
			fmt.Fprintf(&content, "%s\n%s\n", label, m.inputs[i].View())
		}
		content.WriteString("[ Search ]\n\n" + m.table.View() + "\n\n")
		status := lipgloss.NewStyle().Width(m.width - 2).MaxHeight(3).Render(m.status)
		content.WriteString(status + "\n\nTab: field · Enter: search/select · ↑/↓: navigate\nClick: select · Wheel: scroll · /: edit · Ctrl+C: quit")
	}
	v := tea.NewView(lipgloss.NewStyle().Width(max(10, m.width-2)).Render(content.String()))
	v.AltScreen = true
	v.MouseMode = tea.MouseModeCellMotion
	return v
}

// Run displays the terminal interface for an already-running service.
func Run(ctx context.Context, serverURL string) error {
	c, err := client.New(serverURL)
	if err != nil {
		return err
	}
	final, err := tea.NewProgram(newModel(ctx, c), tea.WithContext(ctx)).Run()
	if m, ok := final.(model); ok && m.busy && m.phase == "confirm" {
		fmt.Fprintln(os.Stderr, "Submission interrupted; outcome uncertain. Check PikPak before retrying.")
	}
	if ctx.Err() != nil {
		return nil
	}
	return err
}
