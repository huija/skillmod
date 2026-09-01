// Copyright 2026 huija
//
// SPDX-License-Identifier: MIT

// Package locales exposes the gettext catalogs embedded in the skillmod binary.
// The PO files remain ordinary assets so the website and documentation tooling
// can consume the same translations without importing Go code.
package locales

import "embed"

// FS contains the runtime translation catalogs.
//
//go:embed *.po
var FS embed.FS
