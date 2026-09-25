package github

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

// routeDoer builds a fakeDoer whose respond func picks a canned response by
// matching a URL substring, and records the requests it saw. It reuses the
// shared fakeDoer type from client_test.go (no redeclaration).
type oauthRoute struct {
	responses map[string]*http.Response
	errs      map[string]error
	seen      []*http.Request
}

func (rt *oauthRoute) doer() *fakeDoer {
	return &fakeDoer{respond: func(req *http.Request) (*http.Response, error) {
		rt.seen = append(rt.seen, req)
		for frag, err := range rt.errs {
			if strings.Contains(req.URL.String(), frag) {
				return nil, err
			}
		}
		for frag, resp := range rt.responses {
			if strings.Contains(req.URL.String(), frag) {
				return resp, nil
			}
		}
		return &http.Response{StatusCode: 599, Body: io.NopCloser(strings.NewReader("no canned response"))}, nil
	}}
}

func oresp(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body))}
}

func testOAuth(rt *oauthRoute) *OAuth {
	return NewOAuth(OAuthConfig{ClientID: "Iv1.test", ClientSecret: "shh", RedirectURI: "https://x/callback"}, rt.doer())
}

func TestAuthorizeURL(t *testing.T) {
	o := NewOAuth(OAuthConfig{ClientID: "Iv1.test", ClientSecret: "shh"}, (&oauthRoute{}).doer())
	u := o.AuthorizeURL("state-abc")
	if !strings.HasPrefix(u, oauthAuthorizeBase+"?") {
		t.Fatalf("authorize URL has wrong base: %s", u)
	}
	for _, want := range []string{"client_id=Iv1.test", "state=state-abc", "allow_signup=false"} {
		if !strings.Contains(u, want) {
			t.Errorf("authorize URL missing %q: %s", want, u)
		}
	}
}

func TestExchangeCode_Success(t *testing.T) {
	rt := &oauthRoute{responses: map[string]*http.Response{
		"login/oauth/access_token": oresp(200, `{"access_token":"usr-tok","token_type":"bearer"}`),
		"/user":                    oresp(200, `{"login":"octocat","id":1}`),
	}}
	login, err := testOAuth(rt).ExchangeCode(context.Background(), "code123")
	if err != nil {
		t.Fatalf("ExchangeCode: %v", err)
	}
	if login != "octocat" {
		t.Fatalf("login = %q, want octocat", login)
	}
	var sawToken bool
	for _, r := range rt.seen {
		if strings.Contains(r.URL.String(), "access_token") {
			sawToken = true
			if r.Method != http.MethodPost {
				t.Errorf("token exchange method = %s, want POST", r.Method)
			}
		}
	}
	if !sawToken {
		t.Error("token exchange was never called")
	}
}

func TestExchangeCode_EmptyCode(t *testing.T) {
	rt := &oauthRoute{}
	if _, err := testOAuth(rt).ExchangeCode(context.Background(), "  "); err == nil {
		t.Fatal("empty code must error before any call")
	}
	if len(rt.seen) != 0 {
		t.Errorf("empty code made %d calls, want 0", len(rt.seen))
	}
}

func TestExchangeCode_GitHubOAuthError(t *testing.T) {
	rt := &oauthRoute{responses: map[string]*http.Response{
		"login/oauth/access_token": oresp(200, `{"error":"bad_verification_code","error_description":"expired"}`),
	}}
	if _, err := testOAuth(rt).ExchangeCode(context.Background(), "code123"); err == nil {
		t.Fatal("a GitHub OAuth error field must be surfaced as an error")
	}
}

func TestExchangeCode_TokenNon2xx_FailsClosed(t *testing.T) {
	rt := &oauthRoute{responses: map[string]*http.Response{
		"login/oauth/access_token": oresp(500, `boom`),
	}}
	login, err := testOAuth(rt).ExchangeCode(context.Background(), "code123")
	if err == nil || login != "" {
		t.Fatalf("non-2xx token exchange must fail closed; got (%q,%v)", login, err)
	}
}

func TestExchangeCode_UserFetchError_FailsClosed(t *testing.T) {
	rt := &oauthRoute{
		responses: map[string]*http.Response{
			"login/oauth/access_token": oresp(200, `{"access_token":"usr-tok"}`),
		},
		errs: map[string]error{"/user": errors.New("network down")},
	}
	login, err := testOAuth(rt).ExchangeCode(context.Background(), "code123")
	if err == nil || login != "" {
		t.Fatalf("user fetch error must fail closed; got (%q,%v)", login, err)
	}
}

func TestExchangeCode_EmptyLogin_FailsClosed(t *testing.T) {
	rt := &oauthRoute{responses: map[string]*http.Response{
		"login/oauth/access_token": oresp(200, `{"access_token":"usr-tok"}`),
		"/user":                    oresp(200, `{"login":""}`),
	}}
	login, err := testOAuth(rt).ExchangeCode(context.Background(), "code123")
	if err == nil || login != "" {
		t.Fatalf("empty login must fail closed; got (%q,%v)", login, err)
	}
}

func TestExchangeCode_MissingClientCreds(t *testing.T) {
	o := NewOAuth(OAuthConfig{ClientID: "", ClientSecret: ""}, (&oauthRoute{}).doer())
	if _, err := o.ExchangeCode(context.Background(), "code123"); err == nil {
		t.Fatal("missing client id/secret must error")
	}
}
