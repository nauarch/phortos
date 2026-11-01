// Copyright 2026 darki73
// SPDX-License-Identifier: Apache-2.0

package disk

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/nauarch/phortos/internal/kernel/clock"
	"github.com/nauarch/phortos/internal/kernel/errkind"
)

// lockName is the file that says which phortos holds the store.
const lockName = ".lock"

// Lock timing: the holder rewrites the lock every beat; a lock not rewritten for stale is left by
// a phortos that died and may be taken over.
const (
	defaultBeat  = 30 * time.Second
	defaultStale = 2 * time.Minute
)

// LockOptions say who takes the lock (shown to whoever is refused) and from which host and
// process, on which clock, how often it beats and when a silent lock counts as stale (zero takes
// the defaults), and how to tell whether a process on this host still runs (nil asks the system).
type LockOptions struct {
	Owner string
	Host  string
	PID   int
	Clock clock.Clock
	Beat  time.Duration
	Stale time.Duration
	Alive func(pid int) bool
}

// lockFile is the lock's content.
type lockFile struct {
	Owner string    `json:"owner"`
	Host  string    `json:"host,omitempty"`
	PID   int       `json:"pid,omitempty"`
	Beat  time.Time `json:"beat"`
}

// Lock makes this phortos the only one using the store. It is a file rewritten on a heartbeat rather
// than an OS lock, which NFS and SMB do not keep reliably: a live lock refuses with ErrConflict, a
// stale one (its holder died) is taken over, at once when its holder was a process on this host
// that is gone (or had this very PID, as a restarted container does). release stops the heartbeat
// and removes the lock.
func (s *Dir) Lock(o LockOptions) (release func(), err error) {
	if o.Clock == nil {
		o.Clock = clock.System{}
	}
	if o.Beat <= 0 {
		o.Beat = defaultBeat
	}
	if o.Stale <= 0 {
		o.Stale = defaultStale
	}
	if o.Alive == nil {
		o.Alive = processAlive
	}
	path := filepath.Join(s.root, lockName)
	if held, ok := s.readLock(path); ok && o.Clock.Now().Sub(held.Beat) < o.Stale && !left(held, o) {
		return nil, fmt.Errorf("store %s is used by %s (last seen %s); stop it, or remove %s if it is gone: %w",
			s.root, held.Owner, held.Beat.Format(time.RFC3339), path, errkind.ErrConflict)
	}
	beat := func() error {
		data, err := json.Marshal(lockFile{Owner: o.Owner, Host: o.Host, PID: o.PID, Beat: o.Clock.Now().UTC()})
		if err != nil {
			return err
		}
		return s.writeFile(path, data)
	}
	if err := beat(); err != nil {
		return nil, err
	}
	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Go(func() {
		t := time.NewTicker(o.Beat)
		defer t.Stop()
		for {
			select {
			case <-stop:
				return
			case <-t.C:
				_ = beat()
			}
		}
	})
	var once sync.Once
	return func() {
		once.Do(func() {
			close(stop)
			wg.Wait()
			_ = os.Remove(path)
		})
	}, nil
}

// readLock reads the lock; false when there is none or it cannot be read (a torn write counts as
// no lock).
func (s *Dir) readLock(path string) (lockFile, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return lockFile{}, false
	}
	var l lockFile
	if json.Unmarshal(data, &l) != nil {
		return lockFile{}, false
	}
	return l, true
}

// left reports whether a lock was left by a process of this host that no longer holds it: one that
// is gone, or one with this very PID (a restarted container, where phortos is always the same PID).
func left(held lockFile, o LockOptions) bool {
	if held.Host == "" || held.Host != o.Host || held.PID == 0 {
		return false
	}
	return held.PID == o.PID || !o.Alive(held.PID)
}
