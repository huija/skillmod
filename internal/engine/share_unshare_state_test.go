// Copyright 2026 huija
//
// SPDX-License-Identifier: MIT

package engine

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/huija/skillmod/internal/dirhash"
	"github.com/huija/skillmod/internal/modfile"
)

func TestUnshareWaitsForManifestScopeLock(t *testing.T) {
	root := t.TempDir()
	managed := filepath.Join(root, ".agents", "skills", "hello")
	if err := os.MkdirAll(managed, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(managed, "SKILL.md"), []byte("---\nname: hello\ndescription: test skill\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	hash, err := dirhash.HashDir(managed)
	if err != nil {
		t.Fatal(err)
	}
	m := &modfile.Mod{SchemaVersion: modfile.SchemaVersion, Skills: []modfile.ModSkill{
		{Name: "hello", Local: true, Agents: []string{"claude"}},
	}}
	l := &modfile.Lock{SchemaVersion: modfile.SchemaVersion, Skills: []modfile.LockSkill{
		{Name: "hello", Dirhash: hash, Agents: []string{"claude"}},
	}}
	if err := modfile.SaveState(root, m, l); err != nil {
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
		_, err := eng.Share(t.Context(), ShareOptions{All: true, Remove: []string{"claude"}}, IO{Yes: true})
		finished <- err
	}()
	<-started
	select {
	case err := <-finished:
		unlock()
		t.Fatalf("Unshare completed under a held scope lock: %v, want it to wait", err)
	case <-time.After(100 * time.Millisecond):
	}
	unlock()
	select {
	case err := <-finished:
		if err != nil {
			t.Errorf("Unshare after unlock = %v, want success", err)
		}
	case <-time.After(5 * time.Second):
		t.Error("Unshare did not complete after scope unlock")
	}
}
