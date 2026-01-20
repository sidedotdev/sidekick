package logger

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewDailyRotatingLogWriter(t *testing.T) {
	t.Parallel()
	tempDir := t.TempDir()

	writer, err := newDailyRotatingLogWriter(tempDir)
	require.NoError(t, err)
	require.NotNil(t, writer)
	defer writer.Close()

	assert.Equal(t, time.Now().Format("2006-01-02"), writer.currentDate)
	assert.NotNil(t, writer.file)

	expectedFileName := logFilePrefix + time.Now().Format("2006-01-02") + logFileSuffix
	_, err = os.Stat(filepath.Join(tempDir, expectedFileName))
	assert.NoError(t, err)
}

func TestNewDailyRotatingLogWriter_InvalidPath(t *testing.T) {
	t.Parallel()
	writer, err := newDailyRotatingLogWriter("/nonexistent/path/that/should/not/exist")
	assert.Error(t, err)
	assert.Nil(t, writer)
}

func TestDailyRotatingLogWriter_Write(t *testing.T) {
	t.Parallel()
	tempDir := t.TempDir()

	writer, err := newDailyRotatingLogWriter(tempDir)
	require.NoError(t, err)
	defer writer.Close()

	testData := []byte("test log message\n")
	n, err := writer.Write(testData)
	assert.NoError(t, err)
	assert.Equal(t, len(testData), n)

	expectedFileName := logFilePrefix + time.Now().Format("2006-01-02") + logFileSuffix
	content, err := os.ReadFile(filepath.Join(tempDir, expectedFileName))
	assert.NoError(t, err)
	assert.Equal(t, testData, content)
}

func TestDailyRotatingLogWriter_Close(t *testing.T) {
	t.Parallel()
	tempDir := t.TempDir()

	writer, err := newDailyRotatingLogWriter(tempDir)
	require.NoError(t, err)

	err = writer.Close()
	assert.NoError(t, err)
	assert.Nil(t, writer.file)

	// Closing again should not error
	err = writer.Close()
	assert.NoError(t, err)
}

func TestCleanupOldLogFiles(t *testing.T) {
	t.Parallel()
	tempDir := t.TempDir()

	// Create 10 log files with different dates
	for i := 0; i < 10; i++ {
		date := time.Now().AddDate(0, 0, -i).Format("2006-01-02")
		fileName := logFilePrefix + date + logFileSuffix
		err := os.WriteFile(filepath.Join(tempDir, fileName), []byte("test"), 0644)
		require.NoError(t, err)
	}

	cleanupOldLogFiles(tempDir)

	entries, err := os.ReadDir(tempDir)
	require.NoError(t, err)

	var logFiles []string
	for _, entry := range entries {
		if !entry.IsDir() {
			logFiles = append(logFiles, entry.Name())
		}
	}

	assert.Equal(t, maxLogFileCount, len(logFiles))
}

func TestCleanupOldLogFiles_BelowThreshold(t *testing.T) {
	t.Parallel()
	tempDir := t.TempDir()

	// Create fewer files than the threshold
	for i := 0; i < 3; i++ {
		date := time.Now().AddDate(0, 0, -i).Format("2006-01-02")
		fileName := logFilePrefix + date + logFileSuffix
		err := os.WriteFile(filepath.Join(tempDir, fileName), []byte("test"), 0644)
		require.NoError(t, err)
	}

	cleanupOldLogFiles(tempDir)

	entries, err := os.ReadDir(tempDir)
	require.NoError(t, err)

	assert.Equal(t, 3, len(entries))
}

func TestCleanupOldLogFiles_IgnoresOtherFiles(t *testing.T) {
	t.Parallel()
	tempDir := t.TempDir()

	// Create log files
	for i := 0; i < 10; i++ {
		date := time.Now().AddDate(0, 0, -i).Format("2006-01-02")
		fileName := logFilePrefix + date + logFileSuffix
		err := os.WriteFile(filepath.Join(tempDir, fileName), []byte("test"), 0644)
		require.NoError(t, err)
	}

	// Create other files that should not be touched
	otherFiles := []string{"other.txt", "random.log", "sidekick.txt"}
	for _, f := range otherFiles {
		err := os.WriteFile(filepath.Join(tempDir, f), []byte("test"), 0644)
		require.NoError(t, err)
	}

	cleanupOldLogFiles(tempDir)

	// Verify other files still exist
	for _, f := range otherFiles {
		_, err := os.Stat(filepath.Join(tempDir, f))
		assert.NoError(t, err, "file %s should still exist", f)
	}

	// Verify only maxLogFileCount log files remain
	entries, err := os.ReadDir(tempDir)
	require.NoError(t, err)

	var logFileCount int
	for _, entry := range entries {
		if !entry.IsDir() {
			name := entry.Name()
			if len(name) > len(logFilePrefix)+len(logFileSuffix) &&
				name[:len(logFilePrefix)] == logFilePrefix &&
				name[len(name)-len(logFileSuffix):] == logFileSuffix {
				logFileCount++
			}
		}
	}
	assert.Equal(t, maxLogFileCount, logFileCount)
}
