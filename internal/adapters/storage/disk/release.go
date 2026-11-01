// Copyright 2026 darki73
// SPDX-License-Identifier: Apache-2.0

package disk

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/nauarch/phortos/internal/contracts/storage"
	"github.com/nauarch/phortos/internal/kernel/errkind"
)

// Release reads a release URL's record.
func (s *Dir) Release(url string) (storage.ReleaseRecord, error) {
	r, err := readRelease(s.releasePath(url))
	if errors.Is(err, fs.ErrNotExist) {
		return storage.ReleaseRecord{}, fmt.Errorf("release %s: %w", url, errkind.ErrNotFound)
	}
	if err != nil {
		return storage.ReleaseRecord{}, err
	}
	if r.URL != url {
		return storage.ReleaseRecord{}, fmt.Errorf("store: release record for %s holds %s", url, r.URL)
	}
	return r, nil
}

// PutRelease writes a release URL's record.
func (s *Dir) PutRelease(r storage.ReleaseRecord) error {
	data, err := json.Marshal(r)
	if err != nil {
		return fmt.Errorf("store: %w", err)
	}
	return s.writeFile(s.releasePath(r.URL), data)
}

// Releases lists every release record; one that cannot be read is skipped, as it is fetched
// again when asked for.
func (s *Dir) Releases() ([]storage.ReleaseRecord, error) {
	var out []storage.ReleaseRecord
	root := filepath.Join(s.root, "releases")
	err := filepath.WalkDir(root, func(path string, e fs.DirEntry, err error) error {
		if errors.Is(err, fs.ErrNotExist) && path == root {
			return filepath.SkipDir
		}
		if err != nil || e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			return err
		}
		if r, rerr := readRelease(path); rerr == nil {
			out = append(out, r)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("store: %w", err)
	}
	return out, nil
}

// readRelease reads one record file.
func readRelease(path string) (storage.ReleaseRecord, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return storage.ReleaseRecord{}, err
	}
	var r storage.ReleaseRecord
	if err := json.Unmarshal(data, &r); err != nil {
		return storage.ReleaseRecord{}, fmt.Errorf("store: %s: %w", path, err)
	}
	return r, nil
}
