package tui

import (
	"bytes"
	"fmt"
	"os/exec"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/lllamnyp/inbox/internal/derive"
	"github.com/lllamnyp/inbox/internal/worktree"
)

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.clampScroll()
		return m, nil

	case tickMsg:
		m.now = time.Time(msg)
		cmds := []tea.Cmd{tick()}
		if !m.polling && m.now.Sub(m.lastPollStart) >= m.currentInterval() {
			cmds = append(cmds, m.startPoll())
		}
		return m, tea.Batch(cmds...)

	case pollResult:
		m.polling = false
		m.lastPollDone = time.Now()
		if msg.err != nil {
			m.pollErr = msg.err.Error()
			return m, nil
		}
		m.pollErr = ""
		fp := fingerprint(msg.prs)
		if fp == m.fingerprint {
			m.unchanged++
		} else {
			m.unchanged = 0
			m.fingerprint = fp
		}
		m.st.MyLogin = msg.login
		m.st.LastPollAt = time.Now()
		m.st.PRs = derive.ApplyAll(m.st.PRs, msg.login, msg.prs, m.isBot, time.Now())
		sc := worktree.NewScanner()
		for _, p := range m.st.PRs {
			sc.Annotate(p)
		}
		m.saveState()
		m.rebuild()
		return m, nil

	case engageResult:
		if msg.err != nil {
			m.status = "engage failed: " + msg.err.Error()
		} else {
			m.status = "engaged: " + msg.path
			if p, ok := m.st.PRs[msg.key]; ok {
				worktree.NewScanner().Annotate(p)
				m.saveState()
				m.rebuild()
			}
		}
		return m, nil

	case execDoneMsg:
		if msg.err != nil {
			m.status = msg.err.Error()
		}
		return m, nil

	case tea.KeyMsg:
		return m.handleKey(msg)
	}
	return m, nil
}

func (m *Model) startPoll() tea.Cmd {
	m.polling = true
	m.lastPollStart = time.Now()
	return pollCmd(m.client)
}

func (m Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.searching {
		switch msg.String() {
		case "esc":
			m.searching = false
			m.search.Blur()
			m.search.SetValue("")
			m.rebuild()
			return m, nil
		case "enter":
			m.searching = false
			m.search.Blur()
			return m, nil
		default:
			var cmd tea.Cmd
			m.search, cmd = m.search.Update(msg)
			m.rebuild()
			return m, cmd
		}
	}

	switch msg.String() {
	case "q", "ctrl+c":
		m.saveState()
		return m, tea.Quit

	case "up", "k":
		m.cursor--
		m.clampScroll()
	case "down", "j":
		m.cursor++
		m.clampScroll()
	case "pgup":
		m.cursor -= m.viewportHeight()
		m.clampScroll()
	case "pgdown":
		m.cursor += m.viewportHeight()
		m.clampScroll()
	case "g", "home":
		m.cursor = 0
		m.clampScroll()
	case "G", "end":
		m.cursor = len(m.rows) - 1
		m.clampScroll()

	case "1", "2", "3", "4", "5", "6", "7":
		m.filter = Filter(int(msg.String()[0] - '1'))
		m.rebuild()

	case "/":
		m.searching = true
		return m, m.search.Focus()

	case "esc":
		m.status = ""
		if m.search.Value() != "" {
			m.search.SetValue("")
			m.rebuild()
		}

	case "enter": // open in browser
		if p := m.selected(); p != nil {
			return m, openBrowser(p.URL)
		}

	case "o": // gh pr view in the terminal
		if p := m.selected(); p != nil {
			c := exec.Command("gh", "pr", "view", p.URL, "--comments")
			return m, tea.ExecProcess(c, func(err error) tea.Msg { return execDoneMsg{err: err} })
		}

	case "e": // engage: create the worktree, check out the PR
		if p := m.selected(); p != nil {
			m.status = "engaging " + p.Key() + "…"
			return m, engageCmd(p)
		}

	case "a": // ack toggles read/unread without touching GitHub state
		if p := m.selected(); p != nil {
			if p.AcknowledgedAt != nil && (p.TheirLastActionAt == nil || !p.TheirLastActionAt.After(*p.AcknowledgedAt)) {
				p.AcknowledgedAt = nil
				m.status = "unread: " + p.Key()
			} else {
				p.AcknowledgedAt = new(time.Now())
				m.status = "acked: " + p.Key()
			}
			m.saveState()
			m.rebuild()
		}

	case "s": // snooze 24h; s again unsnoozes
		if p := m.selected(); p != nil {
			if p.SnoozedUntil != nil && time.Now().Before(*p.SnoozedUntil) {
				p.SnoozedUntil = nil
				m.status = "unsnoozed: " + p.Key()
			} else {
				p.SnoozedUntil = new(time.Now().Add(24 * time.Hour))
				m.status = fmt.Sprintf("snoozed until %s: %s", p.SnoozedUntil.Format("Jan 2 15:04"), p.Key())
			}
			m.saveState()
			m.rebuild()
		}

	case "r": // force an immediate poll
		if !m.polling {
			return m, m.startPoll()
		}

	case "i":
		m.showDetail = !m.showDetail
		m.clampScroll()
	}
	return m, nil
}

func engageCmd(p *derive.PR) tea.Cmd {
	key, url, repo, num, headRef := p.Key(), p.URL, p.Repo, p.Number, p.HeadRefName
	return func() tea.Msg {
		var buf bytes.Buffer
		path, err := worktree.Engage(url, repo, num, headRef, &buf)
		return engageResult{key: key, path: path, log: buf.String(), err: err}
	}
}

func openBrowser(url string) tea.Cmd {
	return func() tea.Msg {
		cmd := exec.Command("xdg-open", url)
		if err := cmd.Start(); err != nil {
			return execDoneMsg{err: err}
		}
		go cmd.Wait()
		return execDoneMsg{}
	}
}
