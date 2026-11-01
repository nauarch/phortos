// Copyright 2026 darki73
// SPDX-License-Identifier: Apache-2.0

// Package config is the contract of phortos' config file. Its keys are phortos' flags without the
// dashes, so a file reads as the command-line arguments it stands for, and the flags' own parser
// checks it. A YAML adapter implements it.
package config

// File is a config file.
type File interface {
	// Args are the file's settings as arguments ("--max-size=100G", one "--intercept=h" per item
	// of a list), in the file's order. A file that is not there is errkind.ErrNotFound, one that
	// cannot be read as settings errkind.ErrInvalid.
	Args() ([]string, error)
}

// Renderer writes settings given as arguments ("--name=value", a name repeated for a list) as a
// config file, under a comment.
type Renderer interface {
	Render(args []string, comment string) ([]byte, error)
}
