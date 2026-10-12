package githubref

import "testing"

func TestParse(t *testing.T) {
	cases := []struct {
		name       string
		title      string
		desc       string
		boardRepo  string
		wantOK     bool
		wantKind   Kind
		wantNumber int
		wantRepo   string
		wantURL    string
	}{
		{
			name:       "bare reference resolves against board repo",
			title:      "Fix the login bug #42",
			boardRepo:  "octo/cli",
			wantOK:     true,
			wantKind:   KindIssue,
			wantNumber: 42,
			wantRepo:   "octo/cli",
			wantURL:    "https://github.com/octo/cli/issues/42",
		},
		{
			name:      "bare reference with no board repo yields nothing",
			title:     "Fix #42",
			boardRepo: "",
			wantOK:    false,
		},
		{
			name:       "full PR URL beats a bare reference",
			title:      "Address review on #7",
			desc:       "See https://github.com/octo/cli/pull/99 for the fix",
			boardRepo:  "octo/cli",
			wantOK:     true,
			wantKind:   KindPR,
			wantNumber: 99,
			wantRepo:   "octo/cli",
			wantURL:    "https://github.com/octo/cli/pull/99",
		},
		{
			name:       "full issue URL",
			desc:       "tracked by https://github.com/other/repo/issues/5",
			boardRepo:  "octo/cli",
			wantOK:     true,
			wantKind:   KindIssue,
			wantNumber: 5,
			wantRepo:   "other/repo",
			wantURL:    "https://github.com/other/repo/issues/5",
		},
		{
			name:       "qualified cross-repo reference",
			title:      "Blocked by octo/api#88",
			boardRepo:  "octo/cli",
			wantOK:     true,
			wantKind:   KindIssue,
			wantNumber: 88,
			wantRepo:   "octo/api",
			wantURL:    "https://github.com/octo/api/issues/88",
		},
		{
			name:       "PR URL takes precedence over issue URL",
			desc:       "fixes https://github.com/octo/cli/issues/3 via https://github.com/octo/cli/pull/4",
			boardRepo:  "octo/cli",
			wantOK:     true,
			wantKind:   KindPR,
			wantNumber: 4,
			wantRepo:   "octo/cli",
			wantURL:    "https://github.com/octo/cli/pull/4",
		},
		{
			name:      "no reference at all",
			title:     "Refactor the parser",
			desc:      "nothing to see here",
			boardRepo: "octo/cli",
			wantOK:    false,
		},
		{
			name:      "hash glued to a word is not a reference",
			title:     "color is ab#123 in the palette",
			boardRepo: "octo/cli",
			wantOK:    false,
		},
		{
			name:       "reference in parentheses",
			title:      "Done (#12)",
			boardRepo:  "octo/cli",
			wantOK:     true,
			wantKind:   KindIssue,
			wantNumber: 12,
			wantRepo:   "octo/cli",
			wantURL:    "https://github.com/octo/cli/issues/12",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ref, ok := Parse(tc.title, tc.desc, tc.boardRepo)
			if ok != tc.wantOK {
				t.Fatalf("ok = %v, want %v (ref=%+v)", ok, tc.wantOK, ref)
			}
			if !tc.wantOK {
				if ref != nil {
					t.Fatalf("expected nil ref when ok=false, got %+v", ref)
				}
				return
			}
			if ref.Kind != tc.wantKind {
				t.Errorf("Kind = %q, want %q", ref.Kind, tc.wantKind)
			}
			if ref.Number != tc.wantNumber {
				t.Errorf("Number = %d, want %d", ref.Number, tc.wantNumber)
			}
			if ref.Repo != tc.wantRepo {
				t.Errorf("Repo = %q, want %q", ref.Repo, tc.wantRepo)
			}
			if ref.URL != tc.wantURL {
				t.Errorf("URL = %q, want %q", ref.URL, tc.wantURL)
			}
		})
	}
}
