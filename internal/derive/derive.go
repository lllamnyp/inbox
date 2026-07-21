// Package derive turns raw GitHub PR payloads into the inbox's persisted,
// display-ready records: who acted last, whether the PR is waiting on you,
// and what's new since your last action.
package derive

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/lllamnyp/inbox/internal/ghclient"
)

// DefaultBotPattern filters the usual noise generators out of activity
// attribution. Extend per user with $INBOX_BOTS.
const DefaultBotPattern = `gemini|coderabbit|netlify|\[bot\]|dependabot|renovate`

// BotMatcher compiles a case-insensitive login matcher from a regexp pattern.
func BotMatcher(pattern string) func(string) bool {
	re := regexp.MustCompile(`(?i)(` + pattern + `)`)
	return func(login string) bool { return login != "" && re.MatchString(login) }
}

// Counts is the "how many things happened since I last acted" badge.
type Counts struct {
	Commits  int `json:"commits"`
	Reviews  int `json:"reviews"`
	Comments int `json:"comments"`
}

func (c Counts) Total() int { return c.Commits + c.Reviews + c.Comments }

func (c Counts) String() string {
	var parts []string
	if c.Commits > 0 {
		parts = append(parts, fmt.Sprintf("%dc", c.Commits))
	}
	if c.Reviews > 0 {
		parts = append(parts, fmt.Sprintf("%dr", c.Reviews))
	}
	if c.Comments > 0 {
		parts = append(parts, fmt.Sprintf("%dm", c.Comments))
	}
	if len(parts) == 0 {
		return "—"
	}
	return strings.Join(parts, " ")
}

type Status int

const (
	StatusWaitingOnYou Status = iota
	StatusWaitingOnThem
	StatusSnoozed
	StatusMuted
)

// PR is one derived inbox row, persisted as-is in state.json.
type PR struct {
	Repo            string    `json:"repo"` // owner/name
	Number          int       `json:"number"`
	URL             string    `json:"url"`
	Title           string    `json:"title"`
	Author          string    `json:"author"`
	State           string    `json:"state"`
	IsDraft         bool      `json:"is_draft"`
	Role            string    `json:"role"` // "author" | "reviewer"
	HeadSHA         string    `json:"head_sha"`
	HeadRefName     string    `json:"head_ref_name"`
	HeadCommittedAt time.Time `json:"head_committed_at"`
	ReviewDecision  string    `json:"review_decision"`
	UpdatedAt       time.Time `json:"updated_at"`

	MyLastActionAt      *time.Time `json:"my_last_action_at"`
	MyLastActionKind    string     `json:"my_last_action_kind"`
	TheirLastActionAt   *time.Time `json:"their_last_action_at"`
	TheirLastActionKind string     `json:"their_last_action_kind"`
	TheirLastActionBy   string     `json:"their_last_action_by"`
	// Participants counts attributed non-bot activity (commits, reviews,
	// comments) per login, excluding me — who is on the other side of this
	// PR, weighted by how active they've been.
	Participants map[string]int `json:"participants,omitempty"`
	NewSinceMe   Counts         `json:"new_since_me"`
	ForcePushed  bool           `json:"force_pushed"`
	WaitingOnMe  bool           `json:"waiting_on_me"`
	Muted        bool           `json:"muted"`

	DirectReviewRequest    bool `json:"direct_review_request"`
	CodeOwnerReviewRequest bool `json:"code_owner_review_request"`
	Assigned               bool `json:"assigned"`

	AcknowledgedAt *time.Time `json:"acknowledged_at"`
	SnoozedUntil   *time.Time `json:"snoozed_until"`

	WorktreePath    string `json:"worktree_path"`
	WorktreeExists  bool   `json:"worktree_exists"`
	ClaudeSessionID string `json:"claude_session_id"` // newest associated session
	SessionFresh    bool   `json:"claude_session_fresh"`
	// ClaudeSessions lists every session associated with this PR (via
	// pr-link transcript records, plus the worktree-dir fallback), newest
	// first. One orchestrated PR can have several; one session can appear
	// under several PRs.
	ClaudeSessions []SessionRef `json:"claude_sessions,omitempty"`
}

