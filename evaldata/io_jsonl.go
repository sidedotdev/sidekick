package evaldata

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
)

// WriteDatasetAJSONL writes Dataset A rows to a JSONL file.
func WriteDatasetAJSONL(path string, rows []DatasetARow) error {
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("failed to create file: %w", err)
	}
	defer f.Close()

	w := bufio.NewWriter(f)
	for _, row := range rows {
		data, err := json.Marshal(row)
		if err != nil {
			return fmt.Errorf("failed to marshal row: %w", err)
		}
		w.Write(data)
		w.WriteString("\n")
	}
	return w.Flush()
}

// WriteDatasetBJSONL writes Dataset B rows to a JSONL file.
func WriteDatasetBJSONL(path string, rows []DatasetBRow) error {
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("failed to create file: %w", err)
	}
	defer f.Close()

	w := bufio.NewWriter(f)
	for _, row := range rows {
		data, err := json.Marshal(row)
		if err != nil {
			return fmt.Errorf("failed to marshal row: %w", err)
		}
		w.Write(data)
		w.WriteString("\n")
	}
	return w.Flush()
}

// ReadDatasetAJSONL reads Dataset A rows from a JSONL file.
func ReadDatasetAJSONL(path string) ([]DatasetARow, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("failed to open file: %w", err)
	}
	defer f.Close()
	return ReadDatasetAFromReader(f)
}

// ReadDatasetAFromReader reads Dataset A rows from an io.Reader.
func ReadDatasetAFromReader(r io.Reader) ([]DatasetARow, error) {
	var rows []DatasetARow
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var row DatasetARow
		if err := json.Unmarshal(line, &row); err != nil {
			return nil, err
		}
		rows = append(rows, row)
	}
	return rows, scanner.Err()
}

// ReadDatasetBJSONL reads Dataset B rows from a JSONL file.
func ReadDatasetBJSONL(path string) ([]DatasetBRow, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("failed to open file: %w", err)
	}
	defer f.Close()
	return ReadDatasetBFromReader(f)
}

// ReadDatasetBFromReader reads Dataset B rows from an io.Reader.
func ReadDatasetBFromReader(r io.Reader) ([]DatasetBRow, error) {
	var rows []DatasetBRow
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var row DatasetBRow
		if err := json.Unmarshal(line, &row); err != nil {
			return nil, err
		}
		rows = append(rows, row)
	}
	return rows, scanner.Err()
}
