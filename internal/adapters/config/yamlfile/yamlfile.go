// Copyright 2026 darki73
// SPDX-License-Identifier: Apache-2.0

// Package yamlfile reads and writes phortos' config file as YAML: a mapping of flag names to a
// value or a list of values, such as
//
//	max-size: 100G
//	intercept:
//	  - download.docker.com
//
// It is the only package that uses the YAML module.
package yamlfile

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"

	"go.yaml.in/yaml/v3"

	"github.com/nauarch/phortos/internal/contracts/config"
	"github.com/nauarch/phortos/internal/kernel/errkind"
)

// File is the config file at Path.
type File struct {
	Path string
}

// Codec renders config files.
type Codec struct{}

var (
	_ config.File     = File{}
	_ config.Renderer = Codec{}
)

// Args reads the file's settings as arguments.
func (f File) Args() ([]string, error) {
	b, err := os.ReadFile(f.Path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("config file %s: %w", f.Path, errkind.ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("config file %s: %w", f.Path, err)
	}
	args, err := Parse(b)
	if err != nil {
		return nil, fmt.Errorf("config file %s: %w", f.Path, err)
	}
	return args, nil
}

// Parse reads settings as arguments: every key of the top mapping is a flag, every scalar its
// value (as written, so "100G" and "10s" stay as they are), every list one value per item. An
// empty value or list sets nothing; an empty file has no settings.
func Parse(b []byte) ([]string, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(b, &doc); err != nil {
		return nil, fmt.Errorf("%w: %w", err, errkind.ErrInvalid)
	}
	if len(doc.Content) == 0 {
		return nil, nil
	}
	top := doc.Content[0]
	if top.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("line %d: want a mapping of settings (name: value): %w", top.Line, errkind.ErrInvalid)
	}
	var args []string
	seen := map[string]bool{}
	for i := 0; i+1 < len(top.Content); i += 2 {
		k, v := top.Content[i], top.Content[i+1]
		name := k.Value
		if k.Kind != yaml.ScalarNode || name == "" || strings.HasPrefix(name, "-") || name == "config" {
			return nil, fmt.Errorf("line %d: %q is not a setting: %w", k.Line, name, errkind.ErrInvalid)
		}
		if seen[name] {
			return nil, fmt.Errorf("line %d: %s is set twice: %w", k.Line, name, errkind.ErrInvalid)
		}
		seen[name] = true
		switch v.Kind {
		case yaml.ScalarNode:
			if v.Tag != "!!null" {
				args = append(args, "--"+name+"="+v.Value)
			}
		case yaml.SequenceNode:
			for _, item := range v.Content {
				if item.Kind != yaml.ScalarNode {
					return nil, fmt.Errorf("line %d: %s: want a list of plain values: %w", item.Line, name, errkind.ErrInvalid)
				}
				args = append(args, "--"+name+"="+item.Value)
			}
		default:
			return nil, fmt.Errorf("line %d: %s: want a value or a list of values: %w", v.Line, name, errkind.ErrInvalid)
		}
	}
	return args, nil
}

// Render writes arguments as a config file under a comment: each name once, in the order first
// given, as a list when it was given more than once. A flag given bare ("--debug") is true.
func (Codec) Render(args []string, comment string) ([]byte, error) {
	var names []string
	values := map[string][]string{}
	for _, a := range args {
		name, value, ok := strings.Cut(strings.TrimPrefix(a, "--"), "=")
		if !strings.HasPrefix(a, "--") || name == "" {
			return nil, fmt.Errorf("argument %q is not --name=value: %w", a, errkind.ErrInvalid)
		}
		if !ok {
			value = "true"
		}
		if _, seen := values[name]; !seen {
			names = append(names, name)
		}
		values[name] = append(values[name], value)
	}
	top := &yaml.Node{Kind: yaml.MappingNode}
	for _, name := range names {
		key := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: name}
		vs := values[name]
		var val *yaml.Node
		if len(vs) == 1 {
			val = scalar(vs[0])
		} else {
			val = &yaml.Node{Kind: yaml.SequenceNode}
			for _, v := range vs {
				val.Content = append(val.Content, scalar(v))
			}
		}
		top.Content = append(top.Content, key, val)
	}
	doc := &yaml.Node{Kind: yaml.DocumentNode, HeadComment: comment, Content: []*yaml.Node{top}}
	if len(names) == 0 {
		top.Style = yaml.FlowStyle
	}
	var b strings.Builder
	enc := yaml.NewEncoder(&b)
	enc.SetIndent(2)
	if err := enc.Encode(doc); err != nil {
		return nil, err
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	return []byte(b.String()), nil
}

// scalar is a value as a string node: plain where YAML reads it back as written, quoted where it
// would read as something else.
func scalar(v string) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: v}
}
