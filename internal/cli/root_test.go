// Copyright 2026 huija
//
// SPDX-License-Identifier: MIT

package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/huija/skillmod/internal/engine"
	"github.com/huija/skillmod/internal/i18n"
	"github.com/huija/skillmod/internal/install"
	"github.com/huija/skillmod/internal/modfile"
	"github.com/huija/skillmod/internal/store"
	"github.com/huija/skillmod/internal/testutil"
)

var errTestOutput = errors.New("test output failure")

type errorWriter struct{}

func (errorWriter) Write([]byte) (int, error) { return 0, errTestOutput }

func TestMain(m *testing.M) { testutil.RunMain(m) }

func TestHelpLanguage(t *testing.T) {
	t.Setenv(i18n.Env, "en")
	english := NewRootCmd()
	if !strings.Contains(english.Short, "declarations") {
		t.Fatalf("English Short = %q", english.Short)
	}
	get, _, err := english.Find([]string{"get"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(get.Use, "<repository>") || !strings.Contains(get.Short, "immutable reference") {
		t.Fatalf("English get help = %q / %q", get.Use, get.Short)
	}

	t.Setenv(i18n.Env, "zh")
	chinese := NewRootCmd()
	if chinese.Short == english.Short {
		t.Fatalf("Chinese and English Short are identical: %q", chinese.Short)
	}
}

func TestVersion(t *testing.T) {
	original := Version
	Version = "0.0.1"
	t.Cleanup(func() { Version = original })

	cmd := NewRootCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetArgs([]string{"--version"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "skillmod version 0.0.1") {
		t.Fatalf("version output = %q", out.String())
	}
}

func TestNewEngineAndIO(t *testing.T) {
	project, storeRoot := isolateCLI(t)
	options := &rootOptions{}
	eng, err := options.newEngine()
	if err != nil {
		t.Fatal(err)
	}
	if eng.Root != project || eng.Store.Root() != storeRoot {
		t.Fatalf("engine roots = project %q, store %q", eng.Root, eng.Store.Root())
	}
	if len(eng.Config.Agents) != 1 || eng.Config.Agents[0] != "agents" {
		t.Fatalf("engine config = %+v", eng.Config)
	}

	cmd := NewRootCmd()
	var stdout, stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	options.yes = true
	options.dryRun = true
	got := options.newIO(cmd)
	if got.Out != &stdout || !got.Yes {
		t.Fatalf("newIO = %+v", got)
	}
	if !options.mutationOptions().DryRun {
		t.Fatal("mutationOptions did not preserve --dry-run")
	}
	options.json = true
	got = options.newIO(cmd)
	if got.Out != &stderr {
		t.Fatalf("--json must route engine summaries to stderr; newIO = %+v", got)
	}
}

func TestOutputJSON(t *testing.T) {
	options := &rootOptions{json: true}
	cmd := NewRootCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	rep := &engine.Report{Action: engine.CommandVerify, Entries: []engine.EntryReport{{Name: "demo", Action: engine.ActionInstalled}}}
	if err := options.output(cmd, rep); err != nil {
		t.Fatal(err)
	}
	var got engine.Report
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, out.String())
	}
	if got.Action != rep.Action || len(got.Entries) != 1 || got.Entries[0].Name != "demo" {
		t.Fatalf("JSON report = %+v", got)
	}

	out.Reset()
	options.json = false
	if err := options.output(cmd, rep); err != nil || out.Len() != 0 {
		t.Fatalf("plain output = %q, err = %v", out.String(), err)
	}
	options.json = true
	if err := options.output(cmd, nil); err != nil || out.Len() != 0 {
		t.Fatalf("nil report output = %q, err = %v", out.String(), err)
	}
}

func TestJSONCommandsReturnOutputErrors(t *testing.T) {
	project, _ := isolateCLI(t)
	if err := modfile.SaveMod(project, &modfile.Mod{SchemaVersion: modfile.SchemaVersion}); err != nil {
		t.Fatalf("SaveMod(%q): %v", project, err)
	}
	if err := modfile.SaveLock(project, &modfile.Lock{SchemaVersion: modfile.SchemaVersion}); err != nil {
		t.Fatalf("SaveLock(%q): %v", project, err)
	}

	for _, name := range []string{"sync", "verify"} {
		t.Run(name, func(t *testing.T) {
			cmd := NewRootCmd()
			cmd.SetOut(errorWriter{})
			cmd.SetErr(io.Discard)
			cmd.SetArgs([]string{"--json", name})
			if err := cmd.Execute(); !errors.Is(err, errTestOutput) {
				t.Errorf("skillmod --json %s error = %v, want errors.Is(errTestOutput)", name, err)
			}
		})
	}
}

func TestRootCommandsDoNotShareFlagState(t *testing.T) {
	project, _ := isolateCLI(t)
	if err := modfile.SaveState(project,
		&modfile.Mod{SchemaVersion: modfile.SchemaVersion},
		&modfile.Lock{SchemaVersion: modfile.SchemaVersion}); err != nil {
		t.Fatal(err)
	}

	jsonCmd := NewRootCmd()
	var jsonOut bytes.Buffer
	jsonCmd.SetOut(&jsonOut)
	jsonCmd.SetErr(io.Discard)
	jsonCmd.SetArgs([]string{"--json", "list"})
	if err := jsonCmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if !json.Valid(jsonOut.Bytes()) {
		t.Fatalf("first command output = %q, want JSON", jsonOut.String())
	}

	plainCmd := NewRootCmd()
	var plainOut bytes.Buffer
	plainCmd.SetOut(&plainOut)
	plainCmd.SetErr(io.Discard)
	plainCmd.SetArgs([]string{"list"})
	if err := plainCmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if plainOut.Len() != 0 {
		t.Fatalf("second command inherited --json: %q", plainOut.String())
	}
}

func TestExitCodePrefersOutputFailure(t *testing.T) {
	rep := &engine.Report{Action: engine.CommandSync}
	partial := &engine.PartialError{Report: rep}
	if got := exitCode(errors.Join(partial, &outputError{err: errTestOutput})); got != ExitError {
		t.Fatalf("partial plus output failure exit = %d, want %d", got, ExitError)
	}
	drift := &engine.DriftError{Report: rep}
	if got := exitCode(errors.Join(drift, &outputError{err: errTestOutput})); got != ExitError {
		t.Fatalf("drift plus output failure exit = %d, want %d", got, ExitError)
	}
}

func TestCommandWiring(t *testing.T) {
	isolateCLI(t)

	// Init can complete locally without touching the project in dry-run mode.
	cmd := NewRootCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(io.Discard)
	cmd.SetArgs([]string{"--json", "--yes", "--dry-run", "init"})
	if err := cmd.Execute(); err != nil {
		t.Fatal(err)
	}
	// stdout must parse as JSON on its own; engine summaries go to stderr.
	var rep engine.Report
	if err := json.Unmarshal(out.Bytes(), &rep); err != nil {
		t.Fatalf("init stdout is not valid JSON: %v\n%s", err, out.String())
	}
	if rep.Action != "init" {
		t.Fatalf("init JSON = %+v", rep)
	}

	// Every remaining handler reaches the engine and returns the expected
	// missing-project error; get instead exercises its address validation path.
	for _, args := range [][]string{
		{"get", "github.com/acme/skills@"},
		{"sync"},
		{"list"},
		{"why", "missing"},
		{"update"},
		{"remove", "missing"},
		{"prune"},
		{"verify"},
	} {
		cmd := NewRootCmd()
		cmd.SetOut(io.Discard)
		cmd.SetErr(io.Discard)
		cmd.SetArgs(args)
		if err := cmd.Execute(); err == nil {
			t.Errorf("skillmod %s succeeded", strings.Join(args, " "))
		}
	}
}

func TestExecuteExitCodes(t *testing.T) {
	project, _ := isolateCLI(t)
	originalArgs := os.Args
	t.Cleanup(func() { os.Args = originalArgs })

	os.Args = []string{"skillmod", "--yes", "--dry-run", "init"}
	if got := Execute(); got != ExitOK {
		t.Fatalf("empty init exit = %d, want %d", got, ExitOK)
	}

	m := &modfile.Mod{SchemaVersion: modfile.SchemaVersion, Skills: []modfile.ModSkill{{Name: "demo", Local: true}}}
	if err := modfile.SaveMod(project, m); err != nil {
		t.Fatal(err)
	}
	l := &modfile.Lock{SchemaVersion: modfile.SchemaVersion, Skills: []modfile.LockSkill{{Name: "demo", Dirhash: testutil.DirHash("missing")}}}
	if err := modfile.SaveLock(project, l); err != nil {
		t.Fatal(err)
	}
	os.Args = []string{"skillmod", "verify"}
	if got := Execute(); got != ExitDrift {
		t.Fatalf("drift verify exit = %d, want %d", got, ExitDrift)
	}

	partialProject, partialStore := isolateCLI(t)
	t.Cleanup(func() {
		_ = filepath.WalkDir(partialStore, func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if entry.IsDir() {
				return os.Chmod(path, 0o700)
			}
			return os.Chmod(path, 0o600)
		})
	})
	r := testutil.NewRepo(t)
	r.WriteSkill("", "partial")
	r.CommitAll("init")
	r.Tag("v1.0.0")
	r.Finish()
	eng, err := (&rootOptions{}).newEngine()
	if err != nil {
		t.Fatalf("newEngine(): %v", err)
	}
	eng.Config.InstallMode = install.Copy
	if _, err := eng.Get(context.Background(), r.URL+"@v1.0.0", "", engine.IO{Yes: true, Out: io.Discard}); err != nil {
		t.Fatalf("Get(%q): %v", r.URL+"@v1.0.0", err)
	}
	target := filepath.Join(partialProject, ".agents", "skills", "partial", "SKILL.md")
	if err := os.WriteFile(target, []byte("local edit\n"), 0o644); err != nil {
		t.Fatalf("modify %q: %v", target, err)
	}
	os.Args = []string{"skillmod", "--yes", "sync"}
	if got := Execute(); got != ExitPartial {
		t.Fatalf("partial sync exit = %d, want %d", got, ExitPartial)
	}
}

func isolateCLI(t *testing.T) (project, storeRoot string) {
	t.Helper()
	project = t.TempDir()
	t.Chdir(project)
	storeRoot = filepath.Join(t.TempDir(), "store")
	t.Setenv(store.HomeEnv, storeRoot)
	configRoot := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configRoot)
	t.Setenv("HOME", configRoot)
	t.Setenv("USERPROFILE", configRoot)
	t.Setenv("AppData", configRoot)
	return project, storeRoot
}
