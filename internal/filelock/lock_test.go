// Copyright 2026 huija
//
// SPDX-License-Identifier: MIT

package filelock

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/huija/skillmod/internal/testutil"
)

func TestMain(m *testing.M) { testutil.RunMain(m) }

func TestLockCreatesParentAndSerializesAccess(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "store.lock")
	unlockFirst, err := Lock(path)
	if err != nil {
		t.Fatal(err)
	}
	if st, err := os.Stat(path); err != nil {
		unlockFirst()
		t.Fatal(err)
	} else if st.IsDir() {
		unlockFirst()
		t.Fatalf("lock path is a directory: %s", path)
	}

	type result struct {
		unlock func()
		err    error
	}
	acquired := make(chan result, 1)
	go func() {
		unlock, err := Lock(path)
		acquired <- result{unlock: unlock, err: err}
	}()

	select {
	case second := <-acquired:
		unlockFirst()
		if second.unlock != nil {
			second.unlock()
		}
		t.Fatalf("second Lock returned before release: %v", second.err)
	case <-time.After(50 * time.Millisecond):
		// Expected: the first owner still holds the advisory lock.
	}

	unlockFirst()
	select {
	case second := <-acquired:
		if second.err != nil {
			t.Fatal(second.err)
		}
		second.unlock()
	case <-time.After(2 * time.Second):
		t.Fatal("second Lock did not acquire after release")
	}
}

func TestLockReturnsParentCreationError(t *testing.T) {
	parent := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(parent, []byte("not a directory"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Lock(filepath.Join(parent, "store.lock")); err == nil {
		t.Fatal("Lock succeeded below a regular file")
	}
}

func TestLockReturnsOpenError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "store.lock")
	if err := os.Mkdir(path, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := Lock(path); err == nil {
		t.Fatal("Lock succeeded when the lock path was a directory")
	}
}
