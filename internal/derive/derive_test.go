package derive

import (
	"testing"
	"time"

	"github.com/lllamnyp/inbox/internal/ghclient"
)

var isBot = BotMatcher(DefaultBotPattern)

func at(h int) time.Time {
	return time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC).Add(time.Duration(h) * time.Hour)
}

func pr() ghclient.PullRequest {
	var p ghclient.PullRequest
	p.Repository.NameWithOwner = "org/repo"
	p.Number = 1
	p.URL = "https://github.com/org/repo/pull/1"
	p.Author.Login = "alice"
	p.State = "OPEN"
	return p
}

func addCommit(p *ghclient.PullRequest, login string, t time.Time, oid string) {
	var c ghclient.CommitNode
	c.Commit.OID = oid
	c.Commit.CommittedDate = t
	c.Commit.Author.User.Login = login
	p.Commits.Nodes = append(p.Commits.Nodes, c)
	p.HeadRefOid = oid
}

func addComment(p *ghclient.PullRequest, login string, t time.Time) {
	p.Comments.Nodes = append(p.Comments.Nodes, ghclient.CommentNode{
		Author:    ghclient.Actor{Login: login},
		CreatedAt: t,
	})
}

func addReview(p *ghclient.PullRequest, login string, t time.Time) {
	p.Reviews.Nodes = append(p.Reviews.Nodes, ghclient.ReviewNode{
		Author:      ghclient.Actor{Login: login},
		SubmittedAt: t,
		State:       "COMMENTED",
	})
}

func TestWaitingOnMe(t *testing.T) {
	g := pr()
	addCommit(&g, "alice", at(0), "sha1")
	addReview(&g, "me", at(1))
	addComment(&g, "alice", at(2))
	addComment(&g, "coderabbitai", at(3))

	p := Derive(g, nil, "me", isBot, at(3))
	if p.TheirLastActionBy != "alice" {
		t.Errorf("their_last_action_by = %q, want alice", p.TheirLastActionBy)
	}
	if len(p.Participants) != 1 || p.Participants["alice"] != 2 {
		t.Errorf("participants = %v, want alice×2 (bots excluded)", p.Participants)
	}
	if p.Role != "reviewer" {
		t.Errorf("role = %q, want reviewer", p.Role)
	}
	if !p.WaitingOnMe {
		t.Error("their comment postdates my review; want WaitingOnMe")
	}
	if p.NewSinceMe.Comments != 1 || p.NewSinceMe.Commits != 0 {
		t.Errorf("new_since_me = %+v, want 1 comment", p.NewSinceMe)
	}
	if p.Status(at(3)) != StatusWaitingOnYou {
		t.Errorf("status = %v, want waiting-on-you", p.Status(at(3)))
	}
}

func TestNotWaitingWhenILastActed(t *testing.T) {
	g := pr()
	addCommit(&g, "alice", at(0), "sha1")
	addComment(&g, "alice", at(1))
	addReview(&g, "me", at(2))

	p := Derive(g, nil, "me", isBot, at(3))
	if p.WaitingOnMe {
		t.Error("my review is the last action; want !WaitingOnMe")
	}
}

func TestBotsDoNotCount(t *testing.T) {
	g := pr()
	g.Author.Login = "me"
	addCommit(&g, "me", at(0), "sha1")
	addComment(&g, "coderabbitai", at(1))
	addComment(&g, "dependabot", at(2))

	p := Derive(g, nil, "me", isBot, at(3))
	if p.WaitingOnMe {
		t.Error("only bots acted after me; want !WaitingOnMe")
	}
	if p.NewSinceMe.Total() != 0 {
		t.Errorf("bot activity counted: %+v", p.NewSinceMe)
	}
}

func TestAckRearms(t *testing.T) {
	g := pr()
	addCommit(&g, "alice", at(0), "sha1")
	addReview(&g, "me", at(1))
	addComment(&g, "alice", at(2))

	p := Derive(g, nil, "me", isBot, at(3))
	ack := at(3)
	p.AcknowledgedAt = &ack
	if p.Status(at(4)) != StatusWaitingOnThem {
		t.Fatal("acked with no newer activity; want waiting-on-them")
	}

	// New activity after the ack re-arms the row.
	addComment(&g, "alice", at(5))
	p2 := Derive(g, p, "me", isBot, at(6))
	if p2.Status(at(6)) != StatusWaitingOnYou {
		t.Error("their comment postdates the ack; want waiting-on-you again")
	}
}

func TestForcePushWithLyingDates(t *testing.T) {
	g := pr()
	addCommit(&g, "alice", at(0), "sha1")
	addReview(&g, "me", at(5))
	prev := Derive(g, nil, "me", isBot, at(6))
	if prev.WaitingOnMe {
		t.Fatal("setup: my review should be the last action")
	}

	// Alice force-pushes; the new tip carries a committedDate *older* than
	// the previous tip (amend/rebase preserved timestamps).
	g2 := pr()
	addCommit(&g2, "alice", at(-2), "sha2")
	addReview(&g2, "me", at(5))

	p := Derive(g2, prev, "me", isBot, at(10))
	if !p.ForcePushed {
		t.Error("head SHA moved to an older tip; want ForcePushed")
	}
	if !p.WaitingOnMe {
		t.Error("they pushed after my review; want WaitingOnMe despite old commit dates")
	}
	if p.NewSinceMe.Commits == 0 {
		t.Error("force-push should bump new_since_me.commits")
	}
	if p.TheirLastActionKind != "push" {
		t.Errorf("their_last_action_kind = %q, want push", p.TheirLastActionKind)
	}

	// The synthetic push event must survive the next poll (SHA unchanged).
	p2 := Derive(g2, p, "me", isBot, at(11))
	if !p2.WaitingOnMe {
		t.Error("waiting state regressed on the poll after the force-push")
	}
}

