package github

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

// fakeDoer is an in-memory HTTP transport: it records the last request and
// returns a canned response (or error) chosen by the caller. It is the mock
// seam that keeps every client test off the network.
type fakeDoer struct {
	// respond maps a request to a response; it receives the request so a test
	// can assert on the URL/headers and branch by path.
	respond func(req *http.Request) (*http.Response, error)
	lastReq *http.Request
}

func (f *fakeDoer) Do(req *http.Request) (*http.Response, error) {
	f.lastReq = req
	return f.respond(req)
}

func jsonResp(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Body:       io.NopCloser(bytes.NewBufferString(body)),
		Header:     make(http.Header),
	}
}

func newTestClient(t *testing.T, doer Doer) *httpClient {
	t.Helper()
	c, err := NewClient(AppConfig{AppID: 123, PrivateKeyPEM: pkcs1PEM(t, testKey(t))}, doer)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	return c.(*httpClient)
}

func TestNewClientRejectsBadConfig(t *testing.T) {
	if _, err := NewClient(AppConfig{AppID: 0, PrivateKeyPEM: pkcs1PEM(t, testKey(t))}, &fakeDoer{}); err == nil {
		t.Error("expected error for non-positive app id")
	}
	if _, err := NewClient(AppConfig{AppID: 1, PrivateKeyPEM: []byte("bad")}, &fakeDoer{}); err == nil {
		t.Error("expected error for unparseable private key")
	}
}

// TestInstallationTokenExchange verifies the POST exchange: correct method,
// URL, App-JWT bearer, and that a 201 token body is decoded. NO live GitHub.
func TestInstallationTokenExchange(t *testing.T) {
	doer := &fakeDoer{
		respond: func(req *http.Request) (*http.Response, error) {
			if req.Method != http.MethodPost {
				t.Errorf("method = %s, want POST", req.Method)
			}
			if !strings.HasSuffix(req.URL.Path, "/app/installations/99/access_tokens") {
				t.Errorf("unexpected path %q", req.URL.Path)
			}
			if auth := req.Header.Get("Authorization"); !strings.HasPrefix(auth, "Bearer ") {
				t.Errorf("missing bearer auth: %q", auth)
			}
			if v := req.Header.Get("X-GitHub-Api-Version"); v != "2022-11-28" {
				t.Errorf("missing api version header: %q", v)
			}
			return jsonResp(http.StatusCreated, `{"token":"ghs_installationtoken","expires_at":"2026-09-25T11:00:00Z"}`), nil
		},
	}
	c := newTestClient(t, doer)
	tok, err := c.InstallationToken(context.Background(), 99)
	if err != nil {
		t.Fatalf("InstallationToken: %v", err)
	}
	if tok != "ghs_installationtoken" {
		t.Errorf("token = %q, want ghs_installationtoken", tok)
	}
}

func TestInstallationTokenErrors(t *testing.T) {
	t.Run("non-201 is error", func(t *testing.T) {
		c := newTestClient(t, &fakeDoer{respond: func(*http.Request) (*http.Response, error) {
			return jsonResp(http.StatusUnauthorized, `{"message":"bad jwt"}`), nil
		}})
		if _, err := c.InstallationToken(context.Background(), 99); err == nil {
			t.Fatal("expected error on 401")
		}
	})
	t.Run("transport error propagates", func(t *testing.T) {
		c := newTestClient(t, &fakeDoer{respond: func(*http.Request) (*http.Response, error) {
			return nil, errors.New("dial tcp: connection refused")
		}})
		if _, err := c.InstallationToken(context.Background(), 99); err == nil {
			t.Fatal("expected transport error to propagate")
		}
	})
	t.Run("empty token is error", func(t *testing.T) {
		c := newTestClient(t, &fakeDoer{respond: func(*http.Request) (*http.Response, error) {
			return jsonResp(http.StatusCreated, `{"token":""}`), nil
		}})
		if _, err := c.InstallationToken(context.Background(), 99); err == nil {
			t.Fatal("expected error on empty token")
		}
	})
	t.Run("non-positive id rejected before call", func(t *testing.T) {
		c := newTestClient(t, &fakeDoer{respond: func(*http.Request) (*http.Response, error) {
			t.Fatal("should not call GitHub for id<=0")
			return nil, nil
		}})
		if _, err := c.InstallationToken(context.Background(), 0); err == nil {
			t.Fatal("expected error for id<=0")
		}
	})
}

