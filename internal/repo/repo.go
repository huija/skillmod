// Copyright 2026 huija
//
// SPDX-License-Identifier: MIT

// Package repo provides credential-safe repository identities, persistence,
// and diagnostic formatting for repository transports.
package repo

import (
	"fmt"
	"net"
	"net/url"
	"path"
	"regexp"
	"strings"

	"github.com/huija/skillmod/internal/i18n"
)

var scpRemotePattern = regexp.MustCompile(`^[^/@:]+@([^/:]+):(.+)$`)

const redactedRepository = "<redacted-repository>"

// PersistedTransport validates a repository transport before it is written to
// a manifest, cache metadata, or Git configuration. SSH usernames are
// transport details and remain intact; embedded secrets and URL suffixes that
// commonly carry tokens are rejected instead of being persisted accidentally.
func PersistedTransport(remote string) (string, error) {
	if remote == "" || strings.ContainsAny(remote, "\r\n\x00?#") {
		return "", unsafeRepositoryError()
	}
	if !strings.Contains(remote, "://") {
		if scpRemotePattern.MatchString(remote) {
			return remote, nil
		}
		return "", unsafeRepositoryError()
	}

	u, err := url.Parse(remote)
	if err != nil || u.Scheme == "" {
		return "", unsafeRepositoryError()
	}
	scheme := strings.ToLower(u.Scheme)
	switch scheme {
	case "file", "git", "http", "https", "ssh":
	default:
		return "", unsafeRepositoryError()
	}
	if scheme != "file" && u.Host == "" {
		return "", unsafeRepositoryError()
	}
	if u.User != nil {
		_, hasPassword := u.User.Password()
		if scheme != "ssh" || hasPassword || u.User.Username() == "" {
			return "", unsafeRepositoryError()
		}
	}
	if u.RawQuery != "" || u.Fragment != "" {
		return "", unsafeRepositoryError()
	}
	return remote, nil
}

func unsafeRepositoryError() error {
	return fmt.Errorf("%s", i18n.Text("repo.repository_address_unsafe"))
}

// Redact removes user information, query parameters, and fragments from a
// repository URL before it is included in diagnostics. A safe SSH username
// remains visible because it is needed to distinguish transports.
func Redact(remote string) string {
	if !strings.Contains(remote, "://") && scpRemotePattern.MatchString(remote) {
		return remote
	}
	u, err := url.Parse(remote)
	if err != nil || u.Scheme == "" {
		return redactedRepository
	}
	scheme := strings.ToLower(u.Scheme)
	switch scheme {
	case "file", "git", "http", "https", "ssh":
	default:
		return redactedRepository
	}
	if scheme != "file" && u.Host == "" {
		return redactedRepository
	}
	if scheme == "ssh" && u.User != nil {
		u.User = url.User(u.User.Username())
	} else {
		u.User = nil
	}
	u.RawQuery = ""
	u.ForceQuery = false
	u.Fragment = ""
	return u.String()
}

// RedactText removes the exact repository address and its secret components
// from subprocess diagnostics.
func RedactText(text, remote string) string {
	redacted := Redact(remote)
	if remote != "" && remote != redacted {
		text = strings.ReplaceAll(text, remote, redacted)
	}
	u, err := url.Parse(remote)
	if err != nil {
		return text
	}
	if u.User != nil {
		if password, ok := u.User.Password(); ok && password != "" {
			text = strings.ReplaceAll(text, password, "***")
		}
	}
	for _, values := range u.Query() {
		for _, value := range values {
			if value != "" {
				text = strings.ReplaceAll(text, value, "***")
			}
		}
	}
	return text
}

// Identity returns a credential-free identity used only for local cache keys.
// Credential-free transport metadata is retained separately for Git. Common
// HTTPS and default-port SSH spellings intentionally share an identity.
func Identity(remote string) string {
	remote = strings.TrimSpace(remote)
	if !strings.Contains(remote, "://") {
		if m := scpRemotePattern.FindStringSubmatch(remote); m != nil {
			return webIdentity("https", strings.ToLower(m[1]), m[2])
		}
	}

	u, err := url.Parse(remote)
	if err != nil || u.Scheme == "" {
		if strings.Contains(remote, "/") && !strings.HasPrefix(remote, "/") && !strings.HasPrefix(remote, ".") {
			return Identity("https://" + remote)
		}
		return strings.TrimSuffix(strings.TrimSuffix(remote, "/"), ".git")
	}

	scheme := strings.ToLower(u.Scheme)
	if scheme == "file" {
		// A .git suffix can be part of an actual local directory name.
		return "file://" + strings.ToLower(u.Hostname()) + path.Clean("/"+strings.TrimPrefix(u.Path, "/"))
	}
	if u.Host == "" {
		return strings.TrimSuffix(strings.TrimSuffix(remote, "/"), ".git")
	}

	host := strings.ToLower(u.Hostname())
	port := u.Port()
	switch scheme {
	case "ssh":
		if port == "" || port == "22" {
			scheme = "https"
			port = ""
		}
	case "https":
		if port == "443" {
			port = ""
		}
	case "http":
		if port == "80" {
			port = ""
		}
	}
	if port != "" {
		host = net.JoinHostPort(host, port)
	}
	return webIdentity(scheme, host, u.Path)
}

func webIdentity(scheme, host, repoPath string) string {
	repoPath = strings.Trim(path.Clean("/"+strings.TrimPrefix(repoPath, "/")), "/")
	repoPath = strings.TrimSuffix(repoPath, ".git")
	id := scheme + "://" + host
	if repoPath != "" && repoPath != "." {
		id += "/" + repoPath
	}
	return id
}
