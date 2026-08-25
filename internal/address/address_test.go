// Copyright 2026 huija
//
// SPDX-License-Identifier: MIT

package address

import (
	"strings"
	"testing"
)

func TestParse(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		want    *Address
		wantErr string
	}{
		// Add https:// to a bare path.
		{"裸路径", "github.com/a/b", &Address{Repo: "https://github.com/a/b"}, ""},
		{"裸路径带子目录", "github.com/a/b//sub", &Address{Repo: "https://github.com/a/b", Subdir: "sub"}, ""},
		{"裸路径带子目录和版本", "github.com/a/b//sub@v1.2.0", &Address{Repo: "https://github.com/a/b", Subdir: "sub", Ref: "v1.2.0"}, ""},
		{"裸路径带版本无子目录", "github.com/a/b@v1.2.0", &Address{Repo: "https://github.com/a/b", Ref: "v1.2.0"}, ""},
		// Preserve a complete URL.
		{"https", "https://github.com/a/b", &Address{Repo: "https://github.com/a/b"}, ""},
		{"https 带子目录", "https://github.com/a/b//sub/dir@v2.0.0", &Address{Repo: "https://github.com/a/b", Subdir: "sub/dir", Ref: "v2.0.0"}, ""},
		{"file 协议", "file:///tmp/repo//skill", &Address{Repo: "file:///tmp/repo", Subdir: "skill"}, ""},
		// In scp-like syntax, @ is not a version separator.
		{"scp-like 无 ref", "git@github.com:a/b", &Address{Repo: "git@github.com:a/b"}, ""},
		{"scp-like 带 ref", "git@github.com:a/b@v1.0.0", &Address{Repo: "git@github.com:a/b", Ref: "v1.0.0"}, ""},
		{"scp-like 带子目录和 ref", "git@github.com:a/b//x@v1.0.0", &Address{Repo: "git@github.com:a/b", Subdir: "x", Ref: "v1.0.0"}, ""},
		// Commit SHA reference.
		{"40 位 SHA", "github.com/a/b@0123456789abcdef0123456789abcdef01234567", &Address{Repo: "https://github.com/a/b", Ref: "0123456789abcdef0123456789abcdef01234567"}, ""},
		// Invalid addresses.
		{"空串", "", nil, "为空"},
		{"@ 后为空", "github.com/a/b@", nil, "缺少版本引用"},
		{"缺仓库", "@v1.0.0", nil, "缺少仓库地址"},
		{"// 后为空", "github.com/a/b//", nil, "缺少子目录"},
		{"仓库含空格", "git hub.com/a/b", nil, "空白字符"},
		{"ref 含空格", "github.com/a/b@v1 .0", nil, "空白字符"},
		{"子目录越界", "github.com/a/b//../x", nil, "不规范"},
		{"子目录点段", "github.com/a/b//./x", nil, "不规范"},
		{"子目录尾斜杠", "github.com/a/b//x/", nil, "不规范"},
		{"子目录反斜杠", `github.com/a/b//x\y`, nil, "用 / 分隔"},
		{"子目录重复斜杠", "github.com/a/b//x//y", nil, "不规范"},
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
