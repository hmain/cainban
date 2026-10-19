package main

import (
	"fmt"
	"strings"

	"github.com/aws/aws-lambda-go/events"

	"github.com/hmain/cainban/src/systems/auth"
)

// repoFromConnect extracts and normalizes the "owner/repo" a $connect event
// targets.
//
// API Gateway WebSocket APIs do NOT do path-based routing or expose path
// parameters the way HTTP APIs do (route selection is by body action), so the
// owner/repo is passed on the CONNECT URL as query string parameters —
// wss://<host>/<stage>?owner=<owner>&repo=<repo> — which API Gateway surfaces in
// the $connect event's QueryStringParameters. For resilience we also accept a
// single `repo=owner/repo` query param and, as a last resort, parse a
// /{owner}/{repo} suffix off the request path (a custom-domain / proxy setup
// that preserves it). A value that does not normalize to owner/repo is an error
// so a junk connection never gets stored.
func repoFromConnect(req events.APIGatewayWebsocketProxyRequest) (string, error) {
	q := req.QueryStringParameters

	// Preferred: explicit owner + repo query params.
	owner := strings.TrimSpace(q["owner"])
	name := strings.TrimSpace(q["repo"])
	if owner != "" && name != "" && !strings.Contains(name, "/") {
		return auth.NormalizeRepo(owner + "/" + name)
	}
	// Alternate: a single repo=owner/repo query param.
	if name != "" && strings.Contains(name, "/") {
		return auth.NormalizeRepo(name)
	}
	// Last resort: a /{owner}/{repo} suffix on the path (strip the stage).
	if r, err := repoFromPath(req.Path); err == nil {
		return r, nil
	}
	return "", fmt.Errorf("connect missing owner/repo (expected ?owner=<owner>&repo=<repo>)")
}

// repoFromPath pulls the trailing two non-empty segments of a path as
// owner/repo. It tolerates a leading stage segment and a /ws/ prefix.
func repoFromPath(urlPath string) (string, error) {
	trimmed := strings.Trim(urlPath, "/")
	if trimmed == "" {
		return "", fmt.Errorf("empty path")
	}
	parts := strings.Split(trimmed, "/")
	nonEmpty := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			nonEmpty = append(nonEmpty, p)
		}
	}
	if len(nonEmpty) < 2 {
		return "", fmt.Errorf("path %q has no owner/repo suffix", urlPath)
	}
	owner := nonEmpty[len(nonEmpty)-2]
	name := nonEmpty[len(nonEmpty)-1]
	return auth.NormalizeRepo(owner + "/" + name)
}
