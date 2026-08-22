package file

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// backupTimeFormat must be a fixed-width layout.
const backupTimeFormat = "2006-01-02T15-04-05.000"

// rotating implements io.Writer and io.Closer (io.WriteCloser) and supports automatic rotation.
// It represents a file that is rotated once it reaches a configurable size limit.
// A configurable maximum number of backup files are kept, and old backups are deleted once they exceed a configurable age limit.
//
// It is a drop-in replacement for any file that needs to be bounded in size and age, such as log files.
type rotating struct {
	mu sync.Mutex

	filepath   string
	maxSize    int64
	maxBackups int
	maxAge     time.Duration
	file       *os.File
	size       int64
}

// newRotating creates a new io.WriteCloser that writes to the specified file and rotates it automatically.
// The returned io.WriteCloser is safe for concurrent use.
//
// filepath is the path to the file.
// maxSize is the maximum size in bytes the file can grow to before it is rotated.
// maxBackups is the maximum number of rotated backup files to keep.
// maxAge is the maximum age a backup file can reach before it is deleted.
func NewRotating(filepath string, maxSize int64, maxBackups int, maxAge time.Duration) (io.WriteCloser, error) {
	f := &rotating{
		filepath:   filepath,
		maxSize:    maxSize,
		maxBackups: maxBackups,
		maxAge:     maxAge,
	}

	if err := f.openExistingOrNew(); err != nil {
		return nil, err
	}

	return f, nil
}

// Write implements the io.Writer interface.
// It writes the provided bytes to the file, rotating it if necessary.
func (f *rotating) Write(p []byte) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.file == nil {
		return 0, fmt.Errorf("file is closed")
	}

	if f.maxSize > 0 && f.size+int64(len(p)) > f.maxSize {
		if err := f.rotate(); err != nil {
			return 0, err
		}
	}

	n, err := f.file.Write(p)
	f.size += int64(n)

	return n, err
}

// Close implements the io.Closer interface.
// It closes the file, releasing any resources associated with it.
func (f *rotating) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.file == nil {
		return nil
	}

	err := f.file.Close()
	f.file, f.size = nil, 0

	return err
}

// openExistingOrNew opens the file if it exists, or creates it if it does not exist.
func (f *rotating) openExistingOrNew() error {
	info, err := os.Stat(f.filepath)
	switch {
	case err == nil:
		file, err := os.OpenFile(f.filepath, os.O_APPEND|os.O_WRONLY, 0644)
		if err != nil {
			return fmt.Errorf("error opening %s: %w", f.filepath, err)
		}

		f.file, f.size = file, info.Size()
		return nil

	case os.IsNotExist(err):
		return f.openNew()

	default:
		return fmt.Errorf("error checking %s: %w", f.filepath, err)
	}
}

// openNew creates a new file, truncating it if it already exists.
func (f *rotating) openNew() error {
	if dir := filepath.Dir(f.filepath); dir != "." {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return fmt.Errorf("error creating directory %s: %w", dir, err)
		}
	}

	file, err := os.OpenFile(f.filepath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		return fmt.Errorf("error creating %s: %w", f.filepath, err)
	}

	f.file, f.size = file, 0

	return nil
}

// rotate closes the active file, renames it to a timestamped backup name,
// opens a new active file, and then prunes old backup files.
//
// Callers must hold the lock before calling this method.
func (f *rotating) rotate() error {
	// Close the current file.
	if f.file != nil {
		if err := f.file.Close(); err != nil {
			return fmt.Errorf("error closing %s: %w", f.filepath, err)
		}
		f.file = nil
	}

	// If the file does not exist, there is nothing to rename.
	if _, err := os.Stat(f.filepath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("error checking %s: %w", f.filepath, err)
	} else if err == nil {
		backupFilepath, err := f.getBackupFilepath()
		if err != nil {
			return err
		}

		if err := os.Rename(f.filepath, backupFilepath); err != nil {
			return fmt.Errorf("error renaming %s to %s: %w", f.filepath, backupFilepath, err)
		}
	}

	if err := f.openNew(); err != nil {
		return err
	}

	// Retention errors must not break the write path.
	// The active file is already open and writable at this point.
	if err := f.cleanup(); err != nil {
		fmt.Fprintf(os.Stderr, "error cleaning up backup files: %s\n", err)
	}

	return nil
}

