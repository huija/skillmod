// Copyright 2026 huija
//
// SPDX-License-Identifier: MIT

package upgrade

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/huija/skillmod/internal/fsutil"
	"github.com/huija/skillmod/internal/i18n"
)

// oldExecutablePrefix marks a previous executable set aside because the
// platform refused to overwrite the running image. Every leftover matching the
// prefix is removed by the next upgrade.
const oldExecutablePrefix = ".skillmod-old-"

// rename is a variable so tests can force individual steps of the aside
// fallback to fail. Production code always calls os.Rename.
var rename = os.Rename

// ExecutablePath returns the running executable with symlinks resolved, so an
// upgrade replaces the real file rather than a link pointing at it.
func ExecutablePath() (string, error) {
	path, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf(i18n.Text("upgrade.executable_path_unresolved"), err)
	}
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		return resolved, nil
	}
	return path, nil
}

// Executable extracts the skillmod binary from a release archive. The archive
// format is the one the release publishes for the platform: tar.gz elsewhere and
// zip on Windows.
func Executable(archive []byte, format string) ([]byte, error) {
	switch format {
	case "tar.gz":
		return executableFromTarGz(archive)
	case "zip":
		return executableFromZip(archive)
	default:
		return nil, fmt.Errorf(i18n.Text("upgrade.archive_format_unknown"), format)
	}
}

func executableFromTarGz(archive []byte) ([]byte, error) {
	gz, err := gzip.NewReader(bytes.NewReader(archive))
	if err != nil {
		return nil, fmt.Errorf(i18n.Text("upgrade.archive_unreadable"), "tar.gz", err)
	}
	defer func() { _ = gz.Close() }()
	reader := tar.NewReader(gz)
	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf(i18n.Text("upgrade.archive_unreadable"), "tar.gz", err)
		}
		if header.Typeflag != tar.TypeReg || !isExecutableName(header.Name) {
			continue
		}
		data, err := readLimited(reader, header.Size)
		if err != nil {
			return nil, err
		}
		return data, nil
	}
	return nil, fmt.Errorf(i18n.Text("upgrade.archive_missing_executable"), "tar.gz")
}

func executableFromZip(archive []byte) ([]byte, error) {
	reader, err := zip.NewReader(bytes.NewReader(archive), int64(len(archive)))
	if err != nil {
		return nil, fmt.Errorf(i18n.Text("upgrade.archive_unreadable"), "zip", err)
	}
	for _, file := range reader.File {
		if file.FileInfo().IsDir() || !isExecutableName(file.Name) {
			continue
		}
		opened, err := file.Open()
		if err != nil {
			return nil, fmt.Errorf(i18n.Text("upgrade.archive_unreadable"), "zip", err)
		}
		data, readErr := readLimited(opened, int64(file.UncompressedSize64))
		closeErr := opened.Close()
		if readErr != nil {
			return nil, readErr
		}
		if closeErr != nil {
			return nil, closeErr
		}
		return data, nil
	}
	return nil, fmt.Errorf(i18n.Text("upgrade.archive_missing_executable"), "zip")
}

// isExecutableName matches the release binary at any directory depth. The
// archives place it at the root today; the base-name rule keeps the extractor
// working if a packaging change nests it.
func isExecutableName(name string) bool {
	base := filepath.Base(filepath.FromSlash(name))
	return base == "skillmod" || base == "skillmod.exe"
}

func readLimited(reader io.Reader, size int64) ([]byte, error) {
	if size > maxArchiveBytes {
		return nil, fmt.Errorf(i18n.Text("upgrade.executable_too_large"), size, int64(maxArchiveBytes))
	}
	data, err := io.ReadAll(io.LimitReader(reader, maxArchiveBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > maxArchiveBytes {
		return nil, fmt.Errorf(i18n.Text("upgrade.executable_too_large"), len(data), int64(maxArchiveBytes))
	}
	return data, nil
}

// stage writes the new executable beside the target so the final swap stays on
// one filesystem, and makes it executable. The handle is closed before the path
// is returned: the caller only owns the path, and the rename that installs it
// must not run against a descriptor this process still holds open, which some
// platforms refuse.
func stage(target string, data []byte) (string, error) {
	dir := filepath.Dir(target)
	file, err := os.CreateTemp(dir, ".skillmod-upgrade-*")
	if err != nil {
		return "", fmt.Errorf(i18n.Text("upgrade.stage_failed"), dir, err)
	}
	name := file.Name()
	writeErr := writeExecutable(file, data)
	closeErr := file.Close()
	if err := errors.Join(writeErr, closeErr); err != nil {
		_ = os.Remove(name)
		return "", fmt.Errorf(i18n.Text("upgrade.stage_failed"), dir, err)
	}
	return name, nil
}

func writeExecutable(file *os.File, data []byte) error {
	if _, err := file.Write(data); err != nil {
		return err
	}
	if err := file.Chmod(0o755); err != nil {
		return err
	}
	return file.Sync()
}

// replace installs staged as target. Atomic replacement is attempted first;
// where the platform refuses to replace a running executable, the running file
// is renamed aside instead, because a running image can be renamed even though
// it cannot be overwritten. The aside copy is removed by the next upgrade.
func replace(staged, target string) error {
	replaceErr := fsutil.Replace(staged, target)
	if replaceErr == nil {
		return nil
	}
	removeStaleAsides(target)
	aside, err := reserveAsideName(target)
	if err != nil {
		return fmt.Errorf(i18n.Text("upgrade.replace_failed"), target, errors.Join(replaceErr, err))
	}
	if err := rename(target, aside); err != nil {
		_ = removeIfPresent(aside)
		return fmt.Errorf(i18n.Text("upgrade.replace_failed"), target, errors.Join(replaceErr, err))
	}
	if err := rename(staged, target); err != nil {
		if back := rename(aside, target); back != nil {
			// The rollback failed too, so the previous executable now lives
			// only at aside. The error has to name that location: a plain
			// "cannot replace" would send the user looking for a file that is
			// no longer at target.
			return fmt.Errorf(i18n.Text("upgrade.replace_rollback_failed"), target, aside, errors.Join(replaceErr, err, back))
		}
		return fmt.Errorf(i18n.Text("upgrade.replace_failed"), target, err)
	}
	// Best effort: on Windows the running image still holds the old file open.
	_ = removeIfPresent(aside)
	return nil
}

// reserveAsideName returns a unique aside path beside the target. The name is
// made unique by randomness rather than by creating the file, because on
// Windows a rename cannot replace an existing destination. Uniqueness keeps
// concurrent upgrades from deleting each other's aside copy.
func reserveAsideName(target string) (string, error) {
	var random [8]byte
	if _, err := rand.Read(random[:]); err != nil {
		return "", err
	}
	return target + oldExecutablePrefix + hex.EncodeToString(random[:]), nil
}

// removeStaleAsides deletes aside copies left behind by earlier upgrades, for
// example when the running image kept the old file open on Windows. A copy set
// aside by a concurrently running upgrade matches the prefix too; a concurrent
// replacement of the same executable is already a lost race, and this is a
// known limitation rather than a guarantee under concurrency.
func removeStaleAsides(target string) {
	dir := filepath.Dir(target)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	prefix := filepath.Base(target) + oldExecutablePrefix
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), prefix) {
			_ = os.Remove(filepath.Join(dir, entry.Name()))
		}
	}
}

func removeIfPresent(path string) error {
	err := os.Remove(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return err
}
