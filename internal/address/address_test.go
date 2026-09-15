// Copyright 2026 huija
//
// SPDX-License-Identifier: MIT

package address

import (
	"strings"
	"testing"

	"github.com/huija/skillmod/internal/testutil"
)

func TestMain(m *testing.M) { testutil.RunMain(m) }

func TestParse(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		want    *Address
		wantErr string
	}{
		// Add https:// to a bare path.
		{"bare repository", "github.com/a/b", &Address{Repo: "https://github.com/a/b"}, ""},
		{"bare repository with subdirectory", "github.com/a/b//sub", &Address{Repo: "https://github.com/a/b", Subdir: "sub"}, ""},
		{"bare repository with subdirectory and version", "github.com/a/b//sub@v1.2.0", &Address{Repo: "https://github.com/a/b", Subdir: "sub", Ref: "v1.2.0"}, ""},
		{"bare repository with version", "github.com/a/b@v1.2.0", &Address{Repo: "https://github.com/a/b", Ref: "v1.2.0"}, ""},
		// Preserve a complete URL.
		{"https", "https://github.com/a/b", &Address{Repo: "https://github.com/a/b"}, ""},
		{"https with subdirectory", "https://github.com/a/b//sub/dir@v2.0.0", &Address{Repo: "https://github.com/a/b", Subdir: "sub/dir", Ref: "v2.0.0"}, ""},
		{"file scheme", "file:///tmp/repo//skill", &Address{Repo: "file:///tmp/repo", Subdir: "skill"}, ""},
		// In scp-like syntax, @ is not a version separator.
		{"scp-like without ref", "git@github.com:a/b", &Address{Repo: "git@github.com:a/b"}, ""},
		{"scp-like with ref", "git@github.com:a/b@v1.0.0", &Address{Repo: "git@github.com:a/b", Ref: "v1.0.0"}, ""},
		{"scp-like with subdirectory and ref", "git@github.com:a/b//x@v1.0.0", &Address{Repo: "git@github.com:a/b", Subdir: "x", Ref: "v1.0.0"}, ""},
		// SSH usernames are transport details rather than embedded credentials.
		{"ssh URL with username", "ssh://git@host/path", &Address{Repo: "ssh://git@host/path"}, ""},
		// Commit SHA reference.
		{"40-character SHA", "github.com/a/b@0123456789abcdef0123456789abcdef01234567", &Address{Repo: "https://github.com/a/b", Ref: "0123456789abcdef0123456789abcdef01234567"}, ""},
		// Invalid addresses.
		{"empty", "", nil, "is empty"},
		{"empty version after at sign", "github.com/a/b@", nil, "missing version reference"},
		{"missing repository", "@v1.0.0", nil, "missing repository address"},
		{"empty subdirectory after separator", "github.com/a/b//", nil, "missing subdirectory"},
		{"repository contains whitespace", "git hub.com/a/b", nil, "whitespace"},
		{"https user info", "https://user@host/path", nil, "must not contain embedded credentials"},
		{"https password", "https://user:secret@host/path", nil, "must not contain embedded credentials"},
		{"ssh password", "ssh://git:secret@host/path", nil, "must not contain embedded credentials"},
		{"query parameters", "https://host/path?access_token=secret", nil, "must not contain embedded credentials"},
		{"query parameters after subdirectory", "https://host/path//skill?access_token=secret", nil, "must not contain embedded credentials"},
		{"fragment", "https://host/path#secret", nil, "must not contain embedded credentials"},
		{"fragment after subdirectory", "https://host/path//skill#secret", nil, "must not contain embedded credentials"},
		{"unsupported transport", "custom://host/path", nil, "must not contain embedded credentials"},
		{"ref contains whitespace", "github.com/a/b@v1 .0", nil, "whitespace"},
		{"subdirectory escapes root", "github.com/a/b//../x", nil, "non-canonical"},
		{"subdirectory dot segment", "github.com/a/b//./x", nil, "non-canonical"},
		{"subdirectory trailing slash", "github.com/a/b//x/", nil, "non-canonical"},
		{"subdirectory backslash", `github.com/a/b//x\y`, nil, "use / separators"},
		{"subdirectory redundant slash", "github.com/a/b//x//y", nil, "non-canonical"},
		{"escaped at subdirectory", "github.com/a/b//foo@@bar", nil, "must not contain @"},
		{"slash-bearing full tag", "github.com/a/b//code-review@code-review/v1.2.0", nil, "must not contain @"},
		{"unsupported git transport", "git://github.com/a/b", nil, "unsupported transport"},
		{"unsupported svn transport", "svn+ssh://github.com/a/b", nil, "unsupported transport"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Parse(tt.raw)
			if tt.wantErr != "" {
				if err == nil {
					t.Fatalf("Parse(%q) = %+v, want error containing %q", tt.raw, got, tt.wantErr)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("Parse(%q) err = %v, want containing %q", tt.raw, err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("Parse(%q): %v", tt.raw, err)
			}
			if *got != *tt.want {
				t.Errorf("Parse(%q) = %+v, want %+v", tt.raw, got, tt.want)
			}
		})
	}
}

