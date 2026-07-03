// Package ghclient fetches the watched PR set with one GraphQL search query.
package ghclient

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"

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

// pageSize trades round-trips for query cost: each PR carries nested
// commit/review/comment lists, and pages much larger than this make GitHub's
// GraphQL gateway time out (502).
const pageSize = 25

// maxPages caps the watch set; beyond this the inbox framing has already
// lost, no point hammering the API.
const maxPages = 10

// Fetch pages through the search query and returns the viewer login plus all
// PRs. Rate cost is one point per page — noise against the 5000/hr budget.
func (c *Client) Fetch(ctx context.Context) (string, []PullRequest, error) {
	var (
		login  string
		prs    []PullRequest
		cursor *githubv4.String
	)
	for range maxPages {
		var q searchQuery
		vars := map[string]any{
			"searchQuery": githubv4.String(SearchQuery),
			"pageSize":    githubv4.Int(pageSize),
			"cursor":      cursor,
		}
		if err := c.gql.Query(ctx, &q, vars); err != nil {
			return "", nil, err
		}
		login = q.Viewer.Login
		for _, n := range q.Search.Nodes {
			if n.PullRequest.Number == 0 {
				continue // non-PR search node
			}
			prs = append(prs, n.PullRequest)
		}
		if !q.Search.PageInfo.HasNextPage {
			break
		}
		cursor = new(githubv4.String(q.Search.PageInfo.EndCursor))
	}
	return login, prs, nil
}
