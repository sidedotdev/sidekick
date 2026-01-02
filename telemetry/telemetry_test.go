package telemetry

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewDailyRotatingWriter(t *testing.T) {
	tempDir := t.TempDir()

	writer, err := newDailyRotatingWriter(tempDir)
	require.NoError(t, err)
	require.NotNil(t, writer)
	defer writer.Close()

	assert.Equal(t, tempDir, writer.stateHome)
	assert.Equal(t, time.Now().Format("2006-01-02"), writer.currentDate)
	assert.NotNil(t, writer.file)

	expectedFileName := traceFilePrefix + time.Now().Format("2006-01-02") + traceFileSuffix
	_, err = os.Stat(filepath.Join(tempDir, expectedFileName))
	assert.NoError(t, err)
}

func TestNewDailyRotatingWriter_InvalidPath(t *testing.T) {
	writer, err := newDailyRotatingWriter("/nonexistent/path/that/should/not/exist")
	assert.Error(t, err)
	assert.Nil(t, writer)
}

func TestDailyRotatingWriter_Write(t *testing.T) {
	tempDir := t.TempDir()

	writer, err := newDailyRotatingWriter(tempDir)
	require.NoError(t, err)
	defer writer.Close()

	testData := []byte("test trace data\n")
	n, err := writer.Write(testData)
	require.NoError(t, err)
	assert.Equal(t, len(testData), n)

	expectedFileName := traceFilePrefix + time.Now().Format("2006-01-02") + traceFileSuffix
	content, err := os.ReadFile(filepath.Join(tempDir, expectedFileName))
	require.NoError(t, err)
	assert.Equal(t, testData, content)
}

func TestDailyRotatingWriter_MultipleWrites(t *testing.T) {
	tempDir := t.TempDir()

	writer, err := newDailyRotatingWriter(tempDir)
	require.NoError(t, err)
	defer writer.Close()

	testData1 := []byte("first write\n")
	testData2 := []byte("second write\n")

	_, err = writer.Write(testData1)
	require.NoError(t, err)

	_, err = writer.Write(testData2)
	require.NoError(t, err)

	expectedFileName := traceFilePrefix + time.Now().Format("2006-01-02") + traceFileSuffix
	content, err := os.ReadFile(filepath.Join(tempDir, expectedFileName))
	require.NoError(t, err)
	assert.Equal(t, append(testData1, testData2...), content)
}

func TestDailyRotatingWriter_Close(t *testing.T) {
	tempDir := t.TempDir()

	writer, err := newDailyRotatingWriter(tempDir)
	require.NoError(t, err)

	err = writer.Close()
	assert.NoError(t, err)
	assert.Nil(t, writer.file)
}

func TestDailyRotatingWriter_CloseNilFile(t *testing.T) {
	writer := &dailyRotatingWriter{
		stateHome: t.TempDir(),
		file:      nil,
	}

	err := writer.Close()
	assert.NoError(t, err)
}

func TestDailyRotatingWriter_RotateIfNeeded_SameDay(t *testing.T) {
	tempDir := t.TempDir()

	writer, err := newDailyRotatingWriter(tempDir)
	require.NoError(t, err)
	defer writer.Close()

	originalFile := writer.file
	originalDate := writer.currentDate

	err = writer.rotateIfNeeded()
	require.NoError(t, err)

	assert.Equal(t, originalFile, writer.file)
	assert.Equal(t, originalDate, writer.currentDate)
}

func TestDailyRotatingWriter_RotateIfNeeded_NewDay(t *testing.T) {
	tempDir := t.TempDir()

	writer, err := newDailyRotatingWriter(tempDir)
	require.NoError(t, err)
	defer writer.Close()

	// Simulate a previous day
	writer.currentDate = "2020-01-01"
	oldFile := writer.file

	err = writer.rotateIfNeeded()
	require.NoError(t, err)

	assert.NotEqual(t, oldFile, writer.file)
	assert.Equal(t, time.Now().Format("2006-01-02"), writer.currentDate)
}