// SessionRef is one Claude Code session attached to a PR row.
type SessionRef struct {
	ID         string    `json:"id"`
	CWD        string    `json:"cwd,omitempty"`
	LastActive time.Time `json:"last_active"`
	Fresh      bool      `json:"fresh"`
}

func (p *PR) Key() string { return p.Repo + "#" + strconv.Itoa(p.Number) }

// Status folds mute, snooze and ack over the raw waiting-on-me boolean. Ack
// is a timestamp, not a flag: the PR is unread again the moment their last
// action postdates the ack.
func (p *PR) Status(now time.Time) Status {
	if p.Muted {
		return StatusMuted
	}
	if p.SnoozedUntil != nil && now.Before(*p.SnoozedUntil) {
		return StatusSnoozed
	}
	if p.WaitingOnMe {
		if p.AcknowledgedAt == nil {
			return StatusWaitingOnYou
		}
		if p.TheirLastActionAt != nil && p.TheirLastActionAt.After(*p.AcknowledgedAt) {
			return StatusWaitingOnYou
		}
	}
	return StatusWaitingOnThem
}

type event struct {
	login string
	at    time.Time
	kind  string // "commit" | "review" | "comment" | "push"
}

func events(pr ghclient.PullRequest) []event {
	var evs []event
	for _, c := range pr.Commits.Nodes {
		evs = append(evs, event{c.Commit.Author.User.Login, c.Commit.CommittedDate, "commit"})
	}
	for _, r := range pr.Reviews.Nodes {
		if r.SubmittedAt.IsZero() { // PENDING review
			continue
		}
		evs = append(evs, event{r.Author.Login, r.SubmittedAt, "review"})
	}
	for _, c := range pr.Comments.Nodes {
		evs = append(evs, event{c.Author.Login, c.CreatedAt, "comment"})
	}
	for _, t := range pr.ReviewThreads.Nodes {
		for _, c := range t.Comments.Nodes {
			evs = append(evs, event{c.Author.Login, c.CreatedAt, "comment"})
		}
	}
	return evs
}

