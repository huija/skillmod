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
