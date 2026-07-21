// inbox is a terminal dashboard for the PRs you're engaged with, framed as an
// inbox: a PR is "unread" when the last activity on it is by someone other
// than you. See README.md — it is the design document.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/lllamnyp/inbox/internal/derive"
	"github.com/lllamnyp/inbox/internal/ghclient"
	"github.com/lllamnyp/inbox/internal/state"
	"github.com/lllamnyp/inbox/internal/tui"
	"github.com/lllamnyp/inbox/internal/worktree"
)

func main() {
	var (
		jsonMode  = flag.Bool("json", false, "poll once, print the derived state as JSON to stdout, and exit")
		engageRef = flag.String("engage", "", "engage a PR (owner/repo#num or PR URL) and exit")
		interval  = flag.Duration("interval", time.Minute, "foreground poll interval")
		statePath = flag.String("state", "", "state file path (default: ~/.local/share/inbox/state.json)")
	)
	flag.Parse()

	if *engageRef != "" {
		prURL, repo, num, err := parsePRRef(*engageRef)
		die(err)
		// No PR payload here, so no head branch to match named worktrees by;
		// the numbered convention still applies.
		wt, err := worktree.Engage(prURL, repo, num, "", os.Stdout)
		die(err)
		fmt.Println(wt)
		return
	}

	path := *statePath
	if path == "" {
		p, err := state.DefaultPath()
		die(err)
		path = p
	}
	st, err := state.Load(path)
	die(err)

	isBot := derive.BotMatcher(botPattern())
	ctx := context.Background()
	client, err := ghclient.New(ctx)
	die(err)

	if *jsonMode {
		login, prs, err := client.Fetch(ctx)
		die(err)
		st.MyLogin = login
		st.LastPollAt = time.Now()
		st.PRs = derive.ApplyAll(st.PRs, login, prs, isBot, time.Now())
		sc := worktree.NewScanner()
		ix := worktree.NewSessionIndex()
		ix.Refresh()
		for _, p := range st.PRs {
			sc.Annotate(p, ix)
		}
		if err := st.Save(path); err != nil {
			fmt.Fprintln(os.Stderr, "inbox: warning: state not saved:", err)
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		die(enc.Encode(st))
		return
	}

	m := tui.New(st, path, client, isBot, *interval)
	if _, err := tea.NewProgram(m, tea.WithAltScreen()).Run(); err != nil {
		die(err)
	}
}

// botPattern extends (not replaces) the default: nobody ever wants to
// un-filter dependabot, they want to add their org's release bot.
func botPattern() string {
	if v := os.Getenv("INBOX_BOTS"); v != "" {
		return derive.DefaultBotPattern + "|" + v
	}
	return derive.DefaultBotPattern
}

// parsePRRef accepts "owner/repo#123" (assumes github.com) or a full PR URL
// like "https://host/owner/repo/pull/123".
func parsePRRef(s string) (prURL, repo string, num int, err error) {
	if strings.Contains(s, "://") {
		u, err := url.Parse(s)
		if err != nil {
			return "", "", 0, err
		}
		parts := strings.Split(strings.Trim(u.Path, "/"), "/")
		if len(parts) < 4 || parts[2] != "pull" {
			return "", "", 0, fmt.Errorf("not a PR URL: %s", s)
		}
		num, err = strconv.Atoi(parts[3])
		if err != nil {
			return "", "", 0, fmt.Errorf("not a PR URL: %s", s)
		}
		return s, parts[0] + "/" + parts[1], num, nil
	}
	repoPart, numPart, ok := strings.Cut(s, "#")
	if !ok || strings.Count(repoPart, "/") != 1 {
		return "", "", 0, fmt.Errorf("expected owner/repo#num or a PR URL, got %q", s)
	}
	num, err = strconv.Atoi(numPart)
	if err != nil {
		return "", "", 0, fmt.Errorf("bad PR number in %q", s)
	}
	return "https://github.com/" + repoPart + "/pull/" + numPart, repoPart, num, nil
}

func die(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "inbox:", err)
		os.Exit(1)
	}
}
