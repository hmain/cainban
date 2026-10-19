package mcp

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/hmain/cainban/src/systems/auth"
	"github.com/hmain/cainban/src/systems/store"
	"github.com/hmain/cainban/src/systems/task"
)

// BadgeCounts is the per-status task tally a status badge renders. It is the
// only data the badge endpoint exposes about a board — three integers, never a
// task title, description, or any other board content — so the endpoint is safe
// to serve PUBLICLY (no auth) for README embedding.
type BadgeCounts struct {
	Todo  int
	Doing int
	Done  int
}

// badgeStyle is a shields.io-style badge shape. flat is the default rounded
// badge; flat-square drops the corner radius and the top gloss gradient; plastic
// keeps the radius and adds a stronger gloss.
type badgeStyle struct {
	radius     string // rx on the clip rect; "0" for flat-square
	gradTop    string // top gradient stop opacity
	gradBottom string // bottom gradient stop opacity
}

// badgeStyles maps the ?style= values to their shape. An unknown value falls
// back to flat (see resolveStyle).
var badgeStyles = map[string]badgeStyle{
	"flat":        {radius: "3", gradTop: ".1", gradBottom: ".1"},
	"flat-square": {radius: "0", gradTop: "0", gradBottom: "0"},
	"plastic":     {radius: "3", gradTop: ".3", gradBottom: ".2"},
}

func resolveStyle(s string) badgeStyle {
	if st, ok := badgeStyles[strings.TrimSpace(s)]; ok {
		return st
	}
	return badgeStyles["flat"]
}

// defaultBadgeColor is the message-rect fill used when no ?color= override is
// given. It is the accent blue the static badges use.
const defaultBadgeColor = "#2563eb"

// namedColors maps a handful of shields.io-style color names to hex. A ?color=
// value not in this map is used verbatim IF it is a safe hex literal
// (resolveColor), so custom hex still works while arbitrary text cannot inject
// markup.
var namedColors = map[string]string{
	"blue":        "#2563eb",
	"green":       "#4cd07d",
	"brightgreen": "#4c1",
	"red":         "#e05d44",
	"orange":      "#fe7d37",
	"yellow":      "#dfb317",
	"gray":        "#555",
	"grey":        "#555",
	"lightgray":   "#9f9f9f",
	"lightgrey":   "#9f9f9f",
}

// resolveColor turns a ?color= value into a safe fill. A known name maps to its
// hex; a bare hex literal (3 or 6 hex digits, with or without a leading #) is
// accepted and normalized to #RRGGBB form; anything else falls back to the
// default. This keeps untrusted query input from reaching the SVG as markup.
func resolveColor(c string) string {
	c = strings.TrimSpace(c)
	if c == "" {
		return defaultBadgeColor
	}
	if hex, ok := namedColors[strings.ToLower(c)]; ok {
		return hex
	}
	h := strings.TrimPrefix(c, "#")
	if (len(h) == 3 || len(h) == 6) && isHex(h) {
		return "#" + h
	}
	return defaultBadgeColor
}

func isHex(s string) bool {
	for _, r := range s {
		switch {
		case r >= '0' && r <= '9':
		case r >= 'a' && r <= 'f':
		case r >= 'A' && r <= 'F':
		default:
			return false
		}
	}
	return len(s) > 0
}

// defaultBadgeLabel is the left-side label when no ?label= override is given.
const defaultBadgeLabel = "cainban"

// charWidth / sidePadding approximate shields.io's text metrics well enough for
// a crisp badge at the standard 20px height: ~6.5px per character plus 10px of
// padding (5px each side) for each of the two halves.
const (
	charWidth   = 6.5
	sidePadding = 10.0
)

// textWidth estimates the rendered pixel width of s at the badge font size.
func textWidth(s string) float64 {
	return float64(len([]rune(s)))*charWidth + sidePadding
}

// escapeText XML-escapes a string for safe inclusion in SVG text content. Label
// comes from an untrusted query param, so it MUST be escaped; the message is
// built from integers but is escaped too for uniformity.
func escapeText(s string) string {
	r := strings.NewReplacer(
		"&", "&amp;",
		"<", "&lt;",
		">", "&gt;",
		"\"", "&quot;",
		"'", "&apos;",
	)
	return r.Replace(s)
}

