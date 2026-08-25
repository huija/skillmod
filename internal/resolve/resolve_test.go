// Copyright 2026 huija
//
// SPDX-License-Identifier: MIT

package resolve

import (
	"errors"
	"strings"
	"testing"
	"time"
)

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
		{"根 tag 精确命中", Request{Ref: "v1.0.0"}, "v1.0.0", sha1, nil},
		{"子目录裸版本自动补前缀", Request{Subdir: "code-review", Ref: "v1.2.0"}, "code-review/v1.2.0", sha2, nil},
		{"子目录写全 tag 也命中", Request{Subdir: "code-review", Ref: "code-review/v1.0.0"}, "code-review/v1.0.0", sha1, nil},
		{"子目录裸版本回退根 tag", Request{Subdir: "pdf", Ref: "v1.0.0"}, "v1.0.0", sha1, nil},
		{"40 位 SHA 直接钉", Request{Ref: sha1}, "", sha1, nil},
		{"子目录下 40 位 SHA", Request{Subdir: "pdf", Ref: sha2}, "", sha2, nil},
		{"分支名拒绝", Request{Ref: "main"}, "", "", &BranchError{}},
		{"子目录下分支名拒绝", Request{Subdir: "pdf", Ref: "dev"}, "", "", &BranchError{}},
		{"版本不存在", Request{Ref: "v9.9.9"}, "", "", &NotFoundError{}},
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
	if be.Error() != `分支不可锁定，请用 tag 或 commit SHA（"main" 是分支名）` {
		t.Errorf("文案 = %q", be.Error())
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
			t.Errorf("候选混入非根级/非 semver tag: %v", nf.Candidates)
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
		t.Errorf("最高版本应为 v1.11.0, got %s", nf.Candidates[0])
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
		{"根级最高 semver", testRefs(), Request{}, "v1.2.0", sha2, nil},
		{"子目录最高 semver", testRefs(), Request{Subdir: "code-review"}, "code-review/v1.2.0", sha2, nil},
		{"子目录无 tag 回退根级", testRefs(), Request{Subdir: "lark-doc"}, "v1.2.0", sha2, nil},
		{"无 tag 仓走 HEAD", &Refs{Heads: map[string]string{"main": sha1}, DefaultHead: sha1}, Request{}, "", sha1, nil},
		{"空仓库报错", &Refs{}, Request{}, "", "", &EmptyRepoError{}},
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
		t.Errorf("Version = %q, want v1.9.0（prerelease 避让）", got.Version)
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
		t.Error("40 位 hex 应判定为 SHA")
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
		t.Errorf("生成的伪版本 %q 未通过 IsPseudoVersion", v)
	}
	if IsPseudoVersion("v1.2.0") || IsPseudoVersion("v0.0.0-999") {
		t.Error("IsPseudoVersion 误判")
	}
}
