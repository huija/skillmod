// Copyright 2026 huija
//
// SPDX-License-Identifier: MIT

package cli

import (
	"bytes"
	"encoding/json"
	"errors"
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
	if got.Out != &stdout || !got.Yes || !got.DryRun {
		t.Fatalf("newIO = %+v", got)
	}
	flagJSON = true
	got = newIO(cmd)
	if got.Out != &stderr {
		t.Fatalf("--json must route engine summaries to stderr; newIO = %+v", got)
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

func TestJSONCommandsReturnOutputErrors(t *testing.T) {
	preserveFlags(t)
	project, _ := isolateCLI(t)
	if err := modfile.SaveMod(project, &modfile.Mod{SchemaVersion: modfile.SchemaVersion}); err != nil {
		t.Fatalf("SaveMod(%q): %v", project, err)
	}
	if err := modfile.SaveLock(project, &modfile.Lock{}); err != nil {
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
	t.Setenv("USERPROFILE", configRoot)
	t.Setenv("AppData", configRoot)
	return project, storeRoot
}

func preserveFlags(t *testing.T) {
	t.Helper()
	jsonFlag, yesFlag, dryRunFlag, globalFlag := flagJSON, flagYes, flagDryRun, flagGlobal
	installMode := flagInstallMode
	t.Cleanup(func() {
		flagJSON, flagYes, flagDryRun, flagGlobal = jsonFlag, yesFlag, dryRunFlag, globalFlag
		flagInstallMode = installMode
	})
}
