// Copyright 2026 huija
//
// SPDX-License-Identifier: MIT

package upgrade

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/huija/skillmod/internal/testutil"
)

func TestMain(m *testing.M) { testutil.RunMain(m) }

// testBinary is the payload every staged archive carries.
const testBinary = "#!/bin/sh\necho upgraded\n"

func TestCompareVersions(t *testing.T) {
	tests := []struct {
		current, latest string
		want            int
	}{
		{current: "v0.0.2", latest: "v0.0.3", want: 1},
		{current: "v0.0.2", latest: "v0.0.2", want: 0},
		{current: "v0.0.2", latest: "v0.0.1", want: -1},
		// A development build after v0.0.2 must not be replaced by v0.0.2.
		{current: "v0.0.2-5-gabc1234", latest: "v0.0.2", want: 0},
		{current: "v0.0.2-5-gabc1234", latest: "v0.0.3", want: 1},
		// An unversioned build is older than any release.
		{current: "dev", latest: "v0.0.2", want: 1},
		{current: "(devel)", latest: "v0.0.2", want: 1},
	}
	for _, tt := range tests {
		got := CompareVersions(tt.current, tt.latest)
		switch {
		case tt.want > 0 && got <= 0, tt.want < 0 && got >= 0, tt.want == 0 && got != 0:
			t.Errorf("CompareVersions(%q, %q) = %d, want sign %d", tt.current, tt.latest, got, tt.want)
		}
	}
}

func TestFormatFor(t *testing.T) {
	if got := FormatFor("windows"); got != "zip" {
		t.Errorf("FormatFor(windows) = %q, want zip", got)
	}
	if got := FormatFor("linux"); got != "tar.gz" {
		t.Errorf("FormatFor(linux) = %q, want tar.gz", got)
	}
}

func TestParseChecksums(t *testing.T) {
	sums := parseChecksums(strings.Join([]string{
		"# a comment line",
		"",
		strings.Repeat("a", 64) + "  skillmod_0.0.2_linux_amd64.tar.gz",
		strings.Repeat("b", 64) + " *skillmod_0.0.2_windows_amd64.zip",
		"not-a-digest  file",
		strings.Repeat("c", 63) + "  short-digest",
	}, "\n"))
	if len(sums) != 2 {
		t.Fatalf("parseChecksums = %v, want two digests", sums)
	}
	if sums["skillmod_0.0.2_linux_amd64.tar.gz"] != strings.Repeat("a", 64) {
		t.Errorf("linux digest = %q", sums["skillmod_0.0.2_linux_amd64.tar.gz"])
	}
	if sums["skillmod_0.0.2_windows_amd64.zip"] != strings.Repeat("b", 64) {
		t.Errorf("windows digest = %q", sums["skillmod_0.0.2_windows_amd64.zip"])
	}
}

func TestExecutable(t *testing.T) {
	tarGz := tarGzArchive(t, map[string]string{"LICENSE": "mit", "skillmod": testBinary})
	got, err := Executable(tarGz, "tar.gz")
	if err != nil {
		t.Fatalf("Executable(tar.gz): %v", err)
	}
	if string(got) != testBinary {
		t.Errorf("Executable(tar.gz) = %q, want the binary payload", got)
	}

	zipData := zipArchive(t, map[string]string{"skillmod.exe": testBinary})
	got, err = Executable(zipData, "zip")
	if err != nil {
		t.Fatalf("Executable(zip): %v", err)
	}
	if string(got) != testBinary {
		t.Errorf("Executable(zip) = %q, want the binary payload", got)
	}

	if _, err := Executable(tarGzArchive(t, map[string]string{"README.md": "docs"}), "tar.gz"); err == nil {
		t.Error("Executable accepted an archive without a skillmod executable")
	}
	if _, err := Executable([]byte("not an archive"), "tar.gz"); err == nil {
		t.Error("Executable accepted an unreadable archive")
	}
	if _, err := Executable(tarGz, "rar"); err == nil {
		t.Error("Executable accepted an unknown format")
	}
}

