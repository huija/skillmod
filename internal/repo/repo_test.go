// Copyright 2026 huija
//
// SPDX-License-Identifier: MIT

package repo

import (
	"strings"
	"testing"
)

func TestIdentityNormalizesTransportAndGitSuffix(t *testing.T) {
	want := "https://github.com/acme/skills"
	for _, remote := range []string{
		"https://github.com/acme/skills",
		"https://user:secret@GITHUB.com:443/acme/skills.git/?token=secret",
		"git@github.com:acme/skills.git",
		"ssh://git@github.com:22/acme/skills.git",
		"github.com/acme/skills.git",
	} {
		if got := Identity(remote); got != want {
			t.Errorf("Identity(%q) = %q, want %q", remote, got, want)
		}
	}
	if Identity("ssh://git@github.com:2222/acme/skills.git") == want {
		t.Error("a non-default SSH port must not share the standard HTTPS identity")
	}
}

func TestPersistedTransportRejectsSecretsAndPreservesSSHUser(t *testing.T) {
	for _, remote := range []string{
		"github.com/acme/skills",
		"https://user@github.com/acme/skills",
		"https://user:secret@github.com/acme/skills",
		"ssh://git:secret@github.com/acme/skills",
		"https://github.com/acme/skills?access_token=secret",
		"https://github.com/acme/skills#secret",
		"git@github.com:acme/skills?access_token=secret",
		"user:secret@github.com:acme/skills",
	} {
		if got, err := PersistedTransport(remote); err == nil {
			t.Errorf("PersistedTransport(%q) = %q, want error", remote, got)
		}
	}
	for _, remote := range []string{
		"git@github.com:acme/skills.git",
		"ssh://git@github.com/acme/skills.git",
		"https://github.com/acme/skills.git",
	} {
		if got, err := PersistedTransport(remote); err != nil || got != remote {
			t.Errorf("PersistedTransport(%q) = %q, %v, want unchanged, nil", remote, got, err)
		}
	}
}

func TestDiagnosticsRedactSecrets(t *testing.T) {
	remote := "https://user:secret@example.com/acme/skills?access_token=token#fragment"
	stderr := "fatal: unable to access '" + remote + "': token secret"
	got := Redact(remote) + " " + RedactText(stderr, remote)
	for _, secret := range []string{"user", "secret", "token", "fragment"} {
		if strings.Contains(got, secret) {
			t.Errorf("redacted diagnostic = %q, want %q redacted", got, secret)
		}
	}
	if !strings.Contains(got, "https://example.com/acme/skills") {
		t.Errorf("redacted diagnostic = %q, want credential-free repository", got)
	}
	malformed := "https://user:secret@exa%mple.com/repo"
	if got := RedactText("fatal: "+malformed, malformed); strings.Contains(got, "user") || strings.Contains(got, "secret") {
		t.Errorf("RedactText(malformed URL) = %q, want credentials redacted", got)
	}
}

func TestSupportedTransport(t *testing.T) {
	for _, scheme := range []string{"file", "http", "https", "ssh", "HTTPS", "SSH"} {
		if !SupportedTransport(scheme) {
			t.Errorf("SupportedTransport(%q) = false, want true", scheme)
		}
	}
	// git:// is unauthenticated and unencrypted, and no supported workflow
	// produces it, so an address that uses it is refused rather than fetched.
	for _, scheme := range []string{"git", "GIT", "ftp", "svn+ssh", ""} {
		if SupportedTransport(scheme) {
			t.Errorf("SupportedTransport(%q) = true, want false", scheme)
		}
	}
}

func TestPersistedTransportRejectsUnsupportedScheme(t *testing.T) {
	remote := "git://github.com/acme/skills"
	if got, err := PersistedTransport(remote); err == nil {
		t.Errorf("PersistedTransport(%q) = %q, want error", remote, got)
	}
	if got := Redact(remote); got != redactedRepository {
		t.Errorf("Redact(%q) = %q, want %q", remote, got, redactedRepository)
	}
	// The scp-like form carries no scheme and stays supported.
	if got, err := PersistedTransport("git@github.com:acme/skills"); err != nil || got != "git@github.com:acme/skills" {
		t.Errorf("PersistedTransport(scp-like) = %q, %v, want unchanged, nil", got, err)
	}
}