// getBackupFilepath builds a timestamped backup file name next to the active file.
// In the rare case that two rotations land in the same millisecond, it appends a counter to ensure uniqueness.
func (f *rotating) getBackupFilepath() (string, error) {
	dir := filepath.Dir(f.filepath)
	base := filepath.Base(f.filepath)
	ext := filepath.Ext(base)
	prefix := strings.TrimSuffix(base, ext)
	ts := time.Now().UTC().Format(backupTimeFormat)

	candidate := filepath.Join(dir, fmt.Sprintf("%s-%s%s", prefix, ts, ext))

	// Ensure the candidate backup filepath is unique and does not already exist.
	for i := 1; ; i++ {
		_, err := os.Stat(candidate)
		switch {
		case err == nil:
			candidate = filepath.Join(dir, fmt.Sprintf("%s-%s-%d%s", prefix, ts, i, ext))
		case os.IsNotExist(err):
			return candidate, nil
		default:
			return "", fmt.Errorf("error checking %s: %w", candidate, err)
		}
	}
}

// cleanup enforces MaxBackups and MaxAge against the set of existing backups.
// Both limits are independent and additive: a file is removed if it violates either one.
//
// Callers must hold the lock before calling this method.
func (f *rotating) cleanup() error {
	// Retention is disabled.
	if f.maxBackups <= 0 && f.maxAge <= 0 {
		return nil
	}

	backups, err := f.listBackups()
	if err != nil {
		return err
	}

	var toRemove []backupFile

	if f.maxBackups > 0 {
		if f.maxBackups < len(backups) {
			toRemove = backups[f.maxBackups:]
			backups = backups[:f.maxBackups]
		}
	}

	if f.maxAge > 0 {
		cutoff := time.Now().Add(-f.maxAge)
		for _, b := range backups {
			if b.timestamp.Before(cutoff) {
				toRemove = append(toRemove, b)
			}
		}
	}

	var errs error

	for _, b := range toRemove {
		if err := os.Remove(b.path); err != nil && !os.IsNotExist(err) {
			errs = errors.Join(errs, fmt.Errorf("error removing backup file %s: %w", b.path, err))
		}
	}

	return errs
}

// listBackups lists all backup files for the active file,
// parsing their timestamps and sequence numbers from their filenames.
func (f *rotating) listBackups() ([]backupFile, error) {
	dir := filepath.Dir(f.filepath)
	base := filepath.Base(f.filepath)
	ext := filepath.Ext(base)
	prefix := strings.TrimSuffix(base, ext)

	pattern := fmt.Sprintf(`^%s-(\d{4}-\d{2}-\d{2}T\d{2}-\d{2}-\d{2}\.\d{3})(-\d+)?%s$`, regexp.QuoteMeta(prefix), regexp.QuoteMeta(ext))
	re := regexp.MustCompile(pattern)

	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("error reading directory %s: %w", dir, err)
	}

	var backups []backupFile
	var errs error

	for _, e := range entries {
		if e.IsDir() {
			continue
		}

		// Skip files that do not match the backup filename pattern.
		m := re.FindStringSubmatch(e.Name())
		if len(m) < 2 {
			continue
		}

		// Parse the timestamp from the filename.
		ts, err := time.Parse(backupTimeFormat, m[1])
		if err != nil {
			errs = errors.Join(errs, fmt.Errorf("error parsing timestamp from backup file %s: %w", e.Name(), err))
			continue
		}

		// Parse the sequence number from the filename, if it exists.
		var seq int
		if len(m) >= 3 && m[2] != "" {
			if seq, err = strconv.Atoi(strings.TrimPrefix(m[2], "-")); err != nil {
				errs = errors.Join(errs, fmt.Errorf("error parsing sequence from backup file %s: %w", e.Name(), err))
				continue
			}
		}

		backups = append(backups, backupFile{
			path:      filepath.Join(dir, e.Name()),
			timestamp: ts,
			sequence:  seq,
		})
	}

	if errs != nil {
		return nil, fmt.Errorf("error listing backup files: %w", err)
	}

	// Sort backup files from newest to oldest, first by timestamp and then by sequence number.
	sort.Slice(backups, func(i, j int) bool {
		return backups[i].timestamp.After(backups[j].timestamp) ||
			(backups[i].timestamp.Equal(backups[j].timestamp) && backups[i].sequence > backups[j].sequence)
	})

	return backups, nil
}

type backupFile struct {
	path      string
	timestamp time.Time
	sequence  int
}
