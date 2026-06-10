package gitwalk

import (
	"bytes"
	"fmt"
	"io"
)

// ChangesFromPorcelainZ parses the output of
//
//	git status --porcelain=v1 -uall -z
//
// into Overlay changes. Records are NUL-separated; rename and copy
// records carry the new path in the first field and the original
// path in the immediately following field.
//
// The status codes are mapped as follows:
//
//	"??", "A"   -> ChangeAdded
//	"M", "T"    -> ChangeModified
//	"D"         -> ChangeDeleted
//	"R", "C"    -> ChangeDeleted for the original path plus
//	               ChangeAdded for the new path
//
// open is invoked lazily by the walker when the consumer opens an
// Added or Modified entry. It receives the repo-relative path using
// '/' as separator, matching git's output. open may be nil if the
// caller knows no overlay entry will be opened.
func ChangesFromPorcelainZ(data []byte, open func(relPath string) (io.ReadCloser, error)) ([]Change, error) {
	if len(data) == 0 {
		return nil, nil
	}

	fields := bytes.Split(data, []byte{0})
	if n := len(fields); n > 0 && len(fields[n-1]) == 0 {
		fields = fields[:n-1]
	}

	makeOpen := func(p string) func() (io.ReadCloser, error) {
		if open == nil {
			return nil
		}
		return func() (io.ReadCloser, error) { return open(p) }
	}

	var changes []Change
	for i := 0; i < len(fields); i++ {
		rec := fields[i]
		if len(rec) < 4 || rec[2] != ' ' {
			return nil, fmt.Errorf("malformed porcelain record %d: %q", i, rec)
		}
		x, y := rec[0], rec[1]
		newPath := string(rec[3:])

		if x == 'R' || x == 'C' || y == 'R' || y == 'C' {
			i++
			if i >= len(fields) {
				return nil, fmt.Errorf("rename/copy record missing original path: %q", rec)
			}
			oldPath := string(fields[i])
			changes = append(changes,
				Change{Path: oldPath, Kind: ChangeDeleted},
				Change{Path: newPath, Kind: ChangeAdded, Open: makeOpen(newPath)},
			)
			continue
		}

		switch {
		case x == '?' && y == '?':
			changes = append(changes, Change{Path: newPath, Kind: ChangeAdded, Open: makeOpen(newPath)})
		case x == 'D' || y == 'D':
			changes = append(changes, Change{Path: newPath, Kind: ChangeDeleted})
		case x == 'A' || y == 'A':
			changes = append(changes, Change{Path: newPath, Kind: ChangeAdded, Open: makeOpen(newPath)})
		case x == 'M' || y == 'M' || x == 'T' || y == 'T':
			changes = append(changes, Change{Path: newPath, Kind: ChangeModified, Open: makeOpen(newPath)})
		}
	}
	return changes, nil
}
