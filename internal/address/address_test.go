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
		// Commit SHA reference.
		{"40-character SHA", "github.com/a/b@0123456789abcdef0123456789abcdef01234567", &Address{Repo: "https://github.com/a/b", Ref: "0123456789abcdef0123456789abcdef01234567"}, ""},
		// Invalid addresses.
		{"empty", "", nil, "is empty"},
		{"empty version after at sign", "github.com/a/b@", nil, "missing version reference"},
		{"missing repository", "@v1.0.0", nil, "missing repository address"},
		{"empty subdirectory after separator", "github.com/a/b//", nil, "missing subdirectory"},
		{"repository contains whitespace", "git hub.com/a/b", nil, "whitespace"},
		{"ref contains whitespace", "github.com/a/b@v1 .0", nil, "whitespace"},
		{"subdirectory escapes root", "github.com/a/b//../x", nil, "non-canonical"},
		{"subdirectory dot segment", "github.com/a/b//./x", nil, "non-canonical"},
		{"subdirectory trailing slash", "github.com/a/b//x/", nil, "non-canonical"},
		{"subdirectory backslash", `github.com/a/b//x\y`, nil, "use / separators"},
		{"subdirectory redundant slash", "github.com/a/b//x//y", nil, "non-canonical"},
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
