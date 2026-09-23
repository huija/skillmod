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

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

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
	if eng.Config == nil {
		t.Fatal("engine has no configuration")
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
	if got.Out != io.Discard {
		t.Fatalf("--json must discard engine summaries; newIO.Out = %+v", got.Out)
	}
	if got.Progress != nil {
		t.Fatal("--json must not attach an interactive progress writer")
	}
}

func TestOutputJSON(t *testing.T) {
	options := &rootOptions{json: true}
	cmd := NewRootCmd()
	var out bytes.Buffer
	cmd.SetOut(&out)
	rep := &engine.Report{Action: engine.CommandVerify, Entries: []engine.EntryReport{{Name: "demo", Action: engine.ActionInstalled}}}
	if err := options.output(cmd, rep, nil); err != nil {
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
	if err := options.output(cmd, rep, nil); err != nil || out.Len() != 0 {
		t.Fatalf("plain output = %q, err = %v", out.String(), err)
	}
	options.json = true
	if err := options.output(cmd, nil, nil); err != nil || out.Len() != 0 {
		t.Fatalf("nil report output = %q, err = %v", out.String(), err)
	}
}

// TestOutputJSONOnError pins the contract that makes --json usable in
// automation: a failing run still writes exactly one document, naming the
// failure, and the human error stays off stdout.
func TestOutputJSONOnError(t *testing.T) {
	options := &rootOptions{json: true}
	root := NewRootCmd()
	verify, _, err := root.Find([]string{"verify"})
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	verify.SetOut(&out)

	failure := errors.New("no SKILL.mod found")
	// jsonEmitted is the signal execute reads to decide whether a stderr
	// diagnostic is still owed, and execute is what resets it. Both directions
	// are asserted from a known starting state, so neither depends on which
	// test happened to run first.
	jsonEmitted = false
	options.json = false
	if err := options.output(verify, nil, failure); err != nil {
		t.Fatal(err)
	}
	if jsonEmitted {
		t.Fatal("a plain run recorded that it emitted a JSON document")
	}

	options.json = true
	if err := options.output(verify, nil, failure); err != nil {
		t.Fatal(err)
	}
	var got engine.Report
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("error output is not JSON: %v\n%s", err, out.String())
	}
	if got.Action != engine.CommandVerify || got.Error != failure.Error() {
		t.Fatalf("error report = %+v, want action %q and error %q", got, engine.CommandVerify, failure.Error())
	}
	if !jsonEmitted {
		t.Fatal("output did not record that it emitted a document")
	}

	// A report that completed alongside an error keeps its entries and carries
	// the reason the command failed.
	out.Reset()
	rep := &engine.Report{Action: engine.CommandSync, Entries: []engine.EntryReport{{Name: "demo", Action: engine.ActionInstall}}}
	if err := options.output(verify, rep, &engine.PartialError{Report: rep}); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("partial output is not JSON: %v\n%s", err, out.String())
	}
	if len(got.Entries) != 1 || got.Error == "" {
		t.Fatalf("partial report = %+v, want the entries and the error", got)
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

// TestJSONFallbackDocumentForHandlerFailures pins the other half of the
// single-document contract: a handler that fails before reaching output — an
// unknown --install-mode is the reachable case, because its validation lives
// in newEngine rather than in a cobra flag — still writes the document, while
// a flag error, which reaches no handler at all, keeps its plain stderr text.
func TestJSONFallbackDocumentForHandlerFailures(t *testing.T) {
	project, _ := isolateCLI(t)
	if err := modfile.SaveState(project,
		&modfile.Mod{SchemaVersion: modfile.SchemaVersion},
		&modfile.Lock{SchemaVersion: modfile.SchemaVersion}); err != nil {
		t.Fatal(err)
	}

	run := func(args ...string) (code int, stdout, stderr string) {
		t.Helper()
		cmd, options := newRootCmd()
		var out, errOut bytes.Buffer
		cmd.SetOut(&out)
		cmd.SetErr(&errOut)
		cmd.SetArgs(args)
		return execute(cmd, options), out.String(), errOut.String()
	}

	code, stdout, stderr := run("--json", "--install-mode", "bogus", "list")
	if code != ExitError {
		t.Fatalf("handler failure exit code = %d, want %d", code, ExitError)
	}
	var rep struct {
		Action  string `json:"action"`
		Entries []any  `json:"entries"`
		Error   string `json:"error"`
	}
	if err := json.Unmarshal([]byte(stdout), &rep); err != nil {
		t.Fatalf("stdout is not exactly one JSON document: %v\n%s", err, stdout)
	}
	if rep.Action != "list" || rep.Error == "" {
		t.Errorf("document = %+v, want the list action and the failure named", rep)
	}
	if rep.Entries == nil {
		t.Errorf("document = %s, want an entries array", stdout)
	}
	if stderr != "" {
		t.Errorf("stderr = %q, want the document to carry the failure", stderr)
	}

	// A flag error never reaches a handler, so it stays plain text with an
	// empty stdout — the contract the document explicitly exempts. The
	// diagnostic goes to the process's stderr, so it is captured there rather
	// than from the cobra buffer.
	oldStderr := os.Stderr
	pipe, write, pipeErr := os.Pipe()
	if pipeErr != nil {
		t.Fatal(pipeErr)
	}
	os.Stderr = write
	code, stdout, _ = run("--json", "--no-such-flag", "list")
	os.Stderr = oldStderr
	if closeErr := write.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}
	diagnostic, readErr := io.ReadAll(pipe)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if code != ExitError || stdout != "" {
		t.Errorf("flag error: code = %d, stdout = %q, want a plain failure", code, stdout)
	}
	if !strings.Contains(string(diagnostic), "no-such-flag") {
		t.Errorf("flag error stderr = %q, want the flag named", diagnostic)
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

// A flag typed often carries a one-letter shorthand, so the commands stay short
// to use. Reading the flags from the command tree keeps a new long-only flag
// from slipping in unnoticed, and the deliberately long-only flags are stated
// here rather than discovered in review.
func TestFlagsCarryShorthands(t *testing.T) {
	longOnly := map[string]string{
		"install-mode":    "the letter would be ambiguous",
		"allow-downgrade": "it overrides a safety check",
		"all":             "the letter is taken by --agent, and the long form reads better",
		"on-conflict":     "the policy names are long; a letter would hide the meaning",
	}
	root := NewRootCmd()
	check := func(cmd *cobra.Command) {
		cmd.Flags().VisitAll(func(flag *pflag.Flag) {
			switch flag.Name {
			case "help", "version":
				return // Added by cobra, not part of the documented surface.
			}
			reason, allowed := longOnly[flag.Name]
			switch {
			case flag.Shorthand == "" && !allowed:
				t.Errorf("%s --%s has no shorthand", cmd.CommandPath(), flag.Name)
			case flag.Shorthand != "" && allowed:
				t.Errorf("%s --%s has shorthand %q, but %s", cmd.CommandPath(), flag.Name, flag.Shorthand, reason)
			}
		})
	}
	check(root)
	for _, cmd := range root.Commands() {
		switch cmd.Name() {
		case "help", "completion":
			continue // Generated by cobra for every command tree.
		}
		check(cmd)
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
