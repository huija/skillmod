// Copyright 2026 huija
//
// SPDX-License-Identifier: MIT

package testutil

import "testing"

// Regression: a bare "file://" + native path produced a Windows URL such as
// "file://C:\Users\...\repo.git", which the address validator rejects before
// any test reaches the code under test, so every file:// fixture failed on
// Windows. The want values are host independent on purpose.
func TestFileURL(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "windows drive path",
			in:   `C:\Users\runner\AppData\Local\Temp\TestX\001\repo.git`,
			want: "file:///C:/Users/runner/AppData/Local/Temp/TestX/001/repo.git",
		},
		{
			name: "posix absolute path",
			in:   "/tmp/TestX/001/repo.git",
			want: "file:///tmp/TestX/001/repo.git",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := FileURL(tt.in); got != tt.want {
				t.Errorf("FileURL(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}
