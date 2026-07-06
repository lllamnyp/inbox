// Package tui is the bubbletea program: an inbox table over the derived PR
// set, with filters, ack/snooze, and engage.
package tui

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/lllamnyp/inbox/internal/derive"
	"github.com/lllamnyp/inbox/internal/ghclient"
	"github.com/lllamnyp/inbox/internal/state"
)

type Filter int

const (
	FilterAll Filter = iota
	FilterMine
	FilterReviewing
	FilterWaitingYou
	FilterWaitingThem
	FilterSnoozed
	FilterMuted
)

var filterNames = []string{"all", "mine", "reviewing", "waiting-you", "waiting-them", "snoozed", "muted"}

type Model struct {
	statePath    string
	st           *state.State
	client       *ghclient.Client
	isBot        func(string) bool
	baseInterval time.Duration

	rows   []*derive.PR // filtered + sorted view over st.PRs
	cursor int
	offset int

	filter    Filter
	search    textinput.Model
	searching bool

	showDetail bool
	showLog    bool
	status     string    // transient message (engage result, save errors, ...)
	statusAt   time.Time // status fades from the dashboard after statusTTL
	events     []string  // rolling log of everything status-worthy, newest last
	pollErr    string    // ongoing condition, shown while it persists

	width, height int
	now           time.Time

	polling       bool
	lastPollStart time.Time
	lastPollDone  time.Time
	unchanged     int // consecutive polls with an unchanged updatedAt set
	fingerprint   string
}

func New(st *state.State, statePath string, client *ghclient.Client, isBot func(string) bool, interval time.Duration) Model {
	ti := textinput.New()
	ti.Prompt = "/"
	ti.Placeholder = "substring…"
	ti.CharLimit = 80
	ti.Width = 30
	m := Model{
		statePath:     statePath,
		st:            st,
		client:        client,
		isBot:         isBot,
		baseInterval:  interval,
		search:        ti,
		now:           time.Now(),
		polling:       true, // Init fires the first poll immediately
		lastPollStart: time.Now(),
	}
	m.rebuild()
	return m
}

func (m Model) Init() tea.Cmd {
	return tea.Batch(pollCmd(m.client), tick())
}

// --- messages ---

type tickMsg time.Time

type pollResult struct {
	login string
	prs   []ghclient.PullRequest
	err   error
}

type engageResult struct {
	key  string
	path string
	log  string
	err  error
}

type execDoneMsg struct{ err error }

// --- commands ---

func tick() tea.Cmd {
	return tea.Tick(time.Second, func(t time.Time) tea.Msg { return tickMsg(t) })
}

func pollCmd(client *ghclient.Client) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		login, prs, err := client.Fetch(ctx)
		return pollResult{login: login, prs: prs, err: err}
	}
}

// --- adaptive polling ---

// Back off to 5 min once the search's updatedAt set has been unchanged for 3
// consecutive polls; any change snaps back to the base interval.
func (m Model) currentInterval() time.Duration {
	if m.unchanged >= 3 {
		return 5 * time.Minute
	}
	return m.baseInterval
}

func fingerprint(prs []ghclient.PullRequest) string {
	keys := make([]string, 0, len(prs))
	for _, p := range prs {
		keys = append(keys, fmt.Sprintf("%s#%d@%s", p.Repository.NameWithOwner, p.Number, p.UpdatedAt.Format(time.RFC3339)))
	}
	sort.Strings(keys)
	return strings.Join(keys, "\n")
}

// --- view-model maintenance ---

func statusRank(s derive.Status) int {
	switch s {
	case derive.StatusWaitingOnYou:
		return 0
	case derive.StatusWaitingOnThem:
		return 1
	case derive.StatusSnoozed:
		return 2
	default:
		return 3
	}
}

func (m *Model) matchFilter(p *derive.PR) bool {
	// Muted rows are the whole point of muting: they show up nowhere except
	// the dedicated tab.
	if p.Muted {
		return m.filter == FilterMuted
	}
	switch m.filter {
	case FilterMine:
		return p.Role == "author"
	case FilterReviewing:
		return p.Role == "reviewer"
	case FilterWaitingYou:
		return p.Status(m.now) == derive.StatusWaitingOnYou
	case FilterWaitingThem:
		return p.Status(m.now) == derive.StatusWaitingOnThem
	case FilterSnoozed:
		return p.Status(m.now) == derive.StatusSnoozed
	case FilterMuted:
		return false
	default:
		return true
	}
}

func (m *Model) rebuild() {
	var selKey string
	if p := m.selected(); p != nil {
		selKey = p.Key()
	}

	q := strings.ToLower(m.search.Value())
	rows := make([]*derive.PR, 0, len(m.st.PRs))
	for _, p := range m.st.PRs {
		if !m.matchFilter(p) {
			continue
		}
		if q != "" && !strings.Contains(strings.ToLower(p.Key()+" "+p.Title), q) {
			continue
		}
		rows = append(rows, p)
	}
	// Default sort: waiting-on-you first, then their last action, newest first.
	sort.SliceStable(rows, func(i, j int) bool {
		ri, rj := statusRank(rows[i].Status(m.now)), statusRank(rows[j].Status(m.now))
		if ri != rj {
			return ri < rj
		}
		var ti, tj time.Time
		if rows[i].TheirLastActionAt != nil {
			ti = *rows[i].TheirLastActionAt
		}
		if rows[j].TheirLastActionAt != nil {
			tj = *rows[j].TheirLastActionAt
		}
		if !ti.Equal(tj) {
			return ti.After(tj)
		}
		return rows[i].Key() < rows[j].Key()
	})
	m.rows = rows

	m.cursor = 0
	for i, p := range rows {
		if p.Key() == selKey {
			m.cursor = i
			break
		}
	}
	m.clampScroll()
}

func (m *Model) selected() *derive.PR {
	if m.cursor >= 0 && m.cursor < len(m.rows) {
		return m.rows[m.cursor]
	}
	return nil
}

func (m *Model) viewportHeight() int {
	h := m.height
	if h <= 0 {
		h = 24
	}
	h -= 5 // header + filter + column header + status + help
	if m.showDetail {
		h -= 7
	}
	if h < 1 {
		h = 1
	}
	return h
}

func (m *Model) clampScroll() {
	if m.cursor >= len(m.rows) {
		m.cursor = len(m.rows) - 1
	}
	if m.cursor < 0 {
		m.cursor = 0
	}
	vh := m.viewportHeight()
	if m.cursor < m.offset {
		m.offset = m.cursor
	}
	if m.cursor >= m.offset+vh {
		m.offset = m.cursor - vh + 1
	}
	if m.offset < 0 {
		m.offset = 0
	}
}

func (m *Model) saveState() {
	if err := m.st.Save(m.statePath); err != nil {
		m.setStatus("save failed: " + err.Error())
	}
}

// statusTTL is how long a transient message stays on the dashboard; the full
// history stays in the log (l).
const statusTTL = 5 * time.Second

const maxLogEntries = 200

// setStatus shows an ephemeral message on the dashboard and records it in
// the log.
func (m *Model) setStatus(msg string) {
	m.status = msg
	m.statusAt = time.Now()
	m.logLine(msg)
}

// logLine records a message in the log without touching the dashboard.
func (m *Model) logLine(msg string) {
	m.events = append(m.events, time.Now().Format("15:04:05")+" "+msg)
	if len(m.events) > maxLogEntries {
		m.events = m.events[len(m.events)-maxLogEntries:]
	}
}
