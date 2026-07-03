# inbox

Terminal dashboard for the PRs you're engaged with — the ones you authored plus
the ones you're reviewing. Frames the workload as an inbox: a PR is "unread"
when the last activity on it is by someone other than you.

## Status

MVP implemented — all six milestones below are done: fetcher + derivation,
interactive TUI, worktree/session detection, `engage`, force-push detection,
adaptive polling. This README remains the design document; deviations
discovered during implementation are folded into the relevant sections.

## Install & run

```
go install github.com/lllamnyp/inbox@latest
```

Auth comes from `$GITHUB_TOKEN` / `$GH_TOKEN`, falling back to
`gh auth token`. The `engage` feature shells out to `git` and `gh`.

```
inbox                          # the TUI
inbox --json                   # one-shot poll, derived state to stdout
inbox --engage owner/repo#123  # set up the worktree for a PR, no TUI
inbox --interval 30s           # foreground poll cadence (default 60s)
inbox --state /tmp/alt.json    # alternate state file (default ~/.local/share/inbox/state.json)
```

Environment: `INBOX_BOTS` extends the bot-login pattern (additive regex);
`INBOX_DEV_ROOT` moves the clone/worktree root (default `~/Cloud/dev` — see
"Why this exists" for the expected layout).

## Why this exists

The workflow this tool is built for:

- Many PRs in flight at once — a mix of PRs I authored (waiting on reviewer
  feedback) and PRs I'm reviewing (waiting on the author to push changes or
  respond to comments). Dozens open at any time is typical.
- Each PR lives in its own git worktree — one primary clone per repository,
  plus one worktree per PR I've actively engaged with. The convention is
  `~/Cloud/dev/<git-server>/<org>/<repo>-<identifier>`, where the identifier
  is usually the PR number.
