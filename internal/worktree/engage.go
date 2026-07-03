package worktree

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

// remoteRepo extracts "owner/repo" from a git remote URL, handling both
// https and ssh forms. Exact comparison matters: "foo/bar" must not match a
// remote for "foo/bar-ui".
func remoteRepo(remote string) string {
	r := strings.TrimSpace(remote)
	r = strings.TrimSuffix(r, "/")
	r = strings.TrimSuffix(r, ".git")
	r = strings.ReplaceAll(r, ":", "/") // git@host:owner/repo -> git@host/owner/repo
	parts := strings.Split(r, "/")
	if len(parts) < 2 {
		return ""
	}
	return parts[len(parts)-2] + "/" + parts[len(parts)-1]
}

func sameRepo(dir, repoWithOwner string) bool {
	out, err := exec.Command("git", "-C", dir, "remote", "get-url", "origin").Output()
	if err != nil {
		return false
	}
	return strings.EqualFold(remoteRepo(string(out)), repoWithOwner)
}

func run(out io.Writer, dir string, env []string, name string, args ...string) error {
	fmt.Fprintf(out, "$ %s %s\n", name, strings.Join(args, " "))
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), env...)
	cmd.Stdout = out
	cmd.Stderr = out
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s %s: %w", name, strings.Join(args, " "), err)
	}
	return nil
}

// Engage sets up the per-PR worktree: clone the primary if missing, fetch,
// add a detached worktree, check out the PR head. It prints the worktree
// path and deliberately does NOT launch claude — fresh session vs resume is
// the user's call. headRef (the PR's head branch, may be empty) lets a
// feature worktree named before the PR existed win over creating a numbered
// twin.
func Engage(prURL, repoWithOwner string, number int, headRef string, out io.Writer) (string, error) {
	primary, wt := Paths(prURL, repoWithOwner, number)
	host := hostOf(prURL)
	var env []string
	if host != "github.com" {
		env = []string{"GH_HOST=" + host}
	}

	if headRef != "" && !Exists(wt) {
		if path, ok := NewScanner().branchWorktrees(primary)[headRef]; ok {
			fmt.Fprintf(out, "worktree already exists (branch %s): %s\n", headRef, path)
			return path, nil
		}
	}

	if _, err := os.Stat(wt); err == nil {
		if !Exists(wt) {
			return "", fmt.Errorf("%s exists but is not a git worktree", wt)
		}
		if !sameRepo(wt, repoWithOwner) {
			// The naming convention is not collision-free (cozyportal-ui is
			// its own repo, not a worktree of cozyportal). Refuse and show
			// what actually lives there.
			remotes, _ := exec.Command("git", "-C", wt, "remote", "-v").CombinedOutput()
			return "", fmt.Errorf("%s already exists but tracks a different repo:\n%s", wt, strings.TrimSpace(string(remotes)))
		}
		if headRef != "" {
			if branch, linked := worktreeInfo(wt, primary); linked && !branchCompatible(branch, headRef) {
				// Right repo, wrong branch: the number on the directory
				// refers to something other than this PR.
				return "", fmt.Errorf("%s is checked out on %q, not the PR's head %q — it likely holds unrelated work; refusing", wt, branch, headRef)
			}
		}
		fmt.Fprintf(out, "worktree already exists: %s\n", wt)
		return wt, nil
	}

	if !Exists(primary) {
		spec := repoWithOwner
		if host != "github.com" {
			spec = "https://" + host + "/" + repoWithOwner
		}
		if err := run(out, "", env, "gh", "repo", "clone", spec, primary); err != nil {
			return "", err
		}
	} else if !sameRepo(primary, repoWithOwner) {
		remotes, _ := exec.Command("git", "-C", primary, "remote", "-v").CombinedOutput()
		return "", fmt.Errorf("%s tracks a different repo:\n%s", primary, strings.TrimSpace(string(remotes)))
	}

	if err := run(out, "", env, "git", "-C", primary, "fetch", "origin"); err != nil {
		return "", err
	}
	if err := run(out, "", env, "git", "-C", primary, "worktree", "add", "--detach", wt); err != nil {
		return "", err
	}
	if err := run(out, wt, env, "gh", "pr", "checkout", strconv.Itoa(number)); err != nil {
		return "", err
	}
	fmt.Fprintf(out, "engaged: %s\n", wt)
	return wt, nil
}
