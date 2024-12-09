package frontend

import (
	"embed"
	"io/fs"
)

//go:embed dist/*
var DistFs embed.FS

var AssetsSubdirFs = SubdirEmbedFS{Subdir: "dist/assets/", FS: DistFs}

// implement FS interface for a subdirectory "assets" of DistFS
type SubdirEmbedFS struct {
	Subdir string
	FS     embed.FS
}

func (s SubdirEmbedFS) Open(name string) (fs.File, error) {
	return s.FS.Open(s.Subdir + name)
}