// TestRepoInstallationID covers the coverage lookup: 200 -> id, 404 ->
// ErrNotInstalled, other -> error.
func TestRepoInstallationID(t *testing.T) {
	t.Run("200 returns id and uses App JWT", func(t *testing.T) {
		doer := &fakeDoer{respond: func(req *http.Request) (*http.Response, error) {
			if !strings.HasSuffix(req.URL.Path, "/repos/acme/widgets/installation") {
				t.Errorf("unexpected path %q", req.URL.Path)
			}
			if !strings.HasPrefix(req.Header.Get("Authorization"), "Bearer ") {
				t.Error("expected App-JWT bearer")
			}
			return jsonResp(http.StatusOK, `{"id":555,"app_id":123}`), nil
		}}
		c := newTestClient(t, doer)
		id, err := c.RepoInstallationID(context.Background(), "acme", "widgets")
		if err != nil {
			t.Fatalf("RepoInstallationID: %v", err)
		}
		if id != 555 {
			t.Errorf("id = %d, want 555", id)
		}
	})

	t.Run("404 is ErrNotInstalled", func(t *testing.T) {
		c := newTestClient(t, &fakeDoer{respond: func(*http.Request) (*http.Response, error) {
			return jsonResp(http.StatusNotFound, `{"message":"Not Found"}`), nil
		}})
		_, err := c.RepoInstallationID(context.Background(), "acme", "widgets")
		if !errors.Is(err, ErrNotInstalled) {
			t.Fatalf("err = %v, want ErrNotInstalled", err)
		}
	})

	t.Run("500 is a hard error", func(t *testing.T) {
		c := newTestClient(t, &fakeDoer{respond: func(*http.Request) (*http.Response, error) {
			return jsonResp(http.StatusInternalServerError, `oops`), nil
		}})
		_, err := c.RepoInstallationID(context.Background(), "acme", "widgets")
		if err == nil || errors.Is(err, ErrNotInstalled) {
			t.Fatalf("err = %v, want a hard (non-ErrNotInstalled) error", err)
		}
	})
}

// TestIsOrgMember covers 204 (member), 404/302 (not a member), other (error).
func TestIsOrgMember(t *testing.T) {
	cases := []struct {
		name       string
		status     int
		wantMember bool
		wantErr    bool
	}{
		{"204 member", http.StatusNoContent, true, false},
		{"404 not member", http.StatusNotFound, false, false},
		{"302 not visible => not member", http.StatusFound, false, false},
		{"500 error", http.StatusInternalServerError, false, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := newTestClient(t, &fakeDoer{respond: func(req *http.Request) (*http.Response, error) {
				if !strings.HasSuffix(req.URL.Path, "/orgs/acme/members/octocat") {
					t.Errorf("unexpected path %q", req.URL.Path)
				}
				return jsonResp(tc.status, ``), nil
			}})
			got, err := c.IsOrgMember(context.Background(), "inst-tok", "acme", "octocat")
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tc.wantErr)
			}
			if got != tc.wantMember {
				t.Errorf("member = %v, want %v", got, tc.wantMember)
			}
		})
	}
}

// TestCollaboratorPermission covers 200 (permission), 404 (none), other (error).
func TestCollaboratorPermission(t *testing.T) {
	t.Run("200 write", func(t *testing.T) {
		c := newTestClient(t, &fakeDoer{respond: func(req *http.Request) (*http.Response, error) {
			if !strings.HasSuffix(req.URL.Path, "/repos/acme/widgets/collaborators/octocat/permission") {
				t.Errorf("unexpected path %q", req.URL.Path)
			}
			return jsonResp(http.StatusOK, `{"permission":"write"}`), nil
		}})
		perm, err := c.CollaboratorPermission(context.Background(), "inst-tok", "acme", "widgets", "octocat")
		if err != nil {
			t.Fatalf("CollaboratorPermission: %v", err)
		}
		if perm != "write" {
			t.Errorf("perm = %q, want write", perm)
		}
	})
	t.Run("404 => none", func(t *testing.T) {
		c := newTestClient(t, &fakeDoer{respond: func(*http.Request) (*http.Response, error) {
			return jsonResp(http.StatusNotFound, `{"message":"Not Found"}`), nil
		}})
		perm, err := c.CollaboratorPermission(context.Background(), "inst-tok", "acme", "widgets", "octocat")
		if err != nil {
			t.Fatalf("CollaboratorPermission: %v", err)
		}
		if perm != "none" {
			t.Errorf("perm = %q, want none", perm)
		}
	})
	t.Run("500 error", func(t *testing.T) {
		c := newTestClient(t, &fakeDoer{respond: func(*http.Request) (*http.Response, error) {
			return jsonResp(http.StatusBadGateway, `nope`), nil
		}})
		if _, err := c.CollaboratorPermission(context.Background(), "inst-tok", "acme", "widgets", "octocat"); err == nil {
			t.Fatal("expected error on 502")
		}
	})
}

