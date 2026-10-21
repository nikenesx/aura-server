// Package audio stores recordings. Disk is the only backend for now; S3 will
// implement the same methods.
package audio

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var (
	ErrNotFound = errors.New("audio not found")
	ErrTooLarge = errors.New("audio is too large")
)

// File is an open recording. Reading, seeking and Stat are what
// http.ServeContent needs to answer Range requests.
type File interface {
	io.ReadSeekCloser
	Stat() (fs.FileInfo, error)
}

var safeKey = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

type Disk struct {
	dir string
}

func NewDisk(dir string) (*Disk, error) {
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, fmt.Errorf("create audio dir: %w", err)
	}
	return &Disk{dir: dir}, nil
}

// Put streams r to the store under key, up to limit bytes. It writes to a
// temporary file and renames it, so a cut-off upload leaves nothing behind.
// The error wraps ErrTooLarge when r is longer than limit.
func (d *Disk) Put(key string, r io.Reader, limit int64) (int64, error) {
	path, err := d.path(key)
	if err != nil {
		return 0, err
	}
	tmp, err := os.CreateTemp(d.dir, ".upload-*")
	if err != nil {
		return 0, err
	}
	defer os.Remove(tmp.Name())

	n, err := io.Copy(tmp, io.LimitReader(r, limit+1))
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return n, err
	}
	if n > limit {
		return n, ErrTooLarge
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return n, err
	}
	return n, nil
}

// Open returns the recording for reading and seeking (Range requests need both).
func (d *Disk) Open(key string) (File, error) {
	path, err := d.path(key)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return f, nil
}

func (d *Disk) Delete(key string) error {
	path, err := d.path(key)
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func (d *Disk) path(key string) (string, error) {
	if !safeKey.MatchString(key) || strings.HasPrefix(key, ".") {
		return "", fmt.Errorf("bad audio key %q", key)
	}
	return filepath.Join(d.dir, key), nil
}
