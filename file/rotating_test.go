package file

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestNewRotating(t *testing.T) {
	dir := t.TempDir()

	tests := []struct {
		name       string
		filepath   string
		maxSize    int64
		maxBackups int
		maxAge     time.Duration
	}{
		{
			name:       "OK",
			filepath:   filepath.Join(dir, "app.log"),
			maxSize:    10 * 1024 * 1024,
			maxBackups: 5,
			maxAge:     3 * 24 * time.Hour,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			w, err := NewRotating(tc.filepath, tc.maxSize, tc.maxBackups, tc.maxAge)
			assert.NoError(t, err)
			assert.NotNil(t, w)

			f := w.(*rotating)

			assert.Equal(t, tc.filepath, f.filepath)
			assert.Equal(t, tc.maxSize, f.maxSize)
			assert.Equal(t, tc.maxBackups, f.maxBackups)
			assert.Equal(t, tc.maxAge, f.maxAge)
			assert.NotNil(t, f.file)
			assert.Zero(t, f.size)
		})
	}
}

func TestRotating_Write(t *testing.T) {
	t.Run("FileClosed", func(t *testing.T) {
		f := &rotating{}

		n, err := f.Write([]byte("Hello, World!"))
		assert.EqualError(t, err, "file is closed")
		assert.Zero(t, n)
	})

	tests := []struct {
		name            string
		filename        string
		maxSize         int64
		fileContent     string
		writeContent    string
		expectedSize    int
		expectedBackups int
	}{
		{
			name:            "BelowMaxSzie",
			filename:        "app.log",
			maxSize:         1024, // 1 KB
			fileContent:     loremIpsum[:447],
			writeContent:    "Hello, World!",
			expectedSize:    447,
			expectedBackups: 0,
		},
		{
			name:            "AboveMaxSize",
			filename:        "app.log",
			maxSize:         1024, // 1 KB
			fileContent:     loremIpsum,
			writeContent:    "Ciao Mondo!",
			expectedSize:    1400,
			expectedBackups: 1,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()

			path := filepath.Join(dir, tc.filename)
			err := os.WriteFile(path, []byte(tc.fileContent), 0644)
			assert.NoError(t, err)

			f := &rotating{
				filepath:   path,
				maxSize:    tc.maxSize,
				maxBackups: 0,
				maxAge:     0,
			}

			err = f.openExistingOrNew()
			assert.NoError(t, err)

			n, err := f.Write([]byte(tc.writeContent))
			assert.NoError(t, err)
			assert.Equal(t, len(tc.writeContent), n)

			assert.NotNil(t, f.file)
			assert.NotZero(t, f.size)

			_, err = os.Stat(f.filepath)
			assert.NoError(t, err)

			backups, err := f.listBackups()
			assert.NoError(t, err)
			assert.Len(t, backups, tc.expectedBackups)
		})
	}
}

func TestRotating_Close(t *testing.T) {
	t.Run("FileClosed", func(t *testing.T) {
		f := &rotating{}

		err := f.Close()
		assert.NoError(t, err)

		assert.Nil(t, f.file)
		assert.Zero(t, f.size)
	})

	t.Run("FileOpen", func(t *testing.T) {
		file, err := os.CreateTemp("", "app.log")
		assert.NoError(t, err)

		defer func() {
			_ = os.Remove(file.Name())
		}()

		f := &rotating{
			filepath: file.Name(),
		}

		err = f.Close()
		assert.NoError(t, err)

		assert.Nil(t, f.file)
		assert.Zero(t, f.size)
	})
}

func TestRotating_openExistingOrNew(t *testing.T) {
	tests := []struct {
		name     string
		filename string
	}{
		{
			name:     "OK",
			filename: "app.log",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			file, err := os.CreateTemp("", tc.filename)
			assert.NoError(t, err)

			defer func() {
				_ = os.Remove(file.Name())
			}()

			f := &rotating{
				filepath: file.Name(),
			}

			err = f.openExistingOrNew()
			assert.NoError(t, err)

			assert.NotNil(t, f.file)
			assert.Zero(t, f.size)

			_, err = os.Stat(f.filepath)
			assert.NoError(t, err)
		})
	}
}

