package tui

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/lllamnyp/inbox/internal/derive"
)

const helpText = "↑↓ nav · enter web · o gh · e engage · a ack · s snooze · i info · l log · r poll · / search · 1-7 filter · q quit"

type cols struct {
	repo, title, role, who, since, new, flags int
}

func (m Model) View() string {
	width := m.width
	if width <= 0 {
		width = 100
	}
	now := m.now
	if now.IsZero() {
		now = time.Now()
	}

	var lines []string

	// Header: inbox — 27 open · 8 waiting on you · last poll 42s ago
	waiting, muted := 0, 0
	for _, p := range m.st.PRs {
		switch p.Status(now) {
		case derive.StatusWaitingOnYou:
			waiting++
		case derive.StatusMuted:
			muted++
		}
	}
	pollStr := "last poll never"
	if m.polling {
		pollStr = "polling…"
	} else if !m.lastPollDone.IsZero() {
		pollStr = "last poll " + humanSince(now.Sub(m.lastPollDone)) + " ago"
	} else if !m.st.LastPollAt.IsZero() {
		pollStr = "last poll " + humanSince(now.Sub(m.st.LastPollAt)) + " ago"
	}
	head := fmt.Sprintf(" inbox — %d open · %d waiting on you", len(m.st.PRs), waiting)
	if muted > 0 {
		head += fmt.Sprintf(" · %d muted", muted)
	}
	head += " · " + pollStr
	lines = append(lines, styHeader.Render(ansi.Truncate(head, width, "…")))

	lines = append(lines, m.filterLine(width))

	vh := m.viewportHeight()
	if m.showLog {
		lines = append(lines, styColHead.Render(" recent log — l or esc to close"))
		start := max(len(m.events)-vh, 0)
		for _, e := range m.events[start:] {
			lines = append(lines, styDim.Render(ansi.Truncate(" "+e, width, "…")))
		}
		if len(m.events) == 0 {
			lines = append(lines, styDim.Render("  nothing logged yet"))
		}
	} else {
		c := m.columns(width)
		hdr := " " + pad("", 1) + " " + pad("repo#pr", c.repo) + " " + pad("title", c.title) + " " +
			pad("role", c.role) + " " + pad("who", c.who) + " " + pad("since", c.since) + " " + pad("new", c.new) + " " + pad("⌘", c.flags)
		lines = append(lines, styColHead.Render(hdr))

		if len(m.rows) == 0 {
			lines = append(lines, styDim.Render("  nothing to show — r to poll · 1 to reset the filter"))
		} else {
			end := min(m.offset+vh, len(m.rows))
			for i := m.offset; i < end; i++ {
				lines = append(lines, m.renderRow(m.rows[i], i == m.cursor, c, now))
			}
		}
	}
	for len(lines) < 3+vh {
		lines = append(lines, "")
	}

	if m.showDetail {
		lines = append(lines, m.detailLines(width, now)...)
	}

	switch {
	case m.pollErr != "":
		lines = append(lines, styErr.Render(ansi.Truncate(" poll error: "+m.pollErr, width, "…")))
	case m.status != "" && now.Sub(m.statusAt) < statusTTL:
		lines = append(lines, styOK.Render(ansi.Truncate(" "+m.status, width, "…")))
	default:
		lines = append(lines, "")
	}
	lines = append(lines, styDim.Render(ansi.Truncate(" "+helpText, width, "…")))

	return strings.Join(lines, "\n")
}

func (m Model) filterLine(width int) string {
	var b strings.Builder
	b.WriteString(styDim.Render(" Filter:"))
	for i, name := range filterNames {
		label := fmt.Sprintf("%d:%s", i+1, name)
		if Filter(i) == m.filter {
			b.WriteString(" " + styFilterOn.Render("["+label+"]"))
		} else {
			b.WriteString(" " + styDim.Render(label))
		}
	}
	if m.searching || m.search.Value() != "" {
		b.WriteString("  " + m.search.View())
	}
	return ansi.Truncate(b.String(), width, "…")
}

func (m Model) columns(width int) cols {
	repo := 10
	for _, p := range m.rows {
		if w := lipgloss.Width(shortKey(p)); w > repo {
			repo = w
		}
	}
	repo = min(repo, 26)
	c := cols{repo: repo, role: 4, who: 12, since: 5, new: 10, flags: 3}
	// margins + separators eat 10 columns
	c.title = max(width-c.repo-c.role-c.who-c.since-c.new-c.flags-10, 8)
	return c
}

// shortKey renders "cozyportal#867" — repo name without the owner.
func shortKey(p *derive.PR) string {
	name := p.Repo
	if i := strings.IndexByte(name, '/'); i >= 0 {
		name = name[i+1:]
	}
	return fmt.Sprintf("%s#%d", name, p.Number)
}

