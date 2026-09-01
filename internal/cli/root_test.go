// Copyright 2026 huija
//
// SPDX-License-Identifier: MIT

package cli

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/huija/skillmod/internal/engine"
	"github.com/huija/skillmod/internal/i18n"
	"github.com/huija/skillmod/internal/modfile"
	"github.com/huija/skillmod/internal/store"
	"github.com/huija/skillmod/internal/testutil"
)

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

func TestErrorLanguage(t *testing.T) {
	err := &Error{What: "what", Why: "why", Advice: "advice"}

	t.Setenv(i18n.Env, "en")
	if got := err.Error(); got != "Error: what\nCause: why\nAdvice: advice" {
		t.Fatalf("English error = %q", got)
	}

	t.Setenv(i18n.Env, "zh")
	if got := err.Error(); got == "Error: what\nCause: why\nAdvice: advice" ||
		!strings.Contains(got, "what") || !strings.Contains(got, "why") || !strings.Contains(got, "advice") {
		t.Fatalf("localized error = %q", got)
	}
}

func TestNewEngineAndIO(t *testing.T) {
	preserveFlags(t)
	project, storeRoot := isolateCLI(t)
	eng, err := newEngine()
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
	flagYes = true
	flagDryRun = true
	got := newIO(cmd)
	if got.Out != &stdout || got.Err != &stderr || !got.Yes || !got.DryRun {
		t.Fatalf("newIO = %+v", got)
	}
}

func TestOutputJSON(t *testing.T) {
	preserveFlags(t)
	cmd := NewRootCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	flagJSON = true
	rep := &engine.Report{Action: "verify", Entries: []engine.EntryReport{{Name: "demo", Action: "ok"}}}
	if err := output(cmd, rep); err != nil {
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
	flagJSON = false
	if err := output(cmd, rep); err != nil || out.Len() != 0 {
		t.Fatalf("plain output = %q, err = %v", out.String(), err)
	}
	flagJSON = true
	if err := output(cmd, nil); err != nil || out.Len() != 0 {
		t.Fatalf("nil report output = %q, err = %v", out.String(), err)
	}
}

func TestCommandWiring(t *testing.T) {
	preserveFlags(t)
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
	if !strings.Contains(out.String(), `"action": "init"`) {
		t.Fatalf("init JSON = %q", out.String())
	}

	// Every remaining handler reaches the engine and returns the expected
	// missing-project error; get instead exercises its address validation path.
	for _, args := range [][]string{
		{"get", "github.com/acme/skills@"},
		{"sync"},
		{"list"},
		{"update"},
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
	preserveFlags(t)
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
	l := &modfile.Lock{Skills: []modfile.LockSkill{{Name: "demo", Dirhash: "h1:missing"}}}
	if err := modfile.SaveLock(project, l); err != nil {
		t.Fatal(err)
	}
	os.Args = []string{"skillmod", "verify"}
	if got := Execute(); got != ExitDrift {
		t.Fatalf("drift verify exit = %d, want %d", got, ExitDrift)
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
	t.Setenv("AppData", configRoot)
	return project, storeRoot
}

func preserveFlags(t *testing.T) {
	t.Helper()
	jsonFlag, yesFlag, dryRunFlag := flagJSON, flagYes, flagDryRun
	t.Cleanup(func() {
		flagJSON, flagYes, flagDryRun = jsonFlag, yesFlag, dryRunFlag
	})
}
