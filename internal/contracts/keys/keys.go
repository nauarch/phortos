// Copyright 2026 darki73
// SPDX-License-Identifier: Apache-2.0

// Package keys is the contract of where phortos keeps its private keys: a folder on the phortos
// host only, readable by phortos alone. A file adapter implements it.
package keys

// Store keeps named PEM files. Missing ones are errkind.ErrNotFound.
type Store interface {
	// Load reads a file.
	Load(name string) ([]byte, error)
	// Save writes a file whole, readable by its owner only.
	Save(name string, data []byte) error
}
