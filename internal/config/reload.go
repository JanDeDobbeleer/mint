package config

import (
	"context"
	"crypto/sha256"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"
)

// Loader holds the current configuration and reloads it behind a validation gate: a file that fails to
// decode or validate never replaces the last good configuration, and the failure is reported to the operator.
type Loader struct {
	current  atomic.Pointer[Config]
	path     string
	opts     Options
	mu       sync.Mutex
	lastGood [sha256.Size]byte
	lastBad  [sha256.Size]byte
}

// NewLoader performs the first load. Unlike a reload, a bad first load is an error: there is no good config to keep.
func NewLoader(path string, opts Options) (*Loader, error) {
	l := &Loader{path: path, opts: opts}
	data, err := readLimited(path)
	if err != nil {
		return nil, err
	}
	cfg, err := Parse(data, filepath.Base(path), opts)
	if err != nil {
		return nil, err
	}
	l.current.Store(cfg)
	l.lastGood = sha256.Sum256(data)
	return l, nil
}

// Current returns the last good configuration. It is never nil and must be treated as read-only.
func (l *Loader) Current() *Config { return l.current.Load() }

// Reload re-reads the file. It reports whether the configuration changed. On any error the previous
// configuration stays in force and the error says why the new file was rejected.
func (l *Loader) Reload() (changed bool, err error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	data, err := readLimited(l.path)
	if err != nil {
		return false, err
	}
	sum := sha256.Sum256(data)
	if sum == l.lastGood {
		return false, nil
	}
	cfg, err := Parse(data, filepath.Base(l.path), l.opts)
	if err != nil {
		l.lastBad = sum
		return false, err
	}
	l.current.Store(cfg)
	l.lastGood = sum
	l.lastBad = [sha256.Size]byte{}
	return true, nil
}

// Watch polls the file every interval until ctx ends. onChange receives each new good configuration and
// onError each distinct rejected file once (an unchanged bad file is not reported again).
func (l *Loader) Watch(ctx context.Context, interval time.Duration, onChange func(*Config), onError func(error)) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	var reported [sha256.Size]byte
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		changed, err := l.Reload()
		switch {
		case err != nil:
			l.mu.Lock()
			bad := l.lastBad
			l.mu.Unlock()
			// A file we could not read at all has no hash: report those every time.
			if bad == [sha256.Size]byte{} || bad != reported {
				reported = bad
				if onError != nil {
					onError(err)
				}
			}
		case changed:
			reported = [sha256.Size]byte{}
			if onChange != nil {
				onChange(l.Current())
			}
		}
	}
}
