// Copyright 2026 huija
//
// SPDX-License-Identifier: MIT

// Package msgkey defines the shape of skillmod's translation message keys.
//
// A key is a short identifier such as "cli.get.long" that names a message
// independently of its wording. The wording lives in the locale catalogs under
// locales/; keys never appear in user-facing output unless a catalog is
// incomplete. See locales/README.md for the naming rule.
package msgkey

import (
	"fmt"
	"regexp"
	"strings"
)

const (
	// MaxLen bounds the whole key, keeping catalog lines short.
	MaxLen = 56
	// MaxSegmentLen bounds a single dot-separated segment.
	MaxSegmentLen = 32
)

// shape requires two or three lowercase snake_case segments joined by dots:
// a package scope, an optional file scope, and a subject.
var shape = regexp.MustCompile(`^[a-z][a-z0-9_]*(?:\.[a-z0-9_]+){1,2}$`)

// Valid reports whether key follows the naming rule.
func Valid(key string) bool {
	return len(key) <= MaxLen && shape.MatchString(key)
}

// Validate returns an error describing the first rule key breaks.
func Validate(key string) error {
	if len(key) > MaxLen {
		return fmt.Errorf("i18n key %q is %d characters; keys must be at most %d", key, len(key), MaxLen)
	}
	if !shape.MatchString(key) {
		return fmt.Errorf("i18n key %q must match %s (for example %q)", key, shape, "cli.get.long")
	}
	for _, segment := range strings.Split(key, ".") {
		if len(segment) > MaxSegmentLen {
			return fmt.Errorf("i18n key %q has a %d-character segment; segments must be at most %d", key, len(segment), MaxSegmentLen)
		}
	}
	return nil
}
