package mcp

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRenderBadgeSVG_ContainsCounts(t *testing.T) {
	svg := RenderBadgeSVG("cainban", BadgeCounts{Todo: 3, Doing: 2, Done: 7}, "flat", "")

	if !strings.HasPrefix(svg, "<svg") {
		t.Fatalf("output is not an SVG: %q", svg[:min(40, len(svg))])
	}
	if !strings.Contains(svg, "3|2|7") {
		t.Errorf("SVG missing count message 3|2|7:\n%s", svg)
	}
	if !strings.Contains(svg, ">cainban<") {
		t.Errorf("SVG missing label text:\n%s", svg)
	}
	// Default color (blue) when no override.
	if !strings.Contains(svg, defaultBadgeColor) {
		t.Errorf("SVG missing default color %s:\n%s", defaultBadgeColor, svg)
	}
}

func TestRenderBadgeSVG_ZeroCounts(t *testing.T) {
	svg := RenderBadgeSVG("cainban", BadgeCounts{}, "flat", "")
	if !strings.Contains(svg, "0|0|0") {
		t.Errorf("zero counts should render 0|0|0:\n%s", svg)
	}
}

func TestRenderBadgeSVG_Styles(t *testing.T) {
	cases := map[string]string{
		"flat":        `rx="3"`,
		"flat-square": `rx="0"`,
		"plastic":     `rx="3"`,
	}
	for style, wantRx := range cases {
		svg := RenderBadgeSVG("cainban", BadgeCounts{Todo: 1}, style, "")
		if !strings.Contains(svg, wantRx) {
			t.Errorf("style %q: expected %s in SVG:\n%s", style, wantRx, svg)
		}
	}
	// Unknown style falls back to flat.
	svg := RenderBadgeSVG("cainban", BadgeCounts{}, "nonsense", "")
	if !strings.Contains(svg, `rx="3"`) {
		t.Errorf("unknown style should fall back to flat (rx=3):\n%s", svg)
	}
}

