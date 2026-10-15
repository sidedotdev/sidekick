package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"go/types"
	"os"
	"runtime"
	"time"

	"golang.org/x/tools/go/packages"
)

type measurement struct {
	Iteration      int           `json:"iteration"`
	Elapsed        time.Duration `json:"elapsed"`
	Packages       int           `json:"packages"`
	Definitions    int           `json:"definitions"`
	Uses           int           `json:"uses"`
	SourceResolved int           `json:"sourceResolved"`
	HeapBytes      uint64        `json:"heapBytes"`
	SystemBytes    uint64        `json:"systemBytes"`
}

func main() {
	dir := flag.String("dir", ".", "Go workspace to load")
	pattern := flag.String("pattern", "./...", "workspace package pattern")
	iterations := flag.Int("iterations", 3, "independent loads, modeling invalidation after edits")
	deps := flag.Bool("dependency-syntax", false, "eagerly type-check dependency source too; exported positions do not require this")
	flag.Parse()
	if *iterations < 1 {
		fmt.Fprintln(os.Stderr, "iterations must be positive")
		os.Exit(1)
	}
	mode := packages.LoadSyntax
	if *deps {
		mode = packages.LoadAllSyntax
	}
	for iteration := 0; iteration < *iterations; iteration++ {
		result, err := measure(*dir, *pattern, mode)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		result.Iteration = iteration
		if err := json.NewEncoder(os.Stdout).Encode(result); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}
}

func measure(dir, pattern string, mode packages.LoadMode) (measurement, error) {
	start := time.Now()
	loaded, err := packages.Load(&packages.Config{
		Dir: dir, Mode: mode, Tests: true,
	}, pattern)
	if err != nil {
		return measurement{}, err
	}
	if packages.PrintErrors(loaded) != 0 {
		return measurement{}, fmt.Errorf("package loading reported errors")
	}
	result := measurement{Elapsed: time.Since(start)}
	objects := make(map[types.Object]bool)
	packages.Visit(loaded, nil, func(pkg *packages.Package) {
		result.Packages++
		if pkg.TypesInfo == nil {
			return
		}
		for _, object := range pkg.TypesInfo.Defs {
			if object != nil {
				objects[object] = true
				result.Definitions++
			}
		}
		for _, object := range pkg.TypesInfo.Uses {
			result.Uses++
			if object != nil && pkg.Fset.Position(object.Pos()).Filename != "" {
				result.SourceResolved++
			}
		}
	})
	runtime.GC()
	var memory runtime.MemStats
	runtime.ReadMemStats(&memory)
	result.HeapBytes = memory.HeapAlloc
	result.SystemBytes = memory.Sys
	runtime.KeepAlive(loaded)
	runtime.KeepAlive(objects)
	return result, nil
}