// Derive computes one inbox row from a fresh payload and the previous poll's
// record (nil on first sight). now is the observation time of this poll.
func Derive(pr ghclient.PullRequest, prev *PR, me string, isBot func(string) bool, now time.Time) *PR {
	p := &PR{
		Repo:           pr.Repository.NameWithOwner,
		Number:         pr.Number,
		URL:            pr.URL,
		Title:          pr.Title,
		Author:         pr.Author.Login,
		State:          pr.State,
		IsDraft:        pr.IsDraft,
		ReviewDecision: pr.ReviewDecision,
		HeadSHA:        pr.HeadRefOid,
		HeadRefName:    pr.HeadRefName,
		UpdatedAt:      pr.UpdatedAt,
	}
	if strings.EqualFold(p.Author, me) {
		p.Role = "author"
	} else {
		p.Role = "reviewer"
	}
	for _, rr := range pr.ReviewRequests.Nodes {
		if strings.EqualFold(rr.RequestedReviewer.User.Login, me) {
			if rr.AsCodeOwner {
				p.CodeOwnerReviewRequest = true
			} else {
				p.DirectReviewRequest = true
			}
		}
	}
	for _, a := range pr.Assignees.Nodes {
		if strings.EqualFold(a.Login, me) {
			p.Assigned = true
		}
	}
	if prev != nil {
		p.AcknowledgedAt = prev.AcknowledgedAt
		p.SnoozedUntil = prev.SnoozedUntil
		p.ForcePushed = prev.ForcePushed
		p.WorktreePath = prev.WorktreePath
		p.WorktreeExists = prev.WorktreeExists
		p.ClaudeSessionID = prev.ClaudeSessionID
		p.SessionFresh = prev.SessionFresh
		p.ClaudeSessions = prev.ClaudeSessions
	}

	evs := events(pr)
	var my, their *event
	for i := range evs {
		e := &evs[i]
		if e.login == "" { // commit with no linked GitHub account: unattributable
			continue
		}
		if strings.EqualFold(e.login, me) {
			if my == nil || e.at.After(my.at) {
				my = e
			}
		} else if !isBot(e.login) {
			if their == nil || e.at.After(their.at) {
				their = e
			}
			if p.Participants == nil {
				p.Participants = map[string]int{}
			}
			p.Participants[e.login]++
		}
	}

	for _, e := range evs {
		if e.login == "" || strings.EqualFold(e.login, me) || isBot(e.login) {
			continue
		}
		if my != nil && !e.at.After(my.at) {
			continue
		}
		switch e.kind {
		case "commit":
			p.NewSinceMe.Commits++
		case "review":
			p.NewSinceMe.Reviews++
		case "comment":
			p.NewSinceMe.Comments++
		}
	}

	// Head-SHA movement catches every push, including ones whose commit
	// dates lie (rebase, cherry-pick, --amend preserve old timestamps).
	var tipAt time.Time
	tipLogin := ""
	if n := len(pr.Commits.Nodes); n > 0 {
		tip := pr.Commits.Nodes[n-1].Commit
		tipAt = tip.CommittedDate
		tipLogin = tip.Author.User.Login
	}
	p.HeadCommittedAt = tipAt
	shaMoved := prev != nil && prev.HeadSHA != "" && prev.HeadSHA != pr.HeadRefOid
	if shaMoved && !strings.EqualFold(tipLogin, me) {
		if !prev.HeadCommittedAt.IsZero() && tipAt.Before(prev.HeadCommittedAt) {
			p.ForcePushed = true
		}
		// If the push is invisible to the timestamp comparison (old commit
		// dates), register it as their action observed at poll time.
		hidden := their == nil ||
			(my != nil && !their.at.After(my.at)) ||
			(p.AcknowledgedAt != nil && !their.at.After(*p.AcknowledgedAt))
		if hidden {
			their = &event{login: tipLogin, at: now, kind: "push"}
			if p.NewSinceMe.Commits == 0 {
				p.NewSinceMe.Commits++
			}
		}
	}

	// Monotonic carry: the query windows (last:N) can scroll old events out,
	// and synthetic push events exist only in the poll that saw the SHA move.
	// Last-action timestamps never regress.
	if prev != nil {
		if prev.MyLastActionAt != nil && (my == nil || my.at.Before(*prev.MyLastActionAt)) {
			my = &event{login: me, at: *prev.MyLastActionAt, kind: prev.MyLastActionKind}
		}
		if prev.TheirLastActionAt != nil && (their == nil || their.at.Before(*prev.TheirLastActionAt)) {
			their = &event{login: prev.TheirLastActionBy, at: *prev.TheirLastActionAt, kind: prev.TheirLastActionKind}
		}
	}

	if my != nil {
		t := my.at
		p.MyLastActionAt = &t
		p.MyLastActionKind = my.kind
	}
	if their != nil {
		t := their.at
		p.TheirLastActionAt = &t
		p.TheirLastActionKind = their.kind
		p.TheirLastActionBy = their.login
	}

	p.WaitingOnMe = their != nil && (my == nil || their.at.After(my.at))
	if my != nil && their != nil && my.at.After(their.at) {
		p.ForcePushed = false // I acted after their push; the flag served its purpose
	}
	// CODEOWNERS blanket-matching issues a review request for every PR that
	// touches your paths — that's machine bookkeeping, not a person asking
	// for you. Mute a PR when that request is the *only* involvement: a
	// direct request, an assignment, or any action of my own (which never
	// regresses) keeps it in the inbox. Involvement we can't see here (e.g.
	// an @-mention) leaves the PR visible — conservative by design.
	p.Muted = p.Role == "reviewer" && my == nil && !p.Assigned &&
		p.CodeOwnerReviewRequest && !p.DirectReviewRequest
	return p
}

// ApplyAll derives the whole watch set. PRs absent from the fresh snapshot
// (closed, merged, no longer involving me) drop out of the state.
func ApplyAll(prev map[string]*PR, me string, prs []ghclient.PullRequest, isBot func(string) bool, now time.Time) map[string]*PR {
	next := make(map[string]*PR, len(prs))
	for _, gpr := range prs {
		key := gpr.Repository.NameWithOwner + "#" + strconv.Itoa(gpr.Number)
		next[key] = Derive(gpr, prev[key], me, isBot, now)
	}
	return next
}
