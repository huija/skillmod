// Copyright 2026 huija
//
// SPDX-License-Identifier: MIT

package dirhash

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/huija/skillmod/internal/testutil"
)

func TestMain(m *testing.M) { testutil.RunMain(m) }

func TestHashBlobsSortsWithoutMutatingInput(t *testing.T) {
	files := []string{"nested/b.txt", "a.txt"}
	original := append([]string(nil), files...)
	contents := map[string]string{"a.txt": "alpha\n", "nested/b.txt": "beta\n"}
	opened := make([]string, 0, len(files))

	got, err := HashBlobs(files, func(name string) (io.ReadCloser, error) {
		opened = append(opened, name)
		return io.NopCloser(strings.NewReader(contents[name])), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(got, "h1:") {
		t.Fatalf("hash = %q, want h1 prefix", got)
	}
	if !reflect.DeepEqual(files, original) {
		t.Fatalf("HashBlobs mutated input: got %v, want %v", files, original)
	}
	if !reflect.DeepEqual(opened, []string{"a.txt", "nested/b.txt"}) {
		t.Fatalf("open order = %v, want sorted paths", opened)
	}
}

func TestHashBlobsPropagatesOpenError(t *testing.T) {
	want := errors.New("cannot open")
	_, err := HashBlobs([]string{"a.txt"}, func(string) (io.ReadCloser, error) {
		return nil, want
	})
	if !errors.Is(err, want) {
		t.Fatalf("error = %v, want %v", err, want)
	}
}

func TestHashDirMatchesBlobHash(t *testing.T) {
	dir := t.TempDir()
	files := map[string][]byte{
		"SKILL.md":       []byte("---\nname: demo\n---\n"),
		"scripts/run.sh": []byte("#!/bin/sh\necho ok\n"),
	}
	for name, data := range files {
		p := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, data, 0o644); err != nil {
			t.Fatal(err)
		}
	}

	got, err := HashDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	want, err := HashBlobs([]string{"scripts/run.sh", "SKILL.md"}, func(name string) (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(files[name])), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("HashDir = %q, HashBlobs = %q", got, want)
	}
}

func TestHashDirRejectsEmptyMissingAndSymlink(t *testing.T) {
	for name, dir := range map[string]string{
		"empty":   t.TempDir(),
		"missing": filepath.Join(t.TempDir(), "missing"),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := HashDir(dir); err == nil {
				t.Fatalf("HashDir(%q) succeeded", dir)
			}
		})
	}

	if runtime.GOOS == "windows" {
		t.Skip("creating symlinks may require elevated Windows privileges")
	}
	dir := t.TempDir()
	target := filepath.Join(dir, "target.txt")
	if err := os.WriteFile(target, []byte("target"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("target.txt", filepath.Join(dir, "link.txt")); err != nil {
		t.Fatal(err)
	}
	if _, err := HashDir(dir); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("symlink error = %v", err)
	}
}

func TestValidate(t *testing.T) {
	if err := Validate("h1:anything"); err != nil {
		t.Fatalf("valid hash rejected: %v", err)
	}
	if err := Validate("sha256:anything"); err == nil || !strings.Contains(err.Error(), "h1:") {
		t.Fatalf("invalid hash error = %v", err)
	}
}
