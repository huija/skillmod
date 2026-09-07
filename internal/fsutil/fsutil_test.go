// Copyright 2026 huija
//
// SPDX-License-Identifier: MIT

package fsutil

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestMain(m *testing.M) {
	// i18n.Text is environment driven; pin English so error-fragment
	// assertions are deterministic regardless of the developer's locale.
	if err := os.Setenv("SKILLMOD_LANG", "en_US"); err != nil {
		panic("set test locale: " + err.Error())
	}
	os.Exit(m.Run())
}

func TestValidName(t *testing.T) {
	valid := []string{
		"demo", "Demo-1.2_skill", "café", "中文技能", "a b", "a.b", ".hidden",
		"-x", "com10", "consolidated", "conrad", "K", "σ", "ς", "Σ",
		strings.Repeat("a", 255), // byte-length boundary
	}
	for _, name := range valid {
		if err := ValidName(name); err != nil {
			t.Errorf("ValidName(%q) = %v, want nil", name, err)
		}
	}
	invalid := map[string]string{
		"":                       "empty",
		".":                      "must not be",
		"..":                     "must not be",
		"a/b":                    "illegal on Windows",
		`a\b`:                    "illegal on Windows",
		"a:b":                    "illegal on Windows",
		"a*b":                    "illegal on Windows",
		"a?b":                    "illegal on Windows",
		`a"b`:                    "illegal on Windows",
		"a<b":                    "illegal on Windows",
		"a>b":                    "illegal on Windows",
		"a|b":                    "illegal on Windows",
		"CON":                    "reserved Windows device name",
		"con":                    "reserved Windows device name",
		"Con":                    "reserved Windows device name",
		"con.txt":                "reserved Windows device name",
		"Con.tar.gz":             "reserved Windows device name",
		"CON .txt":               "reserved Windows device name",
		"aux":                    "reserved Windows device name",
		"nul":                    "reserved Windows device name",
		"com1":                   "reserved Windows device name",
		"LPT9":                   "reserved Windows device name",
		"com¹":                   "reserved Windows device name",
		"lpt³":                   "reserved Windows device name",
		"conin$":                 "reserved Windows device name",
		"conout$":                "reserved Windows device name",
		"foo.":                   "dot or space",
		"foo ":                   "dot or space",
		"a\x01b":                 "control character",
		"a\x7fb":                 "control character",
		strings.Repeat("a", 256): "longer than",
		"\xff\xfe":               "UTF-8",
	}
	for name, want := range invalid {
		err := ValidName(name)
		if err == nil {
			t.Errorf("ValidName(%q) = nil, want error containing %q", name, want)
			continue
		}
		if !strings.Contains(err.Error(), want) {
			t.Errorf("ValidName(%q) = %q, want error containing %q", name, err, want)
		}
	}
}

func TestValidAlias(t *testing.T) {
	for _, s := range []string{"demo", "Demo-1.2_skill", "a.b"} {
		if err := ValidAlias(s); err != nil {
			t.Errorf("ValidAlias(%q) = %v, want nil", s, err)
		}
	}
	for _, s := range []string{"café", "a b", "a:b", "CON", "con.txt", "demo.", "", "demo/skill"} {
		if err := ValidAlias(s); err == nil {
			t.Errorf("ValidAlias(%q) = nil, want error", s)
		}
	}
}

func TestValidPath(t *testing.T) {
	for _, p := range []string{"SKILL.md", "skills/demo/SKILL.md", "技能/说明.md"} {
		if err := ValidPath(p); err != nil {
			t.Errorf("ValidPath(%q) = %v, want nil", p, err)
		}
	}
	for _, p := range []string{`a\b`, "a//b", "/abs", "a/../b", "trailing/", "doc/CON", "a:b.txt"} {
		if err := ValidPath(p); err == nil {
			t.Errorf("ValidPath(%q) = nil, want error", p)
		}
	}
}

func TestFoldKey(t *testing.T) {
	equal := [][2]string{
		{"demo", "DEMO"},
		{"Demo", "demo"},
		{"σigma", "ςigma"},   // Greek final vs standard sigma
		{"K", "\u212A"},      // ASCII K vs Kelvin sign
		{"café", "café"},     // NFC vs NFD
		{"straße", "STRAẞE"}, // ß vs capital sharp s share one SimpleFold orbit
	}
	for _, pair := range equal {
		if foldKey(pair[0]) != foldKey(pair[1]) {
			t.Errorf("FoldKey(%q) = %q != FoldKey(%q) = %q", pair[0], foldKey(pair[0]), pair[1], foldKey(pair[1]))
		}
	}
	unequal := [][2]string{
		{"demo", "daemon"},
		{"a", "b"},
	}
	for _, pair := range unequal {
		if foldKey(pair[0]) == foldKey(pair[1]) {
			t.Errorf("FoldKey(%q) == FoldKey(%q), want different", pair[0], pair[1])
		}
	}
}