// RenderBadgeSVG builds a shields.io-style flat badge SVG. label is the
// left (dark) half; the right (colored) half shows "todo|doing|done". style
// selects the badge shape (flat / flat-square / plastic); color overrides the
// message-rect fill. label and color are already-resolved, trusted values here
// (the HTTP handler resolves and escapes them) — but the label is escaped again
// defensively so a direct caller cannot inject markup.
func RenderBadgeSVG(label string, counts BadgeCounts, style, color string) string {
	st := resolveStyle(style)
	fill := resolveColor(color)

	msg := fmt.Sprintf("%d|%d|%d", counts.Todo, counts.Doing, counts.Done)
	labelW := roundUp(textWidth(label))
	msgW := roundUp(textWidth(msg))
	total := labelW + msgW

	labelX := float64(labelW) / 2
	msgX := float64(labelW) + float64(msgW)/2

	esc := escapeText(label)
	escMsg := escapeText(msg)
	aria := esc + ": " + escMsg

	return fmt.Sprintf(`<svg xmlns="http://www.w3.org/2000/svg" width="%d" height="20" role="img" aria-label="%s">
  <title>%s</title>
  <linearGradient id="s" x2="0" y2="100%%">
    <stop offset="0" stop-color="#fff" stop-opacity="%s"/>
    <stop offset="1" stop-opacity="%s"/>
  </linearGradient>
  <clipPath id="r"><rect width="%d" height="20" rx="%s" fill="#fff"/></clipPath>
  <g clip-path="url(#r)">
    <rect width="%d" height="20" fill="#555"/>
    <rect x="%d" width="%d" height="20" fill="%s"/>
    <rect width="%d" height="20" fill="url(#s)"/>
  </g>
  <g fill="#fff" text-anchor="middle" font-family="Verdana,Geneva,DejaVu Sans,sans-serif" font-size="11">
    <text x="%.1f" y="15" fill="#010101" fill-opacity=".3">%s</text>
    <text x="%.1f" y="14">%s</text>
    <text x="%.1f" y="15" fill="#010101" fill-opacity=".3">%s</text>
    <text x="%.1f" y="14">%s</text>
  </g>
</svg>`,
		total, aria,
		aria,
		st.gradTop, st.gradBottom,
		total, st.radius,
		labelW,
		labelW, msgW, fill,
		total,
		labelX, esc,
		labelX, esc,
		msgX, escMsg,
		msgX, escMsg,
	)
}

// roundUp rounds a float width up to the next whole pixel.
func roundUp(f float64) int {
	i := int(f)
	if float64(i) < f {
		i++
	}
	return i
}

// CountFunc returns the task counts for a normalized "owner/repo", or an error
// when the repo cannot be read. It is the single seam the HTTP handler depends
// on, so the handler can be tested without a real store.
type CountFunc func(ctx context.Context, repo string) (BadgeCounts, error)

// NewBadgeHandler returns the PUBLIC (no-auth) badge HTTP handler. It parses
// owner/repo from the request path, fetches counts via countFn, and writes a
// shields.io-style SVG. A repo that cannot be read renders a "not found" badge
// with HTTP 200 (so an <img> embed still shows something) rather than a 404.
//
// The handler is intentionally store-agnostic: NewStatelessBadgeCounter wires
// the real DynamoDB/SQLite-backed counter, while tests pass a stub.
func NewBadgeHandler(countFn CountFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		repo, err := parseBadgePath(r.URL.Path)
		q := r.URL.Query()
		style := q.Get("style")
		color := q.Get("color")
		label := q.Get("label")
		if strings.TrimSpace(label) == "" {
			label = defaultBadgeLabel
		}

		writeSVGHeaders(w)
		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusOK)
			return
		}

		if err != nil {
			// Malformed path: still a 200 SVG so an embed degrades gracefully.
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(RenderBadgeSVG(label, BadgeCounts{}, style, color)))
			return
		}

		counts, cerr := countFn(r.Context(), repo)
		if cerr != nil {
			// Repo not found / unreadable: a "not found" badge, NOT a 404, so a
			// README <img> shows a badge instead of a broken image.
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(renderNotFoundBadge(label, style)))
			return
		}

		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(RenderBadgeSVG(label, counts, style, color)))
	})
}

