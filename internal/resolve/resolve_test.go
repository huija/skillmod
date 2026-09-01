// Copyright 2026 huija
//
// SPDX-License-Identifier: MIT

package resolve

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/huija/skillmod/internal/i18n"
	"github.com/huija/skillmod/internal/testutil"
)

func TestMain(m *testing.M) { testutil.RunMain(m) }

const sha1 = "0123456789abcdef0123456789abcdef01234567"
const sha2 = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
const sha3 = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"

func testRefs() *Refs {
	return &Refs{
		Tags: map[string]string{
			"v1.0.0":             sha1,
			"v1.2.0":             sha2,
			"v2.0.0-beta":        sha3,
			"code-review/v1.0.0": sha1,
			"code-review/v1.2.0": sha2,
			"pdf/v0.8.1":         sha3,
			"latest":             sha1, // Ignore non-semver tags.
			"docs/readme":        sha2, // Ignore non-semver subdirectory tags.
		},
		Heads:         map[string]string{"main": sha3, "dev": sha2},
		DefaultBranch: "main",
		DefaultHead:   sha3,
	}
}

func TestResolve_Explicit(t *testing.T) {
	refs := testRefs()
	tests := []struct {
		name        string
		req         Request
		wantVersion string
		wantCommit  string
		wantErr     any // nil means no error; otherwise this is the errors.As target
	}{
		{"exact root tag", Request{Ref: "v1.0.0"}, "v1.0.0", sha1, nil},
		{"bare subdirectory version adds prefix", Request{Subdir: "code-review", Ref: "v1.2.0"}, "code-review/v1.2.0", sha2, nil},
		{"fully qualified subdirectory tag", Request{Subdir: "code-review", Ref: "code-review/v1.0.0"}, "code-review/v1.0.0", sha1, nil},
		{"bare subdirectory version falls back to root tag", Request{Subdir: "pdf", Ref: "v1.0.0"}, "v1.0.0", sha1, nil},
		{"40-character SHA pin", Request{Ref: sha1}, "", sha1, nil},
		{"40-character SHA under subdirectory", Request{Subdir: "pdf", Ref: sha2}, "", sha2, nil},
		{"branch rejected", Request{Ref: "main"}, "", "", &BranchError{}},
		{"branch under subdirectory rejected", Request{Subdir: "pdf", Ref: "dev"}, "", "", &BranchError{}},
		{"version not found", Request{Ref: "v9.9.9"}, "", "", &NotFoundError{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Resolve(tt.req, refs)
			if tt.wantErr != nil {
				if !errors.As(err, &tt.wantErr) {
					t.Fatalf("err = %v (%T), want %T", err, err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("Resolve: %v", err)
			}
			if got.Version != tt.wantVersion || got.Commit != tt.wantCommit {
				t.Errorf("= %+v, want version=%q commit=%q", got, tt.wantVersion, tt.wantCommit)
			}
		})
	}
}

func TestResolve_BranchErrorMessage(t *testing.T) {
	_, err := Resolve(Request{Ref: "main"}, testRefs())
	var be *BranchError
	if !errors.As(err, &be) {
		t.Fatalf("err = %v", err)
	}
	// Exact wording required by PRD §3.2.
	want := i18n.Format("branches cannot be locked; use a tag or commit SHA (%q is a branch name)", "main")
	if be.Error() != want {
		t.Errorf("message = %q", be.Error())
	}
}

func TestResolve_NotFoundListsCandidates(t *testing.T) {
	_, err := Resolve(Request{Subdir: "code-review", Ref: "v3.0.0"}, testRefs())
	var nf *NotFoundError
	if !errors.As(err, &nf) {
		t.Fatalf("err = %v", err)
	}
	// Prefer subdirectory tags in descending semver order for a subdirectory request.
	want := []string{"code-review/v1.2.0", "code-review/v1.0.0"}
	if strings.Join(nf.Candidates, ",") != strings.Join(want, ",") {
		t.Errorf("Candidates = %v, want %v", nf.Candidates, want)
	}
}

func TestResolve_NotFoundFallsBackToRootTags(t *testing.T) {
	refs := testRefs()
	// With no lark-doc/ tags and no v9.9.9, fall back to root tags and ignore the non-semver "latest" tag.
	_, err := Resolve(Request{Subdir: "lark-doc", Ref: "v9.9.9"}, refs)
	var nf *NotFoundError
	if !errors.As(err, &nf) {
		t.Fatalf("err = %v", err)
	}
	for _, c := range nf.Candidates {
		if strings.Contains(c, "/") || c == "latest" {
			t.Errorf("candidates contain a non-root or non-semver tag: %v", nf.Candidates)
		}
	}
}

func TestResolve_NotFoundCapsAt10(t *testing.T) {
	tags := map[string]string{}
	for _, v := range []string{"v1.0.0", "v1.1.0", "v1.2.0", "v1.3.0", "v1.4.0", "v1.5.0", "v1.6.0", "v1.7.0", "v1.8.0", "v1.9.0", "v1.10.0", "v1.11.0"} {
		tags[v] = sha1
	}
	_, err := Resolve(Request{Ref: "v9.9.9"}, &Refs{Tags: tags})
	var nf *NotFoundError
	if !errors.As(err, &nf) {
		t.Fatalf("err = %v", err)
	}
	if len(nf.Candidates) != 10 {
		t.Errorf("len(Candidates) = %d, want 10", len(nf.Candidates))
	}
	if nf.Candidates[0] != "v1.11.0" {
		t.Errorf("highest version = %s, want v1.11.0", nf.Candidates[0])
	}
}

func TestResolve_Latest(t *testing.T) {
	tests := []struct {
		name        string
		refs        *Refs
		req         Request
		wantVersion string
		wantCommit  string
		wantErr     any
	}{
		{"highest root semver", testRefs(), Request{}, "v1.2.0", sha2, nil},
		{"highest subdirectory semver", testRefs(), Request{Subdir: "code-review"}, "code-review/v1.2.0", sha2, nil},
		{"subdirectory without tag falls back to root", testRefs(), Request{Subdir: "lark-doc"}, "v1.2.0", sha2, nil},
		{"untagged repository uses HEAD", &Refs{Heads: map[string]string{"main": sha1}, DefaultHead: sha1}, Request{}, "", sha1, nil},
		{"empty repository returns error", &Refs{}, Request{}, "", "", &EmptyRepoError{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Resolve(tt.req, tt.refs)
			if tt.wantErr != nil {
				if !errors.As(err, &tt.wantErr) {
					t.Fatalf("err = %v (%T), want %T", err, err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("Resolve: %v", err)
			}
			if got.Version != tt.wantVersion || got.Commit != tt.wantCommit {
				t.Errorf("= %+v, want version=%q commit=%q", got, tt.wantVersion, tt.wantCommit)
			}
		})
	}
}

func TestResolve_LatestPrefersStableOverPrerelease(t *testing.T) {
	// Ignore a higher prerelease when a release exists.
	refs := &Refs{Tags: map[string]string{"v1.9.0": sha1, "v2.0.0-beta": sha2}}
	got, err := Resolve(Request{}, refs)
	if err != nil {
		t.Fatal(err)
	}
	if got.Version != "v1.9.0" {
		t.Errorf("Version = %q, want stable v1.9.0 instead of a prerelease", got.Version)
	}
	// Select a prerelease when it is the only option.
	refs = &Refs{Tags: map[string]string{"v2.0.0-beta": sha2, "v2.0.0-alpha": sha1}}
	got, err = Resolve(Request{}, refs)
	if err != nil {
		t.Fatal(err)
	}
	if got.Version != "v2.0.0-beta" {
		t.Errorf("Version = %q, want v2.0.0-beta", got.Version)
	}
}

func TestIsSHA(t *testing.T) {
	if !IsSHA(sha1) {
		t.Error("a 40-character lowercase hexadecimal string should be a SHA")
	}
	for _, s := range []string{"", "v1.0.0", sha1 + "0", sha1[:39], strings.ToUpper(sha1), "g123456789abcdef0123456789abcdef01234567"} {
		if IsSHA(s) {
			t.Errorf("IsSHA(%q) = true, want false", s)
		}
	}
}

func TestPseudoVersion(t *testing.T) {
	ts := time.Date(2026, 8, 26, 12, 30, 45, 0, time.FixedZone("CST", 8*3600))
	v := PseudoVersion(ts, sha1)
	// Convert to UTC: 12:30:45 +0800 = 04:30:45Z.
	if v != "v0.0.0-20260826043045-0123456789ab" {
		t.Errorf("PseudoVersion = %q", v)
	}
	if !IsPseudoVersion(v) {
		t.Errorf("generated pseudo-version %q was rejected by IsPseudoVersion", v)
	}
	if IsPseudoVersion("v1.2.0") || IsPseudoVersion("v0.0.0-999") {
		t.Error("IsPseudoVersion accepted a non-pseudo-version")
	}
}

func TestResolutionErrorDiagnostics(t *testing.T) {
	tests := []struct {
		err  error
		want []string
	}{
		{err: &NotFoundError{Ref: "v9.0.0"}, want: []string{"v9.0.0", "no available tags"}},
		{err: &NotFoundError{Ref: "v9.0.0", Candidates: []string{"v2.0.0", "v1.0.0"}}, want: []string{"v9.0.0", "v2.0.0, v1.0.0"}},
		{err: &EmptyRepoError{Repo: "https://example.com/acme/skills"}, want: []string{"example.com/acme/skills", "default-branch HEAD"}},
	}
	for _, tt := range tests {
		message := tt.err.Error()
		for _, want := range tt.want {
			if !strings.Contains(message, want) {
				t.Errorf("%T message %q is missing %q", tt.err, message, want)
			}
		}
	}
}
