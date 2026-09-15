// Copyright 2026 huija
//
// SPDX-License-Identifier: MIT

// Package upgrade replaces the running skillmod executable with a published
// release. Downloads are verified against the release's own checksums.txt, so
// an upgrade follows the same trust path as the documented manual install.
package upgrade

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path"
	"runtime"
	"strings"
	"time"

	"golang.org/x/mod/semver"

	"github.com/huija/skillmod/internal/i18n"
	"github.com/huija/skillmod/internal/ui"
)

// DefaultAPIBase is the GitHub REST endpoint releases are read from.
const DefaultAPIBase = "https://api.github.com"

// Repository is the project whose releases are installed.
const Repository = "huija/skillmod"

// ChecksumAsset is the release file listing every archive digest.
const ChecksumAsset = "checksums.txt"

// maxArchiveBytes bounds a release archive so a wrong or hostile response
// cannot exhaust memory before extraction rejects it.
const maxArchiveBytes = 512 << 20

// Status is the outcome of comparing the running version with a release.
type Status string

const (
	// StatusCurrent means no newer release exists, or the requested release is
	// already installed.
	StatusCurrent Status = "current"
	// StatusAvailable means a different release is available to install.
	StatusAvailable Status = "available"
)

// Options describes one upgrade request. An empty OS, Arch, or Format is
// filled from the running platform.
type Options struct {
	Repository string       // "owner/name", defaults to Repository
	APIBase    string       // defaults to DefaultAPIBase
	Tag        string       // install this release tag instead of the latest
	Check      bool         // report availability without downloading
	DryRun     bool         // download and verify, but keep the running executable
	Current    string       // running version, such as "v0.0.2" or "dev"
	Target     string       // executable to replace
	OS         string       // defaults to runtime.GOOS
	Arch       string       // defaults to runtime.GOARCH
	Format     string       // "tar.gz" or "zip"; defaults from OS
	HTTP       *http.Client // defaults to a shared client with a 5 minute timeout
	Progress   ui.Progress  // optional activity indicator
}

// Result reports what an upgrade request found and did.
type Result struct {
	Status  Status
	Current string
	Latest  string // release tag that was selected
	Asset   string // downloaded archive name; empty when nothing was downloaded
	Updated bool   // the executable was replaced
}

// Run resolves the requested release and, unless asked only to check, installs
// it over Options.Target after verifying the published checksum.
func Run(ctx context.Context, options Options) (Result, error) {
	options.applyDefaults()
	// Status starts as current: every early return means nothing was installed.
	result := Result{Current: options.Current, Status: StatusCurrent}
	if options.Target == "" {
		return result, errors.New(i18n.Text("upgrade.executable_path_unknown"))
	}

	release, err := fetchRelease(ctx, options)
	if err != nil {
		return result, err
	}
	result.Latest = release.Tag
	// An explicit tag may deliberately move backwards; without one, only a
	// newer release is installed.
	if options.Tag == "" {
		if CompareVersions(options.Current, release.Tag) <= 0 {
			return result, nil
		}
	} else if release.Tag == options.Current {
		return result, nil
	}
	result.Status = StatusAvailable
	if options.Check {
		return result, nil
	}

	options.setProgress(i18n.Format("upgrade.downloading_release", release.Tag))
	archive, err := downloadRelease(ctx, options, release, &result)
	if err != nil {
		return result, err
	}
	if options.DryRun {
		return result, nil
	}

	executable, err := Executable(archive, options.Format)
	if err != nil {
		return result, err
	}
	staged, err := stage(options.Target, executable)
	if err != nil {
		return result, err
	}
	defer func() { _ = removeIfPresent(staged) }()
	if err := replace(staged, options.Target); err != nil {
		return result, err
	}
	result.Updated = true
	return result, nil
}