func TestCleanupOldTraceFiles(t *testing.T) {
	tempDir := t.TempDir()

	// Create more than maxTraceFileCount trace files
	dates := []string{
		"2024-01-01", "2024-01-02", "2024-01-03", "2024-01-04",
		"2024-01-05", "2024-01-06", "2024-01-07", "2024-01-08",
		"2024-01-09", "2024-01-10",
	}

	for _, date := range dates {
		fileName := traceFilePrefix + date + traceFileSuffix
		err := os.WriteFile(filepath.Join(tempDir, fileName), []byte("test"), 0644)
		require.NoError(t, err)
	}

	cleanupOldTraceFiles(tempDir)

	entries, err := os.ReadDir(tempDir)
	require.NoError(t, err)

	var traceFiles []string
	for _, entry := range entries {
		if !entry.IsDir() {
			traceFiles = append(traceFiles, entry.Name())
		}
	}

	assert.Len(t, traceFiles, maxTraceFileCount)

	// Verify oldest files were removed
	for _, date := range dates[:len(dates)-maxTraceFileCount] {
		fileName := traceFilePrefix + date + traceFileSuffix
		_, err := os.Stat(filepath.Join(tempDir, fileName))
		assert.True(t, os.IsNotExist(err), "expected %s to be deleted", fileName)
	}

	// Verify newest files remain
	for _, date := range dates[len(dates)-maxTraceFileCount:] {
		fileName := traceFilePrefix + date + traceFileSuffix
		_, err := os.Stat(filepath.Join(tempDir, fileName))
		assert.NoError(t, err, "expected %s to exist", fileName)
	}
}

func TestCleanupOldTraceFiles_BelowThreshold(t *testing.T) {
	tempDir := t.TempDir()

	// Create fewer than maxTraceFileCount trace files
	dates := []string{"2024-01-01", "2024-01-02", "2024-01-03"}

	for _, date := range dates {
		fileName := traceFilePrefix + date + traceFileSuffix
		err := os.WriteFile(filepath.Join(tempDir, fileName), []byte("test"), 0644)
		require.NoError(t, err)
	}

	cleanupOldTraceFiles(tempDir)

	entries, err := os.ReadDir(tempDir)
	require.NoError(t, err)

	assert.Len(t, entries, len(dates))
}

func TestCleanupOldTraceFiles_IgnoresOtherFiles(t *testing.T) {
	tempDir := t.TempDir()

	// Create trace files
	for i := 1; i <= 10; i++ {
		fileName := traceFilePrefix + "2024-01-" + padInt(i) + traceFileSuffix
		err := os.WriteFile(filepath.Join(tempDir, fileName), []byte("test"), 0644)
		require.NoError(t, err)
	}

	// Create non-trace files
	err := os.WriteFile(filepath.Join(tempDir, "other-file.txt"), []byte("test"), 0644)
	require.NoError(t, err)
	err = os.WriteFile(filepath.Join(tempDir, "traces-incomplete"), []byte("test"), 0644)
	require.NoError(t, err)

	cleanupOldTraceFiles(tempDir)

	// Non-trace files should still exist
	_, err = os.Stat(filepath.Join(tempDir, "other-file.txt"))
	assert.NoError(t, err)
	_, err = os.Stat(filepath.Join(tempDir, "traces-incomplete"))
	assert.NoError(t, err)
}

func TestCleanupOldTraceFiles_EmptyDir(t *testing.T) {
	tempDir := t.TempDir()

	// Should not panic on empty directory
	cleanupOldTraceFiles(tempDir)

	entries, err := os.ReadDir(tempDir)
	require.NoError(t, err)
	assert.Empty(t, entries)
}

func TestCleanupOldTraceFiles_NonexistentDir(t *testing.T) {
	// Should not panic on nonexistent directory
	cleanupOldTraceFiles("/nonexistent/path/that/should/not/exist")
}

func padInt(i int) string {
	if i < 10 {
		return "0" + string(rune('0'+i))
	}
	return string(rune('0'+i/10)) + string(rune('0'+i%10))
}