- Each worktree usually has a [Claude Code](https://claude.com/claude-code)
  session started from its directory, so the model has a fresh context per PR
  and its transcript stays with the work.

That decomposition works well for making progress on any individual PR, but
the cross-PR view falls off a cliff:

- `gh pr list` shows PRs I authored or I'm reviewing, but not which ones are
  actually *waiting on me*. The state is buried in review comment timestamps,
  commit-push times, and unresolved review-thread flags — none of which a
  flat list surfaces.
- Team chat "review please" channels are chronological soup: after twenty
  posts they scroll off, there's no signal for "new activity after my last
  action", and every teammate's post looks the same as every other. Fine for
  announcing work; useless for tracking it.
- GitHub's own notifications inbox tracks unread *events*, not unread PRs.
  It won't collapse a burst of activity into a single "3 new commits + 2
  comments since you last touched this" summary, and its read/unread state
  isn't tied to *your own* last action.
- None of the above knows about the per-PR worktrees or Claude Code sessions
  above them, so switching between PRs is manual: remember the repo, remember
  the branch, `cd` to the right worktree, resume the right session.

`inbox` collapses all of that into one screen: every open PR I'm on, sorted by
"who's blocking whom", with the associated worktree path and Claude Code
session ID next to each row. One keystroke to engage (create the worktree,
check out the PR) on rows I haven't picked up yet; one keystroke to open the
right session on rows I have.

The design is deliberately opinionated about that workflow — it assumes the
one-worktree-per-PR convention and the naming pattern above. If you don't
use that layout, the "Claude Code integration" and `engage` parts won't do
anything useful, but the core inbox view still works standalone.

## Concept

For each open PR that involves you:

- `my_last_action_at` = latest of your commit / review / comment on that PR.
- `their_last_action_at` = latest activity by anyone else (non-bot) on that PR.
- **Waiting on you** iff `their_last_action_at > my_last_action_at` (or the
  head SHA moved when you were the last actor).

That single boolean drives everything else — filters, sort, colors.
Explicit `ack` marks a PR "read" without changing GitHub state; new activity
re-arms it.

**Muting CODEOWNERS-only review requests.** In repos with a broad CODEOWNERS
file, *every* PR review-requests everyone whose paths it touches — and
`involves:@me` dutifully returns them all, as noise. GraphQL exposes the
distinction: `ReviewRequest.asCodeOwner` says whether the request came from
CODEOWNERS matching or from a human picking you. Rule: a PR is **muted**
while your *only* involvement is a code-owner review request — no direct
request, no assignment, no action of yours. Muted rows appear only under the
`7:muted` filter tab and never count as waiting-on-you. Your first real
action on one (comment, review, commit) un-mutes it permanently — being
matched by CODEOWNERS is not engagement, acting is. Involvement the query
can't see (an @-mention, say) leaves the PR visible: mute only on positive
evidence. An earlier iteration muted on *bot-authored* instead; that was the
wrong axis — the release bot's PRs are noisy for the same reason every other
PR is (blanket code-owner requests), and human PRs are just as noisy.

## Scope: what to track

GitHub search: `involves:@me is:pr is:open archived:false` — returns exactly the
set of PRs where you're author, reviewer, review-requested, or have commented.
No manual list, no per-repo config.

## Stack

- **TUI**: [`charmbracelet/bubbletea`][bt] (Elm-style) + [`lipgloss`][lg]
  (styles/colors). Table rows are rendered by hand rather than through
  [`bubbles/table`][bb] — the design needs per-cell conditional colors (dot,
  since-age, role) *and* a selected-row highlight, and embedded ANSI inside
  table cells breaks the widget's row-highlight styling. `bubbles` is still
  used for the `/` search textinput.
- **GitHub**: [`shurcooL/githubv4`][ghv4] typed GraphQL client. One query per
  poll fetches the whole set. Auth via `$GITHUB_TOKEN` or shelling out to
  `gh auth token`.
- **State**: `encoding/json` + atomic write (`os.CreateTemp` → `os.Rename`).
- **Config**: `os.UserHomeDir()` for the state path.

[bt]: https://github.com/charmbracelet/bubbletea
[lg]: https://github.com/charmbracelet/lipgloss
[bb]: https://github.com/charmbracelet/bubbles
[ghv4]: https://github.com/shurcooL/githubv4

## Layout

```
inbox/
├── go.mod
├── main.go                 # entrypoint, flag parsing, wiring
├── internal/
│   ├── ghclient/           # GraphQL query + fetch
│   │   ├── query.go
│   │   └── fetch.go
│   ├── state/              # JSON persist (load/save/migrate)
│   │   └── store.go
│   ├── derive/             # my/their-last-action, waiting-on, new_since_me
│   │   └── derive.go
│   ├── worktree/           # PR ↔ worktree ↔ claude session mapping
│   │   ├── resolve.go
│   │   └── engage.go
│   └── tui/                # bubbletea program
│       ├── model.go
│       ├── update.go
│       ├── view.go
│       └── styles.go       # lipgloss color palette
└── README.md
```

## Data model

`~/.local/share/inbox/state.json`:

```json
{
  "my_login": "lllamnyp",
  "last_poll_at": "2026-07-03T09:14:22Z",
  "prs": {
    "aenix-org/cozyportal#867": {
      "url": "https://github.com/aenix-org/cozyportal/pull/867",
      "title": "feat(console): typed managed-services API...",
      "author": "kvaps",
      "state": "OPEN",
      "is_draft": true,
      "role": "reviewer",
      "head_sha": "abc123...",
      "review_decision": "REVIEW_REQUIRED",
      "my_last_action_at": "2026-07-01T14:03:00Z",
      "my_last_action_kind": "review",
      "their_last_action_at": "2026-07-02T20:47:12Z",
      "their_last_action_kind": "commit",
      "new_since_me": { "commits": 3, "reviews": 0, "comments": 2 },
      "acknowledged_at": null,
      "snoozed_until": null,
      "worktree_path": "/home/lllamnyp/Cloud/dev/github.com/aenix-org/cozyportal-867",
      "claude_session_id": "0904d366-840d-4b3b-885e-417c8a0347b7"
    }
  }
}
```

## GitHub — one GraphQL query per poll

```graphql
query {
  search(query: "involves:@me is:pr is:open archived:false", type: ISSUE, first: 100) {
    nodes { ... on PullRequest {
      number url title isDraft state reviewDecision headRefOid updatedAt
      repository { nameWithOwner }
      author { login }
      reviewRequests(first: 20) { nodes { requestedReviewer { ... on User { login } } } }
      commits(last: 30)  { nodes { commit { committedDate oid author { user { login } } } } }
      reviews(last: 30)  { nodes { author { login } submittedAt state } }
      comments(last: 50) { nodes { author { login } createdAt } }
      reviewThreads(last: 30) { nodes { isResolved comments(last: 10) { nodes { author { login } createdAt } } } }
    }}
  }
}
```

One search per poll, but paginated at 25 nodes per page: with the nested
commit/review/comment lists, pages of 100 make GitHub's GraphQL gateway time
out with 502s (found empirically at a 155-PR watch set — ~7 pages, ~26s).
Still a handful of rate-limit points per poll, ≪ 5000/hr GraphQL budget.
Bots filtered in code — reuse the pattern from `vibe-project-management`'s
scrape: `gemini|coderabbit|netlify|[bot]|dependabot|renovate`. Override with
`$INBOX_BOTS`.

## Derivation

```go
type Actor struct { Login string; At time.Time }

func lastAction(actors []Actor, filter func(string) bool) *Actor { ... }

my    := lastAction(all,  isMe)
their := lastAction(all,  and(not(isMe), not(isBot)))

waitingOnMe := their != nil && (my == nil || their.At.After(my.At))
```

Force-push detection: cache prior `headRefOid`; if it changes and the new tip's
`committedDate` is older than the previous tip's, flag `force-pushed` (bumps
`new_since_me.commits`). If I'm the pusher, don't flag.

