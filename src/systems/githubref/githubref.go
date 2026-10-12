// Package githubref derives a card's GitHub linkage from its text, with no new
// storage and no new write path.
//
// A cainban card (task.Task) carries only Title and Description. The board is
// already scoped to one authorized GitHub repo (owner/repo), so a reference the
// card's text makes to that repo's issues or pull requests can be recovered by
// parsing — exactly the way GitHub itself cross-links "#123" in a comment. This
// package is that parser: a pure function of (text, repo) that returns at most
// one primary reference plus a resolved github.com URL.
//
// Scope (Tier 1, read-only): detect a reference and build its URL. It writes
// nothing to GitHub and reads nothing from the GitHub API — the live issue/PR
// state (open/closed, checks) is a later tier. Keeping it a pure parser means
// it needs no credential, no network, and no change to the agent-facing MCP
// tool contract: the derived reference rides along as an extra read-only output
// field on the task.
package githubref

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// Kind distinguishes an issue reference from a pull-request reference. A bare
// "#123" is ambiguous on GitHub (issues and PRs share one number space), so it
// is reported as KindIssue — github.com/<repo>/issues/<n> redirects to the PR
// when the number is actually a PR, so the issue URL is always a valid landing
// page.
type Kind string

const (
	KindIssue Kind = "issue"
	KindPR    Kind = "pr"
)

// Ref is a single GitHub reference derived from a card's text. It is the shape
// that rides along on task output as the optional github_ref field.
type Ref struct {
	// Kind is "issue" or "pr".
	Kind Kind `json:"kind"`
	// Number is the issue / PR number (always > 0).
	Number int `json:"number"`
	// Repo is the owner/repo the reference resolves against (the card's board
	// repo, or an explicit owner/repo named in the text).
	Repo string `json:"repo"`
	// URL is the canonical github.com page for the reference.
	URL string `json:"url"`
}

// hostGuard anchors the scheme so "github.com" must be the actual host, not a
// trailing label of a hostile host (evilgithub.com) or a path segment of
// another host (https://evil.com/github.com/...). RE2 has no lookbehind, so the
// guard is a leading group m[1] that matches start-of-text or a character that
// cannot be part of a hostname — the owner/repo/number captures therefore shift
// to m[2]/m[3]/m[4].
const hostGuard = `(^|[^\w.-])`

// prURLRe matches a full GitHub pull-request URL anywhere in the text, capturing
// owner, repo and number. Host is fixed to github.com (Tier 1 is github.com
// only, matching the GitHub App's scope).
var prURLRe = regexp.MustCompile(
	hostGuard + `https?://github\.com/([A-Za-z0-9_.-]+)/([A-Za-z0-9_.-]+)/pull/(\d+)`,
)

// issueURLRe matches a full GitHub issue URL anywhere in the text.
var issueURLRe = regexp.MustCompile(
	hostGuard + `https?://github\.com/([A-Za-z0-9_.-]+)/([A-Za-z0-9_.-]+)/issues/(\d+)`,
)

// qualifiedRe matches a cross-repo short reference "owner/repo#123".
var qualifiedRe = regexp.MustCompile(
	`\b([A-Za-z0-9_.-]+)/([A-Za-z0-9_.-]+)#(\d+)\b`,
)

// bareRe matches a bare "#123" reference. It requires the '#' to start a token
// (not be glued to a preceding word character) so "ab#1" / a URL fragment does
// not match, while "#12", "(#12)", "see #12" do.
var bareRe = regexp.MustCompile(`(^|[^\w/])#(\d+)\b`)

// Parse derives the PRIMARY GitHub reference from a card's title and
// description, resolving bare references against boardRepo (the card's
// board-scoped owner/repo, e.g. "octo/cli"). It returns (nil, false) when the
// text carries no recognizable reference, or when a bare "#123" is found but
// boardRepo is empty (nothing to resolve it against — the local CLI path).
//
// Precedence, most specific first, because a card that pastes a full PR URL
// means that PR more precisely than an incidental "#123" in prose:
//  1. a full pull-request URL,
//  2. a full issue URL,
//  3. a qualified "owner/repo#123",
//  4. a bare "#123" resolved against boardRepo.
//
// Only ONE reference is returned (the primary). Tier 1 shows a single chip per
// card; surfacing every number in a long description would be noise.
func Parse(title, description, boardRepo string) (*Ref, bool) {
	text := title + "\n" + description

	if m := prURLRe.FindStringSubmatch(text); m != nil {
		return makeRef(KindPR, m[2]+"/"+m[3], m[4]), true
	}
	if m := issueURLRe.FindStringSubmatch(text); m != nil {
		return makeRef(KindIssue, m[2]+"/"+m[3], m[4]), true
	}
	if m := qualifiedRe.FindStringSubmatch(text); m != nil {
		return makeRef(KindIssue, m[1]+"/"+m[2], m[3]), true
	}
	if m := bareRe.FindStringSubmatch(text); m != nil {
		repo := strings.TrimSpace(boardRepo)
		if repo == "" {
			// Nothing to resolve a bare reference against.
			return nil, false
		}
		return makeRef(KindIssue, repo, m[2]), true
	}
	return nil, false
}

// makeRef builds a Ref, parsing the captured number string (already \d+ from
// the regex, so Atoi cannot fail) and composing the canonical github.com URL.
func makeRef(kind Kind, repo, numStr string) *Ref {
	n, _ := strconv.Atoi(numStr)
	seg := "issues"
	if kind == KindPR {
		seg = "pull"
	}
	return &Ref{
		Kind:   kind,
		Number: n,
		Repo:   repo,
		URL:    fmt.Sprintf("https://github.com/%s/%s/%d", repo, seg, n),
	}
}