func TestMyOwnPushDoesNotFlag(t *testing.T) {
	g := pr()
	g.Author.Login = "me"
	addCommit(&g, "me", at(0), "sha1")
	addComment(&g, "alice", at(1))
	addComment(&g, "me", at(2))
	prev := Derive(g, nil, "me", isBot, at(3))

	g2 := pr()
	g2.Author.Login = "me"
	addCommit(&g2, "me", at(-1), "sha2") // my own rebase
	addComment(&g2, "alice", at(1))
	addComment(&g2, "me", at(2))

	p := Derive(g2, prev, "me", isBot, at(4))
	if p.ForcePushed || p.WaitingOnMe {
		t.Errorf("my own force-push flagged: force=%v waiting=%v", p.ForcePushed, p.WaitingOnMe)
	}
}

func TestMonotonicCarry(t *testing.T) {
	g := pr()
	addCommit(&g, "alice", at(0), "sha1")
	addComment(&g, "alice", at(8))
	prev := Derive(g, nil, "me", isBot, at(9))

	// Next poll the comment scrolled out of the (last: N) window.
	g2 := pr()
	addCommit(&g2, "alice", at(0), "sha1")

	p := Derive(g2, prev, "me", isBot, at(10))
	if p.TheirLastActionAt == nil || !p.TheirLastActionAt.Equal(at(8)) {
		t.Errorf("their_last_action_at regressed: %v, want %v", p.TheirLastActionAt, at(8))
	}
	if p.TheirLastActionBy != "alice" {
		t.Errorf("their_last_action_by lost in carry: %q, want alice", p.TheirLastActionBy)
	}
}

func requestReview(p *ghclient.PullRequest, login string, asCodeOwner bool) {
	var rr struct {
		AsCodeOwner       bool
		RequestedReviewer struct {
			User ghclient.Actor `graphql:"... on User"`
		}
	}
	rr.AsCodeOwner = asCodeOwner
	rr.RequestedReviewer.User.Login = login
	p.ReviewRequests.Nodes = append(p.ReviewRequests.Nodes, rr)
}

func TestCodeOwnerOnlyRequestIsMuted(t *testing.T) {
	g := pr()
	addCommit(&g, "alice", at(0), "sha1")
	requestReview(&g, "me", true)

	p := Derive(g, nil, "me", isBot, at(1))
	if !p.Muted {
		t.Error("code-owner request is my only involvement; want Muted")
	}
	if p.Status(at(1)) != StatusMuted {
		t.Errorf("status = %v, want muted", p.Status(at(1)))
	}

	// My first real action un-mutes it — and keeps it un-muted on later
	// polls even after that action scrolls out of the query window, because
	// my_last_action_at never regresses.
	addReview(&g, "me", at(2))
	p2 := Derive(g, p, "me", isBot, at(3))
	if p2.Muted {
		t.Error("I reviewed; want un-muted")
	}
	g2 := pr()
	addCommit(&g2, "alice", at(0), "sha1")
	requestReview(&g2, "me", true)
	p3 := Derive(g2, p2, "me", isBot, at(4))
	if p3.Muted {
		t.Error("mute must not come back after my review scrolls out of the window")
	}
}

func TestDirectRequestIsNotMuted(t *testing.T) {
	g := pr()
	addCommit(&g, "alice", at(0), "sha1")
	requestReview(&g, "me", false)
	if p := Derive(g, nil, "me", isBot, at(1)); p.Muted {
		t.Error("a human asked for my review; must not be muted")
	}

	// A direct request alongside the code-owner one also wins.
	requestReview(&g, "me", true)
	if p := Derive(g, nil, "me", isBot, at(1)); p.Muted {
		t.Error("direct request must override the code-owner request")
	}
}

func TestAssigneeIsNotMuted(t *testing.T) {
	g := pr()
	addCommit(&g, "alice", at(0), "sha1")
	requestReview(&g, "me", true)
	g.Assignees.Nodes = append(g.Assignees.Nodes, ghclient.Actor{Login: "me"})
	if p := Derive(g, nil, "me", isBot, at(1)); p.Muted {
		t.Error("assigned to me; must not be muted")
	}
}

func TestInvisibleInvolvementIsNotMuted(t *testing.T) {
	// No request, no assignment, no action of mine — the search matched on
	// something we can't see (e.g. an @-mention). Stay visible.
	g := pr()
	addCommit(&g, "alice", at(0), "sha1")
	if p := Derive(g, nil, "me", isBot, at(1)); p.Muted {
		t.Error("involvement basis unknown; must not be muted")
	}
}

func TestSnooze(t *testing.T) {
	g := pr()
	addCommit(&g, "alice", at(0), "sha1")
	p := Derive(g, nil, "me", isBot, at(1))
	until := at(24)
	p.SnoozedUntil = &until
	if p.Status(at(2)) != StatusSnoozed {
		t.Error("want snoozed before the deadline")
	}
	if p.Status(at(25)) != StatusWaitingOnYou {
		t.Error("snooze expired and they acted last; want waiting-on-you")
	}
}