// UserInstallations decodes the {installations:[...]} envelope, uses the USER
// token as the bearer (not an App JWT), and returns id/account/app metadata.
func TestUserInstallations(t *testing.T) {
	doer := &fakeDoer{
		respond: func(req *http.Request) (*http.Response, error) {
			if req.Method != http.MethodGet {
				t.Errorf("method = %s, want GET", req.Method)
			}
			if !strings.HasSuffix(req.URL.Path, "/user/installations") {
				t.Errorf("unexpected path %q", req.URL.Path)
			}
			if auth := req.Header.Get("Authorization"); auth != "Bearer usr-token" {
				t.Errorf("bearer = %q, want the USER token", auth)
			}
			return jsonResp(http.StatusOK, `{"total_count":1,"installations":[{"id":42,"app_id":7,"app_slug":"cainban","account":{"login":"acme"}}]}`), nil
		},
	}
	c := newTestClient(t, doer)
	installs, err := c.UserInstallations(context.Background(), "usr-token")
	if err != nil {
		t.Fatalf("UserInstallations: %v", err)
	}
	if len(installs) != 1 {
		t.Fatalf("installs = %d, want 1", len(installs))
	}
	got := installs[0]
	if got.ID != 42 || got.AppID != 7 || got.AppSlug != "cainban" || got.Account != "acme" {
		t.Fatalf("installation = %+v, want {42,acme,7,cainban}", got)
	}
}

func TestUserInstallations_Non2xx_FailsClosed(t *testing.T) {
	doer := &fakeDoer{respond: func(_ *http.Request) (*http.Response, error) {
		return jsonResp(http.StatusForbidden, `forbidden`), nil
	}}
	c := newTestClient(t, doer)
	if _, err := c.UserInstallations(context.Background(), "usr-token"); err == nil {
		t.Fatal("a non-2xx must fail closed")
	}
}

func TestUserInstallations_EmptyToken(t *testing.T) {
	c := newTestClient(t, &fakeDoer{respond: func(_ *http.Request) (*http.Response, error) {
		t.Fatal("must not call GitHub with an empty user token")
		return nil, nil
	}})
	if _, err := c.UserInstallations(context.Background(), "  "); err == nil {
		t.Fatal("empty user token must error before any call")
	}
}

// InstallationRepositories follows pagination: a full first page (per_page=100)
// then a short page terminates. All full_names across pages are returned.
func TestInstallationRepositories_Paginated(t *testing.T) {
	// Build a full page of 100 repos, then a 2-repo page.
	var page1 strings.Builder
	page1.WriteString(`{"total_count":102,"repositories":[`)
	for i := 0; i < 100; i++ {
		if i > 0 {
			page1.WriteString(",")
		}
		page1.WriteString(`{"full_name":"acme/repo`)
		page1.WriteString(itoa(i))
		page1.WriteString(`"}`)
	}
	page1.WriteString(`]}`)
	page2 := `{"total_count":102,"repositories":[{"full_name":"acme/repo100"},{"full_name":"acme/repo101"}]}`

	var pagesSeen []string
	doer := &fakeDoer{respond: func(req *http.Request) (*http.Response, error) {
		if !strings.Contains(req.URL.Path, "/user/installations/42/repositories") {
			t.Errorf("unexpected path %q", req.URL.Path)
		}
		if req.Header.Get("Authorization") != "Bearer usr-token" {
			t.Errorf("bearer = %q, want the USER token", req.Header.Get("Authorization"))
		}
		p := req.URL.Query().Get("page")
		pagesSeen = append(pagesSeen, p)
		if p == "1" {
			return jsonResp(http.StatusOK, page1.String()), nil
		}
		return jsonResp(http.StatusOK, page2), nil
	}}
	c := newTestClient(t, doer)
	repos, err := c.InstallationRepositories(context.Background(), "usr-token", 42)
	if err != nil {
		t.Fatalf("InstallationRepositories: %v", err)
	}
	if len(repos) != 102 {
		t.Fatalf("repos = %d, want 102 (pagination not followed)", len(repos))
	}
	if repos[0] != "acme/repo0" || repos[101] != "acme/repo101" {
		t.Errorf("boundary repos = %q..%q", repos[0], repos[101])
	}
	if len(pagesSeen) < 2 {
		t.Errorf("expected at least 2 page requests, saw %v", pagesSeen)
	}
}

func TestInstallationRepositories_Non2xx_FailsClosed(t *testing.T) {
	doer := &fakeDoer{respond: func(_ *http.Request) (*http.Response, error) {
		return jsonResp(http.StatusInternalServerError, `boom`), nil
	}}
	c := newTestClient(t, doer)
	if _, err := c.InstallationRepositories(context.Background(), "usr-token", 42); err == nil {
		t.Fatal("a non-2xx must fail closed")
	}
}

// itoa is a tiny local int->string to avoid importing strconv in the test for
// one call site.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
