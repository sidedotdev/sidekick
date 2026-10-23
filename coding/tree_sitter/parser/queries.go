package parser

import "io/fs"

// SignatureQueries exposes the embedded signature query templates so callers
// can fingerprint the extraction inputs that shape signature outlines.
func SignatureQueries() fs.FS {
	return signatureQueriesFS
}
