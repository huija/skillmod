// Copyright 2026 huija
//
// SPDX-License-Identifier: MIT

package engine

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestShareWaitsForManifestScopeLock(t *testing.T) {
	root := t.TempDir()
	managed := filepath.Join(root, ".agents", "skills", "hello")
	if err := os.MkdirAll(managed, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(managed, "SKILL.md"), []byte("---\nname: hello\ndescription: test skill\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	eng := &Engine{Root: root}
	unlock, err := eng.lockState()
	if err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{})
	finished := make(chan error, 1)
	go func() {
		close(started)
		_, err := eng.Share(t.Context(), ShareOptions{All: true, Agents: []string{"claude"}}, IO{Yes: true})
		finished <- err
	}()
	<-started
	select {
	case err := <-finished:
		unlock()
		t.Fatalf("Share completed under a held scope lock: %v, want it to wait", err)
	case <-time.After(100 * time.Millisecond):
	}
	unlock()
	select {
	case err := <-finished:
		if err != nil {
			t.Errorf("Share after unlock = %v, want success", err)
		}
	case <-time.After(5 * time.Second):
		t.Error("Share did not complete after scope unlock")
	}
}