func (m Model) renderRow(p *derive.PR, selected bool, c cols, now time.Time) string {
	st := p.Status(now)

	dot := "●"
	switch st {
	case derive.StatusWaitingOnThem:
		dot = "○"
	case derive.StatusSnoozed:
		dot = "⋯"
	case derive.StatusMuted:
		dot = "·"
	}

	title := p.Title
	if p.IsDraft {
		title = "[draft] " + title
	}

	role := "rev"
	if p.Role == "author" {
		role = "auth"
	}

	sinceAt := p.UpdatedAt
	if p.TheirLastActionAt != nil {
		sinceAt = *p.TheirLastActionAt
	}
	sinceD := now.Sub(sinceAt)
	if sinceD < 0 {
		sinceD = 0
	}

	newStr := p.NewSinceMe.String()
	if p.ForcePushed {
		newStr += " !"
	}
	if st == derive.StatusSnoozed {
		newStr = "(snoozed)"
	}
	if st == derive.StatusMuted {
		newStr = "(muted)"
	}

	flags := " "
	if p.WorktreeExists {
		flags = "⎇"
	}
	flags += " "
	if p.SessionFresh {
		flags += "⌗"
	} else {
		flags += " "
	}

	// The counterpart: whoever acted last on their side; for rows where
	// that's unknown (unattributed commits) fall back to the PR author when
	// it isn't me.
	who := p.TheirLastActionBy
	if who == "" && p.Role == "reviewer" {
		who = p.Author
	}
	if who == "" {
		who = "—"
	}

	repoC := pad(shortKey(p), c.repo)
	titleC := pad(title, c.title)
	roleC := pad(role, c.role)
	whoC := pad(who, c.who)
	sinceC := pad(humanSince(sinceD), c.since)
	newC := pad(newStr, c.new)
	flagsC := pad(flags, c.flags)

	if selected {
		return stySelected.Render(" " + dot + " " + repoC + " " + titleC + " " + roleC + " " + whoC + " " + sinceC + " " + newC + " " + flagsC)
	}
	if st == derive.StatusSnoozed || st == derive.StatusMuted {
		return styDim.Render(" " + dot + " " + repoC + " " + titleC + " " + roleC + " " + whoC + " " + sinceC + " " + newC + " " + flagsC)
	}

	dotS := styDotThem.Render(dot)
	if st == derive.StatusWaitingOnYou {
		dotS = styDotYou.Render(dot)
	}
	roleS := styRoleRev.Render(roleC)
	if p.Role == "author" {
		roleS = styRoleAuth.Render(roleC)
	}
	var sinceS string
	switch {
	case sinceD < 24*time.Hour:
		sinceS = stySinceNew.Render(sinceC)
	case sinceD < 72*time.Hour:
		sinceS = stySinceMid.Render(sinceC)
	default:
		sinceS = stySinceOld.Render(sinceC)
	}
	newS := newC
	if p.ForcePushed {
		newS = styForce.Render(newC)
	} else if p.NewSinceMe.Total() == 0 {
		newS = styDim.Render(newC)
	}
	return " " + dotS + " " + repoC + " " + titleC + " " + roleS + " " + whoC + " " + sinceS + " " + newS + " " + styMark.Render(flagsC)
}

func (m Model) detailLines(width int, now time.Time) []string {
	lines := []string{styDim.Render(strings.Repeat("─", max(width, 1)))}
	p := m.selected()
	if p == nil {
		lines = append(lines, styDim.Render(" no selection"), "", "", "", "", "")
		return lines
	}

	wtNote := "missing — press e to engage"
	if p.WorktreeExists {
		wtNote = "exists"
	}
	session := "—"
	if p.ClaudeSessionID != "" {
		if p.SessionFresh {
			session = p.ClaudeSessionID + " (fresh)"
		} else {
			session = p.ClaudeSessionID + " (idle)"
		}
	}
	involve := ""
	switch {
	case p.DirectReviewRequest:
		involve = " · review requested directly"
	case p.CodeOwnerReviewRequest:
		involve = " · review requested via CODEOWNERS"
	}
	if p.Assigned {
		involve += " · assigned"
	}
	mine := "—"
	if p.MyLastActionAt != nil {
		mine = fmt.Sprintf("%s @ %s (%s ago)", p.MyLastActionKind, p.MyLastActionAt.Local().Format("Jan 2 15:04"), humanSince(now.Sub(*p.MyLastActionAt)))
	}
	theirs := "—"
	if p.TheirLastActionAt != nil {
		by := ""
		if p.TheirLastActionBy != "" {
			by = " by " + p.TheirLastActionBy
		}
		theirs = fmt.Sprintf("%s%s @ %s (%s ago)", p.TheirLastActionKind, by, p.TheirLastActionAt.Local().Format("Jan 2 15:04"), humanSince(now.Sub(*p.TheirLastActionAt)))
	}
	sha := p.HeadSHA
	if len(sha) > 12 {
		sha = sha[:12]
	}

	people := "author " + p.Author
	if len(p.Participants) > 0 {
		type pc struct {
			login string
			n     int
		}
		pcs := make([]pc, 0, len(p.Participants))
		for l, n := range p.Participants {
			pcs = append(pcs, pc{l, n})
		}
		sort.Slice(pcs, func(i, j int) bool {
			if pcs[i].n != pcs[j].n {
				return pcs[i].n > pcs[j].n
			}
			return pcs[i].login < pcs[j].login
		})
		parts := make([]string, 0, len(pcs))
		for _, x := range pcs {
			parts = append(parts, fmt.Sprintf("%s×%d", x.login, x.n))
		}
		people += " · active: " + strings.Join(parts, " ")
	}

	kv := func(k, v string) string {
		return ansi.Truncate(" "+styDetailKey.Render(pad(k, 9))+" "+v, width, "…")
	}
	lines = append(lines,
		ansi.Truncate(" "+styHeader.Render(p.Key())+" · "+p.Title, width, "…"),
		kv("url", p.URL),
		kv("worktree", p.WorktreePath+" ("+wtNote+")"),
		kv("session", session),
		kv("people", people),
		kv("actions", "mine: "+mine+" · theirs: "+theirs+" · "+p.ReviewDecision+" · head "+sha+involve),
	)
	return lines
}

// pad truncates (ansi-aware, with ellipsis) then right-pads to width w.
func pad(s string, w int) string {
	s = ansi.Truncate(s, w, "…")
	if gap := w - lipgloss.Width(s); gap > 0 {
		s += strings.Repeat(" ", gap)
	}
	return s
}

func humanSince(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}