// fetchRelease reads the requested release metadata.
func fetchRelease(ctx context.Context, options Options) (*release, error) {
	endpoint := options.APIBase + "/repos/" + options.Repository + "/releases/latest"
	if options.Tag != "" {
		endpoint = options.APIBase + "/repos/" + options.Repository + "/releases/tags/" + options.Tag
	}
	body, err := fetch(ctx, options, endpoint)
	if err != nil {
		var status *statusError
		if errors.As(err, &status) && status.Code == http.StatusNotFound {
			// The latest endpoint answers 404 when the repository has no
			// releases at all, which deserves its own message rather than
			// "release "" was not found".
			if options.Tag == "" {
				return nil, fmt.Errorf(i18n.Text("upgrade.no_published_release"), options.Repository)
			}
			return nil, fmt.Errorf(i18n.Text("upgrade.release_not_found"), options.Tag, options.Repository)
		}
		return nil, err
	}
	var raw struct {
		TagName string `json:"tag_name"`
		Assets  []struct {
			Name string `json:"name"`
			URL  string `json:"browser_download_url"`
		} `json:"assets"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf(i18n.Text("upgrade.release_metadata_unreadable"), err)
	}
	rel := &release{Tag: raw.TagName}
	for _, entry := range raw.Assets {
		rel.Assets = append(rel.Assets, asset{Name: entry.Name, URL: entry.URL})
	}
	if rel.Tag == "" {
		return nil, fmt.Errorf(i18n.Text("upgrade.release_metadata_unreadable"), i18n.Text("upgrade.release_tag_missing"))
	}
	return rel, nil
}

// downloadRelease selects, verifies, and returns the platform archive.
func downloadRelease(ctx context.Context, options Options, release *release, result *Result) ([]byte, error) {
	target, err := release.platformArchive(options.OS, options.Arch, options.Format)
	if err != nil {
		return nil, err
	}
	result.Asset = target.Name

	checksums, err := release.asset(ChecksumAsset)
	if err != nil {
		return nil, err
	}
	body, err := fetch(ctx, options, checksums.URL)
	if err != nil {
		return nil, err
	}
	want, ok := parseChecksums(string(body))[target.Name]
	if !ok {
		return nil, fmt.Errorf(i18n.Text("upgrade.checksum_missing_entry"), target.Name)
	}

	options.setProgress(i18n.Format("upgrade.verifying_release", target.Name))
	archive, err := fetch(ctx, options, target.URL)
	if err != nil {
		return nil, err
	}
	got := sha256.Sum256(archive)
	if !strings.EqualFold(hex.EncodeToString(got[:]), want) {
		// The bytes are discarded rather than kept for inspection: an
		// unverified download must never reach the filesystem.
		return nil, fmt.Errorf(i18n.Text("upgrade.checksum_mismatch"), target.Name, want, hex.EncodeToString(got[:]))
	}
	return archive, nil
}

// release is the subset of GitHub release metadata this package needs.
type release struct {
	Tag    string
	Assets []asset
}

type asset struct {
	Name string
	URL  string
}

func (r *release) asset(name string) (asset, error) {
	for _, a := range r.Assets {
		if a.Name == name {
			return a, nil
		}
	}
	return asset{}, fmt.Errorf(i18n.Text("upgrade.release_asset_missing"), r.Tag, name)
}

// platformArchive finds the archive for one platform. The release template
// names archives skillmod_<version>_<os>_<arch>.<format>.
func (r *release) platformArchive(goos, goarch, format string) (asset, error) {
	want := fmt.Sprintf("skillmod_%s_%s_%s.%s", strings.TrimPrefix(r.Tag, "v"), goos, goarch, format)
	for _, a := range r.Assets {
		if a.Name == want {
			return a, nil
		}
	}
	names := make([]string, 0, len(r.Assets))
	for _, a := range r.Assets {
		names = append(names, a.Name)
	}
	return asset{}, fmt.Errorf(i18n.Text("upgrade.platform_archive_missing"), r.Tag, want, strings.Join(names, ", "))
}

// parseChecksums reads the "<sha256>  <name>" lines published with a release.
// The digest is validated by length, so a comment, a blank line, or a stray
// summary line is ignored instead of being mistaken for an entry.
func parseChecksums(body string) map[string]string {
	sums := map[string]string{}
	for line := range strings.SplitSeq(body, "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 || len(fields[0]) != 64 {
			continue
		}
		// GNU tools mark binary mode with a leading "*".
		sums[path.Base(strings.TrimPrefix(fields[1], "*"))] = fields[0]
	}
	return sums
}

// statusError reports a non-success HTTP status so callers can distinguish a
// missing release from a transport failure.
type statusError struct {
	URL  string
	Code int
}

func (e *statusError) Error() string {
	message := fmt.Sprintf(i18n.Text("upgrade.response_status"), e.URL, e.Code)
	if e.Code == http.StatusForbidden || e.Code == http.StatusTooManyRequests {
		// An exhausted unauthenticated quota is the usual reason GitHub declines a
		// request that is not simply missing, so name it rather than leaving the
		// status code to be looked up.
		return message + "; " + i18n.Text("upgrade.response_rate_limited")
	}
	return message
}

// fetch performs one metadata or artifact request.
func fetch(ctx context.Context, options Options, url string) ([]byte, error) {
	if url == "" {
		return nil, errors.New(i18n.Text("upgrade.release_asset_url_missing"))
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Accept", "application/vnd.github+json")
	request.Header.Set("User-Agent", "skillmod/"+strings.TrimPrefix(options.Current, "v"))
	response, err := options.client().Do(request)
	if err != nil {
		return nil, fmt.Errorf(i18n.Text("upgrade.request_failed"), url, err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return nil, &statusError{URL: url, Code: response.StatusCode}
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maxArchiveBytes+1))
	if err != nil {
		return nil, fmt.Errorf(i18n.Text("upgrade.request_failed"), url, err)
	}
	if len(body) > maxArchiveBytes {
		return nil, fmt.Errorf(i18n.Text("upgrade.download_too_large"), url, maxArchiveBytes)
	}
	return body, nil
}

func (options *Options) applyDefaults() {
	if options.Repository == "" {
		options.Repository = Repository
	}
	if options.APIBase == "" {
		options.APIBase = DefaultAPIBase
	}
	if options.OS == "" {
		options.OS = runtime.GOOS
	}
	if options.Arch == "" {
		options.Arch = runtime.GOARCH
	}
	if options.Format == "" {
		options.Format = FormatFor(options.OS)
	}
}

// defaultClient is shared so the requests of one run reuse connections instead
// of building a transport per call.
var defaultClient = &http.Client{Timeout: 5 * time.Minute}

func (options *Options) client() *http.Client {
	if options.HTTP != nil {
		return options.HTTP
	}
	return defaultClient
}

func (options *Options) setProgress(messages ...string) {
	if options.Progress != nil {
		options.Progress.Set(messages...)
	}
}

// FormatFor returns the release archive format for an operating system.
func FormatFor(goos string) string {
	if goos == "windows" {
		return "zip"
	}
	return "tar.gz"
}

// CompareVersions returns a positive number when latest is newer than current,
// zero when both describe the same release, and a negative number when the
// running version is already newer. Development builds compare by their release
// base, so a build made after v0.0.2 (git describe reports
// v0.0.2-5-gabc1234) is not "upgraded" to v0.0.2 itself. A version that is not a
// release version at all, such as "dev", is treated as older than any release.
func CompareVersions(current, latest string) int {
	current, latest = releaseBase(current), releaseBase(latest)
	if !semver.IsValid(current) {
		if !semver.IsValid(latest) {
			return 0
		}
		return 1
	}
	return semver.Compare(latest, current)
}

// releaseBase drops pre-release and build suffixes from a semantic version.
func releaseBase(v string) string {
	if i := strings.IndexByte(v, '+'); i >= 0 {
		v = v[:i]
	}
	if i := strings.IndexByte(v, '-'); i >= 0 {
		v = v[:i]
	}
	return v
}
