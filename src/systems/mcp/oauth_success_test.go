package mcp

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestOAuthSuccessPageContent verifies the success page contains required
// guidance for the user: a success message, instruction to close the tab,
// and a reminder about new sessions.
func TestOAuthSuccessPageContent(t *testing.T) {
	h := NewOAuthSuccessHandler()
	req := httptest.NewRequest(http.MethodGet, "/oauth/callback?code=abc123&state=xyz", nil)
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}

	body := rec.Body.String()

	// Must indicate success
	if !strings.Contains(body, "✅") && !strings.Contains(body, "success") && !strings.Contains(body, "Success") {
		t.Error("response body should indicate success (✅ or 'success')")
	}

	// Must tell user to close the tab
	if !strings.Contains(strings.ToLower(body), "close") {
		t.Error("response body should tell user to close the tab")
	}

	// Must mention new session (the MCP "new session to load servers" gotcha)
	if !strings.Contains(strings.ToLower(body), "session") {
		t.Error("response body should mention starting a new session")
	}
}

// TestOAuthSuccessPageContentType verifies the page is served as HTML.
func TestOAuthSuccessPageContentType(t *testing.T) {
	h := NewOAuthSuccessHandler()
	req := httptest.NewRequest(http.MethodGet, "/oauth/callback", nil)
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	ct := rec.Header().Get("Content-Type")
	if !strings.HasPrefix(ct, "text/html") {
		t.Errorf("Content-Type = %q, want text/html", ct)
	}
}

// TestOAuthSuccessPageIsValidHTML verifies minimal HTML structure.
func TestOAuthSuccessPageIsValidHTML(t *testing.T) {
	h := NewOAuthSuccessHandler()
	req := httptest.NewRequest(http.MethodGet, "/oauth/callback", nil)
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	body := rec.Body.String()

	// Check for basic HTML structure
	if !strings.Contains(body, "<!DOCTYPE html>") && !strings.Contains(body, "<!doctype html>") {
		t.Error("response should be valid HTML with DOCTYPE")
	}
	if !strings.Contains(body, "<html") {
		t.Error("response should contain <html> tag")
	}
	if !strings.Contains(body, "<head") {
		t.Error("response should contain <head> tag")
	}
	if !strings.Contains(body, "<body") {
		t.Error("response should contain <body> tag")
	}
	if !strings.Contains(body, "</html>") {
		t.Error("response should contain closing </html> tag")
	}
}

// TestOAuthSuccessPageTitle verifies the page has a descriptive title.
func TestOAuthSuccessPageTitle(t *testing.T) {
	h := NewOAuthSuccessHandler()
	req := httptest.NewRequest(http.MethodGet, "/oauth/callback", nil)
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	body := rec.Body.String()

	// Should have a title tag with cainban or success
	if !strings.Contains(strings.ToLower(body), "<title>") {
		t.Error("response should contain <title> tag")
	}
	if !strings.Contains(strings.ToLower(body), "cainban") {
		t.Error("title should mention cainban")
	}
}

// TestOAuthSuccessPageMethodNotAllowed verifies POST is rejected.
func TestOAuthSuccessPageMethodNotAllowed(t *testing.T) {
	h := NewOAuthSuccessHandler()
	req := httptest.NewRequest(http.MethodPost, "/oauth/callback", nil)
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want 405 for POST", rec.Code)
	}
}

// TestOAuthSuccessPageMentionsCainban verifies branding is present.
func TestOAuthSuccessPageMentionsCainban(t *testing.T) {
	h := NewOAuthSuccessHandler()
	req := httptest.NewRequest(http.MethodGet, "/oauth/callback", nil)
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	body := rec.Body.String()
	if !strings.Contains(strings.ToLower(body), "cainban") {
		t.Error("response body should mention cainban")
	}
}

// TestOAuthSuccessPageHasNextSteps verifies the page provides actionable guidance.
func TestOAuthSuccessPageHasNextSteps(t *testing.T) {
	h := NewOAuthSuccessHandler()
	req := httptest.NewRequest(http.MethodGet, "/oauth/callback", nil)
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	body := strings.ToLower(rec.Body.String())

	// Should tell user what to do next: return to agent, run list_tasks, etc.
	hasNextStep := strings.Contains(body, "return") ||
		strings.Contains(body, "agent") ||
		strings.Contains(body, "list_tasks") ||
		strings.Contains(body, "next")

	if !hasNextStep {
		t.Error("response body should provide next steps (return to agent, run list_tasks, etc.)")
	}
}