func TestWriteFile_AtomicOverwrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "SKILL.mod")
	if err := WriteFile(path, []byte("first"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if err := WriteFile(path, []byte("second"), 0o644); err != nil {
		t.Fatalf("WriteFile overwrite: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "second" {
		t.Fatalf("content = %q, err = %v, want %q", data, err, "second")
	}
	leftovers, err := filepath.Glob(filepath.Join(dir, "*.tmp-*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(leftovers) != 0 {
		t.Fatalf("temporary files left behind: %v", leftovers)
	}
}

// readAll reads path, retrying only the transient sharing violations Windows
// reports while another goroutine replaces the file: MoveFileEx holds the
// target during replacement, and a concurrent open loses that race even
// though the write itself is correct. POSIX rename never errors for readers,
// so the retry is a no-op there. The write side applies the same retry inside
// Replace; the deadline bounds the wait against real failures.
func readAll(path string) ([]byte, error) {
	deadline := time.Now().Add(2 * time.Second)
	delay := time.Millisecond
	for {
		data, err := os.ReadFile(path)
		if err == nil {
			return data, nil
		}
		if !isTransientReadError(err) || time.Now().After(deadline) {
			return nil, err
		}
		time.Sleep(delay)
		if delay < 25*time.Millisecond {
			delay *= 2
		}
	}
}

func TestWriteFile_ConcurrentWriters(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "data")
	const writers, rounds = 8, 25
	payloads := make([][]byte, writers)
	for i := range payloads {
		payloads[i] = []byte(strings.Repeat(string(rune('a'+i)), 512))
	}
	var wg sync.WaitGroup
	for i := range payloads {
		wg.Add(1)
		go func(data []byte) {
			defer wg.Done()
			for range rounds {
				if err := WriteFile(path, data, 0o644); err != nil {
					t.Errorf("WriteFile: %v", err)
					return
				}
				// A reader must always observe one complete payload.
				got, err := readAll(path)
				if err != nil {
					t.Errorf("ReadFile: %v", err)
					return
				}
				complete := false
				for _, want := range payloads {
					if string(got) == string(want) {
						complete = true
						break
					}
				}
				if !complete {
					t.Errorf("concurrent read observed partial content %q", got)
					return
				}
			}
		}(payloads[i])
	}
	wg.Wait()
	leftovers, _ := filepath.Glob(filepath.Join(dir, "*.tmp-*"))
	if len(leftovers) != 0 {
		t.Fatalf("temporary files left behind: %v", leftovers)
	}
}

func TestWriteFile_FailuresLeaveNoTemporary(t *testing.T) {
	t.Run("missing parent", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "absent", "SKILL.mod")
		err := WriteFile(path, []byte("x"), 0o644)
		if err == nil || !strings.Contains(err.Error(), "write SKILL.mod") {
			t.Fatalf("err = %v, want wrapped write SKILL.mod failure", err)
		}
	})
	t.Run("failure keeps old content", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("Windows does not enforce POSIX directory permission bits")
		}
		if os.Geteuid() == 0 {
			t.Skip("root ignores directory permissions")
		}
		dir := t.TempDir()
		path := filepath.Join(dir, "SKILL.mod")
		if err := WriteFile(path, []byte("old"), 0o644); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
		if err := os.Chmod(dir, 0o555); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })
		err := WriteFile(path, []byte("new"), 0o644)
		if err == nil || !strings.Contains(err.Error(), "write SKILL.mod") {
			t.Fatalf("err = %v, want wrapped write SKILL.mod failure", err)
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil || string(data) != "old" {
			t.Fatalf("content = %q, err = %v, want old content preserved", data, readErr)
		}
		leftovers, _ := filepath.Glob(filepath.Join(dir, "*.tmp-*"))
		if len(leftovers) != 0 {
			t.Fatalf("temporary files left behind: %v", leftovers)
		}
	})
}

func TestReplace(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target")
	src := filepath.Join(dir, "source")
	if err := os.WriteFile(target, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(src, []byte("new"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Replace(src, target); err != nil {
		t.Fatalf("Replace: %v", err)
	}
	if _, err := os.Stat(src); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("source still exists after Replace: %v", err)
	}
	data, err := os.ReadFile(target)
	if err != nil || string(data) != "new" {
		t.Fatalf("target content = %q, err = %v, want %q", data, err, "new")
	}
}