func TestResolveColor(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"", defaultBadgeColor},
		{"blue", "#2563eb"},
		{"green", "#4cd07d"},
		{"GREEN", "#4cd07d"},
		{"#ff0000", "#ff0000"},
		{"ff0000", "#ff0000"},
		{"abc", "#abc"},
		{"not-a-color", defaultBadgeColor},
		{"<script>", defaultBadgeColor},
		{"12345", defaultBadgeColor}, // wrong length
	}
	for _, c := range cases {
		if got := resolveColor(c.in); got != c.want {
			t.Errorf("resolveColor(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestRenderBadgeSVG_ColorOverride(t *testing.T) {
	svg := RenderBadgeSVG("cainban", BadgeCounts{Todo: 1}, "flat", "green")
	if !strings.Contains(svg, "#4cd07d") {
		t.Errorf("color override green should render #4cd07d:\n%s", svg)
	}
}

func TestRenderBadgeSVG_LabelEscaped(t *testing.T) {
	// A label with markup characters must be XML-escaped, never emitted raw.
	svg := RenderBadgeSVG(`a<b&"c`, BadgeCounts{}, "flat", "")
	if strings.Contains(svg, "a<b") {
		t.Errorf("label markup not escaped:\n%s", svg)
	}
	if !strings.Contains(svg, "&lt;") || !strings.Contains(svg, "&amp;") {
		t.Errorf("expected escaped entities in SVG:\n%s", svg)
	}
}

func TestParseBadgePath(t *testing.T) {
	cases := []struct {
		path    string
		want    string
		wantErr bool
	}{
		{"/badge/hmain/cainban.svg", "hmain/cainban", false},
		{"/badge/hmain/cainban", "hmain/cainban", false},
		{"/badge/octocat/Hello-World.svg", "octocat/Hello-World", false},
		{"/badge/.svg", "", true},
		{"/badge/noslash.svg", "", true},
		{"/notbadge/x/y.svg", "", true},
		{"/badge/a/b/c.svg", "", true}, // too many segments
	}
	for _, c := range cases {
		got, err := parseBadgePath(c.path)
		if c.wantErr {
			if err == nil {
				t.Errorf("parseBadgePath(%q) expected error, got %q", c.path, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("parseBadgePath(%q) unexpected error: %v", c.path, err)
			continue
		}
		if got != c.want {
			t.Errorf("parseBadgePath(%q) = %q, want %q", c.path, got, c.want)
		}
	}
}

func TestBadgeHandler_ServesSVGWithCounts(t *testing.T) {
	countFn := func(_ context.Context, repo string) (BadgeCounts, error) {
		if repo != "hmain/cainban" {
			return BadgeCounts{}, fmt.Errorf("unexpected repo %q", repo)
		}
		return BadgeCounts{Todo: 5, Doing: 1, Done: 9}, nil
	}
	h := NewBadgeHandler(countFn)

	req := httptest.NewRequest(http.MethodGet, "/badge/hmain/cainban.svg", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "image/svg+xml" {
		t.Errorf("Content-Type = %q, want image/svg+xml", ct)
	}
	if cc := rec.Header().Get("Cache-Control"); cc != "no-cache, max-age=60" {
		t.Errorf("Cache-Control = %q, want no-cache, max-age=60", cc)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "5|1|9") {
		t.Errorf("body missing counts 5|1|9:\n%s", body)
	}
}

func TestBadgeHandler_QueryParams(t *testing.T) {
	countFn := func(_ context.Context, _ string) (BadgeCounts, error) {
		return BadgeCounts{Todo: 1, Doing: 2, Done: 3}, nil
	}
	h := NewBadgeHandler(countFn)

	req := httptest.NewRequest(http.MethodGet, "/badge/a/b.svg?style=flat-square&label=tasks&color=green", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	body := rec.Body.String()
	if !strings.Contains(body, `rx="0"`) {
		t.Errorf("style=flat-square should set rx=0:\n%s", body)
	}
	if !strings.Contains(body, ">tasks<") {
		t.Errorf("label=tasks should appear:\n%s", body)
	}
	if !strings.Contains(body, "#4cd07d") {
		t.Errorf("color=green should render #4cd07d:\n%s", body)
	}
}

func TestBadgeHandler_RepoNotFound_RendersBadgeNot404(t *testing.T) {
	countFn := func(_ context.Context, _ string) (BadgeCounts, error) {
		return BadgeCounts{}, errors.New("repo not found")
	}
	h := NewBadgeHandler(countFn)

	req := httptest.NewRequest(http.MethodGet, "/badge/no/such.svg", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("not-found repo should still return 200 (badge), got %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "image/svg+xml" {
		t.Errorf("Content-Type = %q, want image/svg+xml", ct)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "not found") {
		t.Errorf(`not-found badge should contain "not found":\n%s`, body)
	}
}

func TestBadgeHandler_MalformedPath_RendersBadgeNot404(t *testing.T) {
	called := false
	countFn := func(_ context.Context, _ string) (BadgeCounts, error) {
		called = true
		return BadgeCounts{}, nil
	}
	h := NewBadgeHandler(countFn)

	req := httptest.NewRequest(http.MethodGet, "/badge/garbage.svg", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("malformed path should still return 200 (badge), got %d", rec.Code)
	}
	if called {
		t.Error("countFn must not be called for a malformed path")
	}
}

func TestBadgeHandler_MethodNotAllowed(t *testing.T) {
	h := NewBadgeHandler(func(context.Context, string) (BadgeCounts, error) {
		return BadgeCounts{}, nil
	})
	req := httptest.NewRequest(http.MethodPost, "/badge/a/b.svg", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST should be 405, got %d", rec.Code)
	}
}

func TestBadgeHandler_Head(t *testing.T) {
	h := NewBadgeHandler(func(context.Context, string) (BadgeCounts, error) {
		return BadgeCounts{Todo: 1}, nil
	})
	req := httptest.NewRequest(http.MethodHead, "/badge/a/b.svg", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("HEAD should be 200, got %d", rec.Code)
	}
	if rec.Body.Len() != 0 {
		t.Errorf("HEAD should have empty body, got %d bytes", rec.Body.Len())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "image/svg+xml" {
		t.Errorf("HEAD Content-Type = %q, want image/svg+xml", ct)
	}
}
