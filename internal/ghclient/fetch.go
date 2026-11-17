// Package ghclient syncs the watched PR set over GitHub GraphQL.
package ghclient

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/shurcooL/githubv4"
	"golang.org/x/oauth2"
)

// SearchQuery returns exactly the PRs where the viewer is author, reviewer,
// review-requested, or commenter.
const SearchQuery = "involves:@me is:pr is:open archived:false"

type Client struct {
	gql *githubv4.Client
}

// Token resolves the API token: $GITHUB_TOKEN, $GH_TOKEN, then `gh auth token`.
func Token() (string, error) {
	for _, env := range []string{"GITHUB_TOKEN", "GH_TOKEN"} {
		if tok := os.Getenv(env); tok != "" {
			return tok, nil
		}
	}
	out, err := exec.Command("gh", "auth", "token").Output()
	if err != nil {
		return "", fmt.Errorf("no $GITHUB_TOKEN/$GH_TOKEN and `gh auth token` failed: %w", err)
	}
	tok := strings.TrimSpace(string(out))
	if tok == "" {
		return "", fmt.Errorf("`gh auth token` returned an empty token")
	}
	return tok, nil
}

func New(ctx context.Context) (*Client, error) {
	tok, err := Token()
	if err != nil {
		return nil, err
	}
	src := oauth2.StaticTokenSource(&oauth2.Token{AccessToken: tok})
	return &Client{gql: githubv4.NewClient(oauth2.NewClient(ctx, src))}, nil
}

// The watch set is synced in two steps so no single request has to carry
// the whole set: a cheap listing (key + updatedAt only) establishes
// membership, then full payloads are fetched only for PRs whose updatedAt
// moved, in small batches, a bounded number per round. Every request gets its
// own deadline, so a large backlog costs more rounds, never a longer request.
const (
	// listPageSize is safe at the search maximum: listing nodes are a few
	// scalar fields each.
	listPageSize = 100
	// maxListPages stops at GitHub's 1000-result search cap.
	maxListPages = 10
	// batchSize bounds a detail request: each PR carries nested
	// commit/review/comment lists, and much more than this per request makes
	// GitHub's GraphQL gateway time out (502).
	batchSize = 25
	// requestTimeout is the deadline for one listing page or detail batch.
	requestTimeout = 30 * time.Second
	// BatchesPerRound bounds one Sync call to ~100 detail fetches (a
	// handful of seconds); callers re-run Sync while Remaining > 0.
	BatchesPerRound = 4
)

// SyncResult is one round of the incremental sync.
type SyncResult struct {
	Login string
	// Open is the complete current watch set, newest activity first.
	Open []PRRef
	// PRs holds fresh payloads for the stale subset fetched this round.
	PRs []PullRequest
	// Remaining counts stale PRs left for later rounds.
	Remaining int
}

// Sync lists the watch set and fetches full payloads for up to maxBatches
// batches of PRs that are new or whose updatedAt is later than known[key].
// Stale PRs are taken newest-activity first. On a failed detail batch it
// returns the partial result alongside the error, so completed batches are
// not lost.
func (c *Client) Sync(ctx context.Context, known map[string]time.Time, maxBatches int) (*SyncResult, error) {
	login, open, err := c.list(ctx)
	if err != nil {
		return nil, err
	}
	var stale []string
	for _, r := range open {
		if t, ok := known[r.Key()]; !ok || r.UpdatedAt.After(t) {
			stale = append(stale, r.ID)
		}
	}
	res := &SyncResult{Login: login, Open: open, Remaining: len(stale)}
	for i := 0; i < maxBatches && len(stale) > 0; i++ {
		n := min(batchSize, len(stale))
		prs, err := c.fetchBatch(ctx, stale[:n])
		if err != nil {
			return res, err
		}
		res.PRs = append(res.PRs, prs...)
		stale = stale[n:]
		res.Remaining = len(stale)
	}
	return res, nil
}

// list pages through the search with key-and-timestamp nodes only.
// Sorting by updated-desc makes the offset cursor safe against activity
// landing mid-listing: an updated PR jumps to the front, which can only
// repeat an entry across a page boundary, never skip one.
func (c *Client) list(ctx context.Context) (string, []PRRef, error) {
	var (
		login  string
		refs   []PRRef
		seen   = map[string]bool{}
		cursor *githubv4.String
	)
	for range maxListPages {
		var q listQuery
		vars := map[string]any{
			"searchQuery": githubv4.String(SearchQuery + " sort:updated-desc"),
			"pageSize":    githubv4.Int(listPageSize),
			"cursor":      cursor,
		}
		if err := c.query(ctx, &q, vars); err != nil {
			return "", nil, fmt.Errorf("listing watch set: %w", err)
		}
		login = q.Viewer.Login
		for _, n := range q.Search.Nodes {
			r := n.PullRequest
			if r.Number == 0 || seen[r.Key()] {
				continue // non-PR search node, or repeated across pages
			}
			seen[r.Key()] = true
			refs = append(refs, r)
		}
		if !q.Search.PageInfo.HasNextPage {
			break
		}
		cursor = new(githubv4.String(q.Search.PageInfo.EndCursor))
	}
	return login, refs, nil
}

func (c *Client) fetchBatch(ctx context.Context, ids []string) ([]PullRequest, error) {
	gids := make([]githubv4.ID, len(ids))
	for i, id := range ids {
		gids[i] = githubv4.ID(id)
	}
	var q nodesQuery
	if err := c.query(ctx, &q, map[string]any{"ids": gids}); err != nil {
		return nil, fmt.Errorf("fetching %d PRs: %w", len(ids), err)
	}
	prs := make([]PullRequest, 0, len(q.Nodes))
	for _, n := range q.Nodes {
		if n.PullRequest.Number != 0 { // deleted between list and fetch
			prs = append(prs, n.PullRequest)
		}
	}
	return prs, nil
}

func (c *Client) query(ctx context.Context, q any, vars map[string]any) error {
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	return c.gql.Query(ctx, q, vars)
}