// renderNotFoundBadge renders a gray "not found" badge for a repo that cannot
// be read. It reuses the label half and shows "not found" in the message half.
func renderNotFoundBadge(label, style string) string {
	st := resolveStyle(style)
	msg := "not found"
	labelW := roundUp(textWidth(label))
	msgW := roundUp(textWidth(msg))
	total := labelW + msgW
	labelX := float64(labelW) / 2
	msgX := float64(labelW) + float64(msgW)/2
	esc := escapeText(label)
	aria := esc + ": " + msg

	return fmt.Sprintf(`<svg xmlns="http://www.w3.org/2000/svg" width="%d" height="20" role="img" aria-label="%s">
  <title>%s</title>
  <linearGradient id="s" x2="0" y2="100%%">
    <stop offset="0" stop-color="#fff" stop-opacity="%s"/>
    <stop offset="1" stop-opacity="%s"/>
  </linearGradient>
  <clipPath id="r"><rect width="%d" height="20" rx="%s" fill="#fff"/></clipPath>
  <g clip-path="url(#r)">
    <rect width="%d" height="20" fill="#555"/>
    <rect x="%d" width="%d" height="20" fill="#9f9f9f"/>
    <rect width="%d" height="20" fill="url(#s)"/>
  </g>
  <g fill="#fff" text-anchor="middle" font-family="Verdana,Geneva,DejaVu Sans,sans-serif" font-size="11">
    <text x="%.1f" y="15" fill="#010101" fill-opacity=".3">%s</text>
    <text x="%.1f" y="14">%s</text>
    <text x="%.1f" y="15" fill="#010101" fill-opacity=".3">%s</text>
    <text x="%.1f" y="14">%s</text>
  </g>
</svg>`,
		total, aria,
		aria,
		st.gradTop, st.gradBottom,
		total, st.radius,
		labelW,
		labelW, msgW,
		total,
		labelX, esc,
		labelX, esc,
		msgX, msg,
		msgX, msg,
	)
}

// writeSVGHeaders sets the content type and cache headers common to every
// badge response. Cache-Control keeps the badge fresh-ish: a short max-age so
// README viewers see near-live counts, with no-cache to defeat intermediary
// caching of a stale count.
func writeSVGHeaders(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "image/svg+xml")
	w.Header().Set("Cache-Control", "no-cache, max-age=60")
	// Badges are embedded cross-origin in README renderers; allow any origin.
	w.Header().Set("Access-Control-Allow-Origin", "*")
}

// BadgePathPrefix is the route prefix the badge endpoint is mounted on. The
// path after it is "<owner>/<repo>.svg".
const BadgePathPrefix = "/badge/"

// parseBadgePath extracts and normalizes the "owner/repo" from a badge request
// path of the form "/badge/<owner>/<repo>.svg". The trailing ".svg" is
// optional (an embedder may drop it). It returns an error for a path that does
// not name a well-formed owner/repo.
func parseBadgePath(p string) (string, error) {
	rest := strings.TrimPrefix(p, BadgePathPrefix)
	if rest == p {
		return "", fmt.Errorf("path %q is not under %s", p, BadgePathPrefix)
	}
	rest = strings.TrimSuffix(rest, ".svg")
	return auth.NormalizeRepo(rest)
}

// HandleBadge is the Server-bound PUBLIC badge handler: it serves a live status
// badge for the repo named in the URL path, reading counts straight from the
// store with NO authentication (badges expose only the three per-status counts,
// nothing else). It is mounted on the PublicMux alongside the well-known
// discovery routes, separate from the authenticated MCP tool routes.
func (s *Server) HandleBadge(w http.ResponseWriter, r *http.Request) {
	NewBadgeHandler(s.badgeCounts).ServeHTTP(w, r)
}

// badgeCounts is the real CountFunc: it opens the store scoped to repo's
// partition prefix (same structural isolation the authenticated path uses) and
// tallies the three statuses. No tenant/token is involved because the counts
// carry no sensitive data; the partition prefix still confines the read to the
// one repo named in the URL.
func (s *Server) badgeCounts(ctx context.Context, repo string) (BadgeCounts, error) {
	dbPath := s.boardSystem.GetBoardPath("default")
	prefix := auth.PartitionPrefixFor(repo)

	ts, closer, err := store.OpenTaskForTenant(context.Background(), dbPath, prefix)
	if err != nil {
		return BadgeCounts{}, err
	}
	defer func() { _ = closer() }()

	const boardID = 1
	todo, err := ts.ListByStatus(boardID, task.StatusTodo)
	if err != nil {
		return BadgeCounts{}, err
	}
	doing, err := ts.ListByStatus(boardID, task.StatusDoing)
	if err != nil {
		return BadgeCounts{}, err
	}
	done, err := ts.ListByStatus(boardID, task.StatusDone)
	if err != nil {
		return BadgeCounts{}, err
	}

	return BadgeCounts{Todo: len(todo), Doing: len(doing), Done: len(done)}, nil
}
