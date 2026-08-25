// Copyright 2026 huija
//
// SPDX-License-Identifier: MIT

package source

import (
	"net"
	"net/url"
	"path"
	"regexp"
	"strings"
)

var scpRemotePattern = regexp.MustCompile(`^(?:[^/@:]+@)?([^/:]+):(.+)$`)

// RepoIdentity returns a credential-free identity used only for local cache
// keys. The original repo URL is still used for Git transport and credentials.
// Common HTTPS and default-port SSH spellings intentionally share an identity.
func RepoIdentity(repo string) string {
	repo = strings.TrimSpace(repo)
	if !strings.Contains(repo, "://") {
		if m := scpRemotePattern.FindStringSubmatch(repo); m != nil {
			return webRepoIdentity("https", strings.ToLower(m[1]), m[2])
		}
	}

	u, err := url.Parse(repo)
	if err != nil || u.Scheme == "" {
		if strings.Contains(repo, "/") && !strings.HasPrefix(repo, "/") && !strings.HasPrefix(repo, ".") {
			return RepoIdentity("https://" + repo)
		}
		return strings.TrimSuffix(strings.TrimSuffix(repo, "/"), ".git")
	}

	scheme := strings.ToLower(u.Scheme)
	if scheme == "file" {
		// A .git suffix can be part of an actual local directory name.
		return "file://" + strings.ToLower(u.Hostname()) + path.Clean("/"+strings.TrimPrefix(u.Path, "/"))
	}
	if u.Host == "" {
		return strings.TrimSuffix(strings.TrimSuffix(repo, "/"), ".git")
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
	return webRepoIdentity(scheme, host, u.Path)
}

func webRepoIdentity(scheme, host, repoPath string) string {
	repoPath = strings.Trim(path.Clean("/"+strings.TrimPrefix(repoPath, "/")), "/")
	repoPath = strings.TrimSuffix(repoPath, ".git")
	id := scheme + "://" + host
	if repoPath != "" && repoPath != "." {
		id += "/" + repoPath
	}
	return id
}