func TestRotating_openNew(t *testing.T) {
	tests := []struct {
		name     string
		filename string
	}{
		{
			name:     "OK",
			filename: "app.log",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()

			f := &rotating{
				filepath: filepath.Join(dir, tc.filename),
			}

			err := f.openNew()
			assert.NoError(t, err)

			assert.NotNil(t, f.file)
			assert.Zero(t, f.size)

			_, err = os.Stat(f.filepath)
			assert.NoError(t, err)
		})
	}
}

func TestRotating_rotate(t *testing.T) {
	tests := []struct {
		name            string
		filename        string
		maxBackups      int
		maxAge          time.Duration
		openFile        bool
		expectedBackups int
	}{
		{
			name:            "FileOpen",
			filename:        "app.log",
			maxBackups:      0,
			maxAge:          0,
			openFile:        true,
			expectedBackups: 8,
		},
		{
			name:            "FileClosed",
			filename:        "app.log",
			maxBackups:      3,
			maxAge:          time.Hour,
			openFile:        false,
			expectedBackups: 1,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir, cleanup, err := setupTestDirectory(t, tc.filename)
			assert.NoError(t, err)
			defer cleanup()

			f := &rotating{
				filepath:   filepath.Join(dir, tc.filename),
				maxBackups: tc.maxBackups,
				maxAge:     tc.maxAge,
			}

			if tc.openFile {
				err := f.openExistingOrNew()
				assert.NoError(t, err)
			}

			err = f.rotate()
			assert.NoError(t, err)

			assert.NotNil(t, f.file)
			assert.Zero(t, f.size)

			_, err = os.Stat(f.filepath)
			assert.NoError(t, err)

			backups, err := f.listBackups()
			assert.NoError(t, err)
			assert.Len(t, backups, tc.expectedBackups)
		})
	}
}

func TestRotating_getBackupFilepath(t *testing.T) {
	tests := []struct {
		name     string
		filename string
	}{
		{
			name:     "OK",
			filename: "app.log",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir, cleanup, err := setupTestDirectory(t, tc.filename)
			assert.NoError(t, err)
			defer cleanup()

			f := &rotating{
				filepath: filepath.Join(dir, tc.filename),
			}

			backupFilepath, err := f.getBackupFilepath()
			assert.NoError(t, err)
			assert.NotEmpty(t, backupFilepath)
			assert.True(t, filepath.IsAbs(backupFilepath))
		})
	}
}

func TestRotating_cleanup(t *testing.T) {
	tests := []struct {
		name                     string
		filename                 string
		maxBackups               int
		maxAge                   time.Duration
		expectedRemainingBackups int
	}{
		{
			name:                     "RetentionDisabled",
			filename:                 "app.log",
			maxBackups:               0,
			maxAge:                   0,
			expectedRemainingBackups: 7,
		},
		{
			name:                     "RetentionEnabled",
			filename:                 "app.log",
			maxBackups:               5,
			maxAge:                   150 * time.Minute, // 2.5 hours
			expectedRemainingBackups: 2,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir, cleanup, err := setupTestDirectory(t, tc.filename)
			assert.NoError(t, err)
			defer cleanup()

			f := &rotating{
				filepath:   filepath.Join(dir, tc.filename),
				maxBackups: tc.maxBackups,
				maxAge:     tc.maxAge,
			}

			err = f.cleanup()
			assert.NoError(t, err)

			remaining, err := f.listBackups()
			assert.NoError(t, err)
			assert.Len(t, remaining, tc.expectedRemainingBackups)
		})
	}
}

func TestRotating_listBackups(t *testing.T) {
	tests := []struct {
		name            string
		filename        string
		expectedBackups int
	}{
		{
			name:            "OK",
			filename:        "app.log",
			expectedBackups: 7,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir, cleanup, err := setupTestDirectory(t, tc.filename)
			assert.NoError(t, err)
			defer cleanup()

			f := &rotating{
				filepath: filepath.Join(dir, tc.filename),
			}

			backups, err := f.listBackups()
			assert.NoError(t, err)
			assert.Len(t, backups, tc.expectedBackups)
		})
	}
}

