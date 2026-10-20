// Package ghperm provides GitHub-native authorization for cainban.
//
// It replaces the grants table by checking user permissions via the GitHub
// REST API and caching results for low-latency authorization decisions.
// User management happens entirely in GitHub (repository collaborator settings)
// rather than a separate database.
//
// # Usage
//
// Create a PermissionChecker with a GitHubClient and cache:
//
//	cache := ghperm.NewMemoryCache(nil)
//	client := ghperm.NewGitHubClient(http.DefaultClient)
//
//	checker, err := ghperm.NewChecker(ghperm.CheckerConfig{
//		Client: client,
//		Cache:  cache,
//		TTL:    5 * time.Minute,
//	})
//
// Check if a user can write to a repository:
//
//	canWrite, err := checker.CanWrite(ctx, githubUser, githubToken, "owner/repo")
//	if err != nil {
//		// Fail closed: treat error as denial
//		return unauthorized()
//	}
//	if !canWrite {
//		return forbidden()
//	}
//
// # Cache Behavior
//
// Permission lookups are cached to avoid GitHub API latency (100-200ms per
// call). The default TTL is 5 minutes. Both grants and denials are cached
// to prevent permission enumeration attacks.
//
// Errors are NOT cached - the next request will retry the GitHub API.
//
// # Fail Closed
//
// All error paths deny access. The caller MUST treat any error from CanWrite
// or GetPermission as a denial:
//
//	canWrite, err := checker.CanWrite(ctx, user, token, repo)
//	if err != nil {
//		// MUST deny on error - never fall through to granted
//		return unauthenticated("permission check failed", err)
//	}
//
// # Permission Levels
//
// GitHub permission levels map to the Permission type:
//
//   - PermissionAdmin: repository admin
//   - PermissionMaintain: maintainer (can manage some settings)
//   - PermissionWrite: can push to the repository
//   - PermissionTriage: can manage issues and PRs
//   - PermissionRead: can read the repository
//   - PermissionNone: no access
//
// CanWrite returns true for Admin, Maintain, and Write permissions.
package ghperm