## Polling loop

- Foreground default: 60s. Background: 5 min. Adaptive back-off to 5 min if the
  search's `updatedAt` set is unchanged for N consecutive polls; snap back to
  60s on any change.
- `r` key forces an immediate poll.
- Poll runs in a goroutine that publishes new state via a `tea.Cmd`; the main
  loop never blocks on the network.

## TUI

Bubbletea model with a `bubbles/table.Model` as the primary widget.
`lipgloss` for the ANSI palette:

| Style | Where |
|---|---|
| bold red dot `●` | waiting on you |
| dim `○` | waiting on them |
| grey `⋯` | snoozed |
| green | `since` cell < 24h |
| yellow | `since` cell 1–3d |
| red | `since` cell > 3d |
| cyan | column headers |
| magenta | selected row highlight |
| dim grey | `role: author` in your own PRs |
| bright | `role: reviewer` where you're on the hook |

Rough sketch:

```
┌ inbox — 27 open · 8 waiting on you · last poll 42s ago ────────────────────┐
│ Filter: [all] mine  reviewing  waiting-you  waiting-them  snoozed          │
│                                                                            │
│   repo#pr    title                              role   since   new   ⌘     │
│  ● cs#3017    feat(release): immutable tags…    rev    3h     2c 1r  ⎇    │
│  ● cp#867     feat(console): typed managed…     rev    1d     5c            │
│  ○ cp#961     docs(security): at-rest guide     auth   6h     —      ⎇ ⌗   │
│  ○ cs#2610    feat(k8s): Talos CABPT            auth   2d     —              │
│  ⋯ cp#883     stub design for Group lifecycle   auth   14d    (snoozed)      │
└────────────────────────────────────────────────────────────────────────────┘
  ↑↓ nav · enter web · o gh · e engage · a ack · s snooze · r poll · q quit
```

- `⎇` = worktree exists; `⌗` = live Claude Code session detected.
- A `who` column (not in the sketch above) names the counterpart — whoever
  acted last on their side: the reviewer sending comments on your PR, or the
  author pushing fixes on theirs. The `i` detail pane carries the full
  breakdown: PR author plus every attributed non-bot participant with an
  activity count (`androndo×6 lexfrei×5`).