func TestRunInstallsVerifiedRelease(t *testing.T) {
	format := FormatFor(runtime.GOOS)
	archive := tarGzArchive(t, map[string]string{"skillmod": testBinary})
	if format == "zip" {
		archive = zipArchive(t, map[string]string{"skillmod.exe": testBinary})
	}
	server := newReleaseServer(t, releaseServerOptions{Tag: "v9.9.9", Format: format, Archive: archive})
	defer server.Close()

	target := filepath.Join(t.TempDir(), executableName())
	if err := os.WriteFile(target, []byte("old-binary"), 0o755); err != nil {
		t.Fatal(err)
	}

	result, err := Run(context.Background(), Options{
		APIBase: server.URL, Current: "v0.0.2", Target: target,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !result.Updated || result.Status != StatusAvailable || result.Latest != "v9.9.9" {
		t.Fatalf("Run result = %+v, want an installed v9.9.9", result)
	}
	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != testBinary {
		t.Fatalf("target = %q, want the verified release payload", data)
	}
	info, err := os.Stat(target)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o100 == 0 {
		t.Errorf("replaced executable mode = %o, want the executable bit set", info.Mode().Perm())
	}
	assertNoStagingLeftovers(t, filepath.Dir(target))
}

func TestRunIsNoOpWhenAlreadyCurrent(t *testing.T) {
	format := FormatFor(runtime.GOOS)
	server := newReleaseServer(t, releaseServerOptions{
		Tag: "v0.0.2", Format: format, Archive: tarGzArchive(t, map[string]string{"skillmod": testBinary}),
		BlockDownloads: true,
	})
	defer server.Close()

	result, err := Run(context.Background(), Options{
		APIBase: server.URL, Current: "v0.0.2", Target: filepath.Join(t.TempDir(), executableName()),
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Status != StatusCurrent || result.Updated || result.Latest != "v0.0.2" {
		t.Fatalf("Run result = %+v, want an up-to-date no-op", result)
	}
}

func TestRunCheckDoesNotDownload(t *testing.T) {
	format := FormatFor(runtime.GOOS)
	server := newReleaseServer(t, releaseServerOptions{
		Tag: "v9.9.9", Format: format, Archive: tarGzArchive(t, map[string]string{"skillmod": testBinary}),
		BlockDownloads: true,
	})
	defer server.Close()

	result, err := Run(context.Background(), Options{
		APIBase: server.URL, Current: "v0.0.2", Check: true,
		Target: filepath.Join(t.TempDir(), executableName()),
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Status != StatusAvailable || result.Updated || result.Asset != "" {
		t.Fatalf("Run result = %+v, want availability without a download", result)
	}
}

func TestRunDryRunKeepsTheExecutable(t *testing.T) {
	format := FormatFor(runtime.GOOS)
	archive := tarGzArchive(t, map[string]string{"skillmod": testBinary})
	if format == "zip" {
		archive = zipArchive(t, map[string]string{"skillmod.exe": testBinary})
	}
	server := newReleaseServer(t, releaseServerOptions{Tag: "v9.9.9", Format: format, Archive: archive})
	defer server.Close()

	target := filepath.Join(t.TempDir(), executableName())
	if err := os.WriteFile(target, []byte("old-binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	result, err := Run(context.Background(), Options{
		APIBase: server.URL, Current: "v0.0.2", DryRun: true, Target: target,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Updated || result.Status != StatusAvailable || result.Asset == "" {
		t.Fatalf("Run result = %+v, want a verified but unapplied upgrade", result)
	}
	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "old-binary" {
		t.Fatalf("target = %q, want the running executable kept", data)
	}
	assertNoStagingLeftovers(t, filepath.Dir(target))
}

func TestRunRejectsChecksumMismatch(t *testing.T) {
	format := FormatFor(runtime.GOOS)
	// The server publishes a digest for a different payload.
	server := newReleaseServer(t, releaseServerOptions{
		Tag: "v9.9.9", Format: format, Archive: tarGzArchive(t, map[string]string{"skillmod": testBinary}),
		Mismatch: true,
	})
	defer server.Close()

	target := filepath.Join(t.TempDir(), executableName())
	if err := os.WriteFile(target, []byte("old-binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	_, err := Run(context.Background(), Options{
		APIBase: server.URL, Current: "v0.0.2", Target: target,
		HTTP: server.Client(),
	})
	if err == nil {
		t.Fatal("Run installed an archive whose checksum does not match")
	}
	if !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("Run error = %v, want a checksum diagnostic", err)
	}
	data, readErr := os.ReadFile(target)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(data) != "old-binary" {
		t.Fatalf("target = %q, want the running executable left alone", data)
	}
	assertNoStagingLeftovers(t, filepath.Dir(target))
}

func TestRunRequiresATargetAndKnownRelease(t *testing.T) {
	if _, err := Run(context.Background(), Options{Current: "v0.0.2"}); err == nil {
		t.Error("Run accepted an empty target")
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer server.Close()
	_, err := Run(context.Background(), Options{
		APIBase: server.URL, Current: "v0.0.2", Tag: "v9.9.9",
		Target: filepath.Join(t.TempDir(), executableName()),
	})
	if err == nil || !strings.Contains(err.Error(), "v9.9.9") {
		t.Fatalf("Run error = %v, want a missing-release diagnostic naming the tag", err)
	}
	// The latest endpoint answers 404 when the repository has no releases at
	// all; the message must not render an empty tag name.
	_, err = Run(context.Background(), Options{
		APIBase: server.URL, Current: "v0.0.2",
		Target: filepath.Join(t.TempDir(), executableName()),
	})
	if err == nil || !strings.Contains(err.Error(), "no published release") {
		t.Fatalf("Run error = %v, want a no-releases diagnostic", err)
	}
}

// releaseServerOptions configures the fake release endpoint.
type releaseServerOptions struct {
	Tag     string
	Format  string
	Archive []byte
	// Mismatch publishes a checksum for a different payload.
	Mismatch bool
	// BlockDownloads fails the test if any artifact request is made, which is
	// how --check proves it never downloads.
	BlockDownloads bool
}

// newReleaseServer serves release metadata, checksums, and the archive.
func newReleaseServer(t *testing.T, options releaseServerOptions) *httptest.Server {
	t.Helper()
	name := fmt.Sprintf("skillmod_%s_%s_%s.%s",
		strings.TrimPrefix(options.Tag, "v"), runtime.GOOS, runtime.GOARCH, options.Format)
	digest := sha256.Sum256(options.Archive)
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if options.BlockDownloads && strings.HasPrefix(r.URL.Path, "/download") {
			t.Errorf("unexpected artifact request to %s", r.URL.Path)
			http.Error(w, "unexpected", http.StatusInternalServerError)
			return
		}
		switch r.URL.Path {
		case "/repos/huija/skillmod/releases/latest":
			base := "http://" + r.Host + "/download/"
			_, _ = fmt.Fprintf(w, `{"tag_name":%q,"assets":[{"name":%q,"browser_download_url":%q},{"name":%q,"browser_download_url":%q}]}`,
				options.Tag, ChecksumAsset, base+ChecksumAsset, name, base+name)
		case "/download/" + ChecksumAsset:
			published := digest
			if options.Mismatch {
				published = sha256.Sum256([]byte("a different payload"))
			}
			_, _ = fmt.Fprintf(w, "%s  %s\n", hex.EncodeToString(published[:]), name)
		case "/download/" + name:
			_, _ = w.Write(options.Archive)
		default:
			http.NotFound(w, r)
		}
	}))
}

// tarGzArchive builds a release-shaped tar.gz archive.
func tarGzArchive(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buffer bytes.Buffer
	gz := gzip.NewWriter(&buffer)
	tarWriter := tar.NewWriter(gz)
	for name, content := range files {
		if err := tarWriter.WriteHeader(&tar.Header{
			Name: name, Mode: 0o755, Size: int64(len(content)), Typeflag: tar.TypeReg,
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := tarWriter.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tarWriter.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

// zipArchive builds a release-shaped zip archive.
func zipArchive(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	for name, content := range files {
		entry, err := writer.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := entry.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func executableName() string {
	if runtime.GOOS == "windows" {
		return "skillmod.exe"
	}
	return "skillmod"
}

// assertNoStagingLeftovers keeps the upgrade from littering the executable's
// directory, which on Windows can hold a locked copy of the running image.
func assertNoStagingLeftovers(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".skillmod-upgrade-") {
			t.Errorf("staging file %s remains in %s", entry.Name(), dir)
		}
	}
}

// A declined request names the likely cause instead of leaving the status code
// to be looked up.
func TestRunExplainsADeclinedRequest(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "rate limit exceeded", http.StatusForbidden)
	}))
	defer server.Close()

	_, err := Run(context.Background(), Options{
		APIBase: server.URL, Current: "v0.0.2", Tag: "v9.9.9",
		Target: filepath.Join(t.TempDir(), executableName()),
	})
	if err == nil {
		t.Fatal("Run installed a release after the API declined the request")
	}
	for _, want := range []string{"403", "quota"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("Run error = %q, want it to mention %q", err, want)
		}
	}
}

// useFailingRename swaps the rename used by the aside fallback and restores the
// production one when the test finishes.
func useFailingRename(t *testing.T, fail func(from, to string) bool) {
	t.Helper()
	previous := rename
	t.Cleanup(func() { rename = previous })
	rename = func(from, to string) error {
		if fail(from, to) {
			return os.ErrPermission
		}
		return os.Rename(from, to)
	}
}

// asideCopies lists the leftover aside files in a directory.
func asideCopies(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var copies []string
	for _, entry := range entries {
		if strings.Contains(entry.Name(), oldExecutablePrefix) {
			copies = append(copies, filepath.Join(dir, entry.Name()))
		}
	}
	return copies
}

// When the staged→target swap fails, the previous executable must be renamed
// back and no aside copy may remain. The fallback also clears stale aside
// copies from earlier upgrades.
func TestReplaceRestoresTheTargetWhenTheSwapFails(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, executableName())
	if err := os.WriteFile(target, []byte("old-binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	stale := target + oldExecutablePrefix + "00000000"
	if err := os.WriteFile(stale, []byte("older-binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	// staged does not exist, so the atomic replacement fails and replace walks
	// the aside fallback; the second rename then fails for the same reason and
	// the rollback has to put the running executable back.
	err := replace(filepath.Join(dir, "missing-staged"), target)
	if err == nil {
		t.Fatal("replace succeeded although the staged file does not exist")
	}
	if strings.Contains(err.Error(), oldExecutablePrefix) {
		t.Errorf("replace error = %q, want the plain replace diagnostic when the rollback succeeds", err)
	}
	data, readErr := os.ReadFile(target)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(data) != "old-binary" {
		t.Fatalf("target = %q, want the previous executable restored", data)
	}
	if copies := asideCopies(t, dir); len(copies) != 0 {
		t.Errorf("aside copies remain after the rollback: %v", copies)
	}
}

// When the rollback fails too, the previous executable lives only at the aside
// path, and the error has to name it instead of reporting a bare failure.
func TestReplaceNamesTheAsideCopyWhenTheRollbackFails(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, executableName())
	if err := os.WriteFile(target, []byte("old-binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	// Every rename whose destination is the target fails: the atomic
	// replacement and the staged→target swap fail before replace is entered or
	// via the fallback, and the aside→target rollback fails with them.
	useFailingRename(t, func(from, to string) bool { return to == target })
	// staged does not exist, so the atomic replacement fails.
	err := replace(filepath.Join(dir, "missing-staged"), target)
	if err == nil {
		t.Fatal("replace succeeded although every rename onto the target was forced to fail")
	}
	copies := asideCopies(t, dir)
	if len(copies) != 1 {
		t.Fatalf("want exactly one aside copy holding the previous executable, got %v", copies)
	}
	data, readErr := os.ReadFile(copies[0])
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(data) != "old-binary" {
		t.Fatalf("aside copy = %q, want the previous executable", data)
	}
	if _, statErr := os.Stat(target); !os.IsNotExist(statErr) {
		t.Errorf("target still exists after a failed replace and rollback: %v", statErr)
	}
	if !strings.Contains(err.Error(), "kept at") || !strings.Contains(err.Error(), filepath.Base(copies[0])) {
		t.Errorf("replace error = %q, want it to point at the aside copy %q", err, copies[0])
	}
}