// setupTestDirectory creates a temporary directory populated with backup-like files for testing.
func setupTestDirectory(t *testing.T, filename string) (string, func(), error) {
	t.Helper()

	ext := filepath.Ext(filename)
	prefix := strings.TrimSuffix(filename, ext)

	dir, err := os.MkdirTemp("", "rotate-file-*")
	if err != nil {
		return "", nil, fmt.Errorf("error creating temporary directory: %w", err)
	}

	cleanup := func() {
		assert.NoError(t, os.RemoveAll(dir))
	}

	// Create dummy subdirectories.
	for _, name := range []string{"foo", "bar"} {
		if err := os.Mkdir(filepath.Join(dir, name), 0755); err != nil {
			cleanup()
			return "", nil, fmt.Errorf("error creating subdirectory %s: %w", name, err)
		}
	}

	now := time.Now().UTC()
	ts1 := now.Add(-1 * time.Hour).Format(backupTimeFormat)
	ts2 := now.Add(-2 * time.Hour).Format(backupTimeFormat)
	ts3 := now.Add(-3 * time.Hour).Format(backupTimeFormat)
	ts4 := now.Add(-4 * time.Hour).Format(backupTimeFormat)
	ts5 := now.Add(-5 * time.Hour).Format(backupTimeFormat)

	names := []string{
		// Active file
		filename,
		// Backup files
		fmt.Sprintf("%s-%s%s", prefix, ts1, ext),
		fmt.Sprintf("%s-%s%s", prefix, ts2, ext),
		fmt.Sprintf("%s-%s%s", prefix, ts3, ext),
		fmt.Sprintf("%s-%s-1%s", prefix, ts3, ext),
		fmt.Sprintf("%s-%s-2%s", prefix, ts3, ext),
		fmt.Sprintf("%s-%s%s", prefix, ts4, ext),
		fmt.Sprintf("%s-%s%s", prefix, ts5, ext),
		// Misc files
		"dummy.txt",
	}

	for _, name := range names {
		if err := os.WriteFile(filepath.Join(dir, name), nil, 0644); err != nil {
			cleanup()
			return "", nil, fmt.Errorf("error creating file %s: %w", name, err)
		}
	}

	return dir, cleanup, nil
}

const loremIpsum = `
Lorem ipsum dolor sit amet, consectetur adipiscing elit, sed do eiusmod tempor incididunt ut labore et dolore magna aliqua.
Ut enim ad minim veniam, quis nostrud exercitation ullamco laboris nisi ut aliquip ex ea commodo consequat.
Duis aute irure dolor in reprehenderit in voluptate velit esse cillum dolore eu fugiat nulla pariatur.
Excepteur sint occaecat cupidatat non proident, sunt in culpa qui officia deserunt mollit anim id est laborum.

Sed ut perspiciatis unde omnis iste natus error sit voluptatem accusantium doloremque laudantium,
totam rem aperiam, eaque ipsa quae ab illo inventore veritatis et quasi architecto beatae vitae dicta sunt explicabo.
Nemo enim ipsam voluptatem quia voluptas sit aspernatur aut odit aut fugit,
sed quia consequuntur magni dolores eos qui ratione voluptatem sequi nesciunt.
Neque porro quisquam est, qui dolorem ipsum quia dolor sit amet, consectetur, adipisci velit,
sed quia non numquam eius modi tempora incidunt ut labore et dolore magnam aliquam quaerat voluptatem.
Ut enim ad minima veniam, quis nostrum exercitationem ullam corporis suscipit laboriosam,
nisi ut aliquid ex ea commodi consequatur?
Quis autem vel eum iure reprehenderit qui in ea voluptate velit esse quam nihil molestiae consequatur,
vel illum qui dolorem eum fugiat quo voluptas nulla pariatur?
`