- Default sort: waiting-on-you first, then `their_last_action_at` desc.
- Filters as keybinds `1`..`7` (all / mine / reviewing / waiting-you /
  waiting-them / snoozed / muted). Muted rows show only under `7`.
- `/` toggles a substring filter over `repo#pr` + `title`.

## Claude Code integration

Every worked-on PR conventionally lives in a worktree at
`~/Cloud/dev/<git-server>/<org>/<repo>-<identifier>`. The identifier is
*usually* the PR number — but worktrees started before the PR existed carry a
feature name instead (`cozyportal-freedompay`, not `cozyportal-867`), so
resolution runs in two steps:

1. Scan the `<repo>-*` siblings and match each worktree's checked-out branch
   against the PR's `headRefName` — branch equality is the strongest
   evidence and covers numbered and feature-named worktrees alike. No stored
   mapping needed: git already records both halves. The scan is pure file
   reads — a linked worktree's `.git` is a *file* containing
   `gitdir: <primary>/.git/worktrees/<name>`, which yields the checked-out
   branch (that gitdir's `HEAD`) and proves repo identity in the same step:
   a same-prefix sibling that is its own repository (`cozyportal-ui` next to
   `cozyportal`) has a `.git` directory and can never match. One scan per
   repository per poll, memoized.
2. Fall back to `<repo>-<num>` — but the directory name alone is weak
   evidence (the identifier might be an external ticket number, or an issue
   number in another repo; *same-repo* issue numbers can't collide since
   issues and PRs share one number sequence). The numbered path attaches
   only when its git state doesn't contradict the PR: detached HEAD
   (mid-`engage`), the PR's head branch, or `gh pr checkout`'s
   fork-collision-prefixed `owner/branch` variant. A numbered worktree
   sitting on an unrelated branch is treated as not this PR's worktree, and
   `engage` refuses it rather than reusing or overwriting it.

Claude Code stores per-project sessions under
`~/.claude/projects/<encoded-cwd>/`. The encoding replaces every character
outside `[a-zA-Z0-9]` with `-` (not just `/` — dots too), so an absolute path
like `/home/lllamnyp/Cloud/dev/github.com/foo/bar-42` becomes
`-home-lllamnyp-Cloud-dev-github-com-foo-bar-42` (note `github-com`). Each
session is a `<uuid>.jsonl` file whose first line is a metadata record and
whose subsequent lines are the individual turns. Resolution: encode the
worktree path, then pick the `.jsonl` in the resulting directory with the
newest mtime.

Freshness heuristic: treat a session as "live-looking" if its `mtime` is
within the last hour; otherwise treat it as "resumable but idle". No attempt
to detect a running Claude Code process — process detection is fragile across
IDE integrations and remote hosts, and the mtime signal is enough for the
"do I have an in-flight session on this PR" question.

Both attach to the PR row: worktree path and session UUID. Two icon columns
(`⎇` worktree exists, `⌗` recent session exists) so the whole `session-id/path`
string doesn't dominate the layout; press `i` on a row to open a footer detail
pane with the full strings.

## `engage` command

`e` on a selected row (or `--engage <owner>/<repo>#<num>` at launch) runs:

```
1. Determine paths:
     PRIMARY = ~/Cloud/dev/<git-server>/<org>/<repo>
     WORKTREE = ~/Cloud/dev/<git-server>/<org>/<repo>-<num>

2. If PRIMARY missing: `gh repo clone <owner>/<repo> <PRIMARY>` first.

3. `git -C <PRIMARY> fetch origin`
4. `git -C <PRIMARY> worktree add --detach <WORKTREE>`
5. `cd <WORKTREE> && gh pr checkout <num>`   # creates a local branch tracking the PR head

6. Print the worktree path. Do NOT auto-launch `claude` — that decision is
   yours (you may want a fresh session vs resuming an old one).

7. On the next poll, the row now shows the ⎇ marker and any existing session.
```

Collision handling: if a sibling worktree already holds the PR's head branch
(a feature-named worktree from before the PR existed), report that path and
create nothing. If `<WORKTREE>` already exists, skip step 4 and just
report the path — the user probably already has a session there. If a
different repo already occupies the sibling-named path (see the CLAUDE.md note
about `cozyportal-ui`), refuse and print the actual `git remote -v`.

Cross-server PRs are handled by keeping `<git-server>` derived from the PR
URL's host, not hardcoded to `github.com`.

## Design notes

Choices worth spelling out so a future reader (or a fresh session picking up
the code) doesn't have to re-derive them.

- **Why GraphQL, not REST.** One `search` query returns commits, reviews,
  comments and threads for the entire watched set in a single atomic snapshot.
  Per-PR REST would need N × ~4 calls per poll, races against activity
  landing mid-poll, and would burn most of the 5000/hr REST budget on a
  larger watch set. GraphQL rate cost is ~1–5 points per query.

- **Why `acknowledged_at` instead of a `read` boolean.** A boolean loses
  information the next time activity lands: if `read=true` sticks and a new
  comment arrives, do we auto-flip it? What if two arrive back-to-back?
  Storing the ack *timestamp* lets the derivation stay pure — the PR is
  "unread again" the moment `their_last_action_at > acknowledged_at`. No
  imperative state machine, no flags to reconcile.

- **Why cache `head_sha`.** Committer timestamps lie under rebase, cherry-pick,
  and `git commit --amend` — a fresh push can carry an old `committedDate`
  because the commit's authored/committed time was preserved. Comparing the
  cached head SHA against the fresh one catches every push including
  force-pushes, and is the only reliable way to distinguish "they pushed a
  new revision" from "nothing happened".

- **Why only count activity, not persist events.** The inbox cares about
  *state*, not history — "how many things happened since I last acted" is a
  scalar. Storing every event would grow the JSON without upside; the badge
  count is enough to decide whether to open the PR. Full history lives on
  GitHub.

- **Why not auto-launch `claude` from `engage`.** A live worktree may already
  have a session I'd want to resume, or I may want a fresh session with clean
  context. Both are legitimate; the tool prints the path and lets me choose,
  rather than guessing and getting it wrong half the time.

- **Why one JSON file, not SQLite.** The dataset is tiny (dozens of PRs, tens
  of KB), the access pattern is "read once at startup, rewrite on every poll",
  and human-readability during debugging is a feature. If the watch set grows
  past a few hundred PRs or per-event history becomes interesting to store,
  SQLite becomes the right move — for the MVP it would be over-engineered.

- **Why bots are filtered in the client, not the query.** GraphQL's search
  doesn't let you exclude specific author logins from *within* a PR's
  comment/review lists — the filter has to happen after the payload comes
  back. Doing it in code also makes the bot list configurable per user.

## Nice-to-haves (later)

- Desktop notification (`notify-send`) when the `waiting-on-you` count crosses
  zero-to-nonzero. Silence via env var.
- `--json` output mode so you can pipe the state into other tooling.
- Per-repo overrides (`~/.config/inbox/rules.toml`): auto-snooze docs PRs,
  route certain repos to a different worktree parent, etc.
- Resume mapping: if a session's `.jsonl` mentions the PR URL in its transcript,
  it's associated with that PR even if the worktree naming doesn't match. Cheap
  grep pass at poll time.

## MVP milestones

1. **Fetcher + derivation** — GraphQL query + `derive` package + JSON dump.
   Verify manually against 5–10 PRs whose state you already know. ~1–2h.
2. **Static table print** — `lipgloss.Table` (or `bubbles/table` in
   auto-render), no interactivity. Confirms columns feel right. ~30m.
3. **Interactive TUI** — bubbletea program with poll loop, keybinds for nav
   / ack / snooze / filter. ~3–4h.
4. **Worktree + session detection** — resolve paths, add the `⎇` / `⌗`
   markers, detail pane on `i`. ~1h.
5. **`engage`** — the shell-out sequence above. ~1h.
6. **Polish** — force-push detection, adaptive polling, bot filter, colors
   tuned to your terminal. ~1h.

Total: about a day of focused work to something you'd run every day.