func TestParseErrorsDoNotEchoCredentials(t *testing.T) {
	for _, raw := range []string{
		"https://user:secret@",
		"https://user:secret@example.com/repo path",
		"https://user:secret@example.com/repo//",
		"https://example.com/repo//skill?access_token=secret",
	} {
		_, err := Parse(raw)
		if err == nil {
			t.Fatalf("Parse(%q) succeeded, want error", raw)
		}
		for _, secret := range []string{"user", "secret", "access_token"} {
			if strings.Contains(err.Error(), secret) {
				t.Errorf("Parse(%q) error = %q, leaked %q", raw, err, secret)
			}
		}
	}
}

func TestAddress_String(t *testing.T) {
	a := &Address{Repo: "https://github.com/a/b", Subdir: "sub", Ref: "v1.0.0"}
	if got := a.String(); got != "https://github.com/a/b//sub@v1.0.0" {
		t.Errorf("String() = %q", got)
	}
	a.Ref = ""
	if got := a.String(); got != "https://github.com/a/b//sub" {
		t.Errorf("String() no ref = %q", got)
	}
}

func TestManifestSource_DropsImpliedHTTPSAndKeepsExplicitTransports(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want string
	}{
		{"bare path is already canonical", "github.com/a/b", "github.com/a/b"},
		{"https prefix is not recorded", "https://github.com/a/b", "github.com/a/b"},
		{"uppercase https prefix is not recorded", "HTTPS://github.com/a/b", "github.com/a/b"},
		{"https with subdirectory", "https://github.com/a/b//sub/dir", "github.com/a/b//sub/dir"},
		{"non-default port survives", "https://github.com:8443/a/b", "github.com:8443/a/b"},
		// Every other transport changes how the repository is fetched.
		{"http stays explicit", "http://github.com/a/b", "http://github.com/a/b"},
		{"ssh stays explicit", "ssh://git@github.com/a/b", "ssh://git@github.com/a/b"},
		{"scp-like stays explicit", "git@github.com:a/b", "git@github.com:a/b"},
		{"file stays explicit", "file:///tmp/repo//skill", "file:///tmp/repo//skill"},
		// Rewriting must never swallow a diagnostic or a version.
		{"unsupported transport is unchanged", "git://github.com/a/b", "git://github.com/a/b"},
		{"unparseable source is unchanged", "https://example.com/repo//", "https://example.com/repo//"},
		{"version-bearing source is unchanged", "github.com/a/b//sub@v1.0.0", "github.com/a/b//sub@v1.0.0"},
		{"credential-bearing source is unchanged", "https://user:secret@github.com/a/b", "https://user:secret@github.com/a/b"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ManifestSource(tt.raw); got != tt.want {
				t.Errorf("ManifestSource(%q) = %q, want %q", tt.raw, got, tt.want)
			}
		})
	}
}

func TestManifestSource_RoundTripsThroughParse(t *testing.T) {
	for _, raw := range []string{
		"github.com/a/b",
		"https://github.com/a/b//sub",
		"http://github.com/a/b//sub",
		"ssh://git@github.com/a/b",
		"git@github.com:a/b//sub",
		"file:///tmp/repo//skill",
	} {
		canonical := ManifestSource(raw)
		a, err := Parse(canonical)
		if err != nil {
			t.Fatalf("Parse(ManifestSource(%q)) error = %v", raw, err)
		}
		if got := ManifestSource(canonical); got != canonical {
			t.Errorf("ManifestSource is not idempotent: %q -> %q", canonical, got)
		}
		original, err := Parse(raw)
		if err != nil {
			t.Fatalf("Parse(%q) error = %v", raw, err)
		}
		if a.Repo != original.Repo || a.Subdir != original.Subdir {
			t.Errorf("ManifestSource(%q) = %q resolves to %q//%q, want %q//%q",
				raw, canonical, a.Repo, a.Subdir, original.Repo, original.Subdir)
		}
	}
}
