package ghclient

import "time"

// Actor is any comment/review/commit author. Bots and deleted users decode to
// a zero value (empty login); attribution logic treats "" as unknown.
type Actor struct {
	Login string
}

type CommitNode struct {
	Commit struct {
		OID           string `graphql:"oid"`
		CommittedDate time.Time
		Author        struct {
			User Actor
		}
	}
}

type ReviewNode struct {
	Author      Actor
	SubmittedAt time.Time // zero for PENDING reviews
	State       string
}

type CommentNode struct {
	Author    Actor
	CreatedAt time.Time
}

type ReviewThreadNode struct {
	IsResolved bool
	Comments   struct {
		Nodes []CommentNode
	} `graphql:"comments(last: 10)"`
}

type PullRequest struct {
	Number         int
	URL            string `graphql:"url"`
	Title          string
	IsDraft        bool
	State          string
	ReviewDecision string
	HeadRefOid     string `graphql:"headRefOid"`
	UpdatedAt      time.Time
	Repository     struct {
		NameWithOwner string
	}
	Author         Actor
	ReviewRequests struct {
		Nodes []struct {
			// AsCodeOwner distinguishes "CODEOWNERS matched you" from "a
			// human picked you" — the former is issued for every PR touching
			// your paths and is a much weaker signal.
			AsCodeOwner       bool
			RequestedReviewer struct {
				User Actor `graphql:"... on User"`
			}
		}
	} `graphql:"reviewRequests(first: 20)"`
	Assignees struct {
		Nodes []Actor
	} `graphql:"assignees(first: 10)"`
	Commits struct {
		Nodes []CommitNode
	} `graphql:"commits(last: 30)"`
	Reviews struct {
		Nodes []ReviewNode
	} `graphql:"reviews(last: 30)"`
	Comments struct {
		Nodes []CommentNode
	} `graphql:"comments(last: 50)"`
	ReviewThreads struct {
		Nodes []ReviewThreadNode
	} `graphql:"reviewThreads(last: 30)"`
}

// searchQuery fetches one page of the watched set. Pages are kept small
// (see pageSize): with the nested commit/review/comment lists, large pages
// make GitHub's GraphQL gateway time out with 502s.
type searchQuery struct {
	Viewer struct {
		Login string
	}
	Search struct {
		IssueCount int
		PageInfo   struct {
			HasNextPage bool
			EndCursor   string
		}
		Nodes []struct {
			PullRequest PullRequest `graphql:"... on PullRequest"`
		}
	} `graphql:"search(query: $searchQuery, type: ISSUE, first: $pageSize, after: $cursor)"`
}
