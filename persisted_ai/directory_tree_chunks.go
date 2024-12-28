package persisted_ai

import (
	"io/fs"
	"path/filepath"
	"sidekick/common"
	"sidekick/utils"
	"sort"
	"strings"
)

type DirChunk struct {
	Name  string
	Paths []PathInfo
}

type PathInfo struct {
	Path     string
	DirEntry fs.DirEntry
}

type PathNode struct {
	pathInfo PathInfo
	depth    int
	children []*PathNode
}

const (
	MIN_CHUNK_SIZE = 1
	MAX_CHUNK_SIZE = 40
)

func GetDirectoryChunks(basePath string) []DirChunk {
	allPaths := []PathInfo{}

	common.WalkCodeDirectory(basePath, func(path string, entry fs.DirEntry) error {
		relativePath := strings.Replace(path, basePath, "", 1)
		allPaths = append(allPaths, PathInfo{Path: relativePath, DirEntry: entry})
		return nil
	})

	allPaths = breadthFirstOrder(allPaths)
	//fmt.Println("All paths in order:")
	for i, path := range allPaths {
		pathStr := strings.TrimPrefix(path.Path, string(filepath.Separator))
		allPaths[i].Path = pathStr

		//if path.DirEntry.IsDir() {
		//	pathStr += string(filepath.Separator)
		//}
		//fmt.Println(pathStr)
	}

	//fmt.Println()
	//fmt.Println("List of chunks:")
	chunks := groupIntoChunks(allPaths)
	for _, chunk := range chunks {
		//fmt.Println("Chunk:", chunk.Name)
		for _, path := range chunk.Paths {
			pathStr := path.Path
			if path.DirEntry.IsDir() {
				pathStr += string(filepath.Separator)
			}
			//fmt.Println(pathStr)
		}
		//fmt.Println(len(chunk.Paths))
		//fmt.Println()
	}

	return chunks
}

func breadthFirstOrder(pathInfos []PathInfo) []PathInfo {
	root := &PathNode{pathInfo: PathInfo{Path: "", DirEntry: nil}, depth: -1}

	// Build tree structure
	for _, pi := range pathInfos {
		components := strings.Split(pi.Path, "/")
		currentNode := root
		for i, component := range components {
			found := false
			for _, child := range currentNode.children {
				if child.pathInfo.Path == component {
					currentNode = child
					found = true
					break
				}
			}
			if !found {
				newNode := &PathNode{
					pathInfo: PathInfo{Path: component, DirEntry: nil},
					depth:    i,
				}
				if i == len(components)-1 {
					newNode.pathInfo = pi // Use the full PathInfo for leaf nodes
				}
				currentNode.children = append(currentNode.children, newNode)
				currentNode = newNode
			}
		}
	}

	// Sort children at each level
	var sortNode func(*PathNode)
	sortNode = func(node *PathNode) {
		sort.Slice(node.children, func(i, j int) bool {
			return node.children[i].pathInfo.Path < node.children[j].pathInfo.Path
		})
		for _, child := range node.children {
			sortNode(child)
		}
	}
	sortNode(root)

	// Traverse tree to get ordered paths
	var result []PathInfo
	var traverse func(*PathNode, string)
	traverse = func(node *PathNode, currentPath string) {
		if node.depth >= 0 {
			if node.pathInfo.DirEntry != nil {
				result = append(result, node.pathInfo)
			}
		}

		for _, child := range node.children {
			childPath := filepath.Join(currentPath, node.pathInfo.Path)
			traverse(child, childPath)
		}
	}
	traverse(root, "")

	return result
}

func groupIntoChunks(sortedPaths []PathInfo) []DirChunk {
	var chunks []DirChunk
	if len(sortedPaths) == 0 {
		return chunks
	}

	currentChunk := DirChunk{
		Name:  getParentDir(sortedPaths[0].Path),
		Paths: []PathInfo{sortedPaths[0]},
	}
	if currentChunk.Name != "" {
		currentChunk.Name += string(filepath.Separator)
	}

	for i := 1; i < len(sortedPaths); i++ {
		currentPath := sortedPaths[i]
		prevPath := sortedPaths[i-1]

		if shouldStartNewChunk(prevPath, currentPath, currentChunk) {
			chunks = append(chunks, currentChunk)
			currentChunk = DirChunk{
				Name:  getParentDir(currentPath.Path),
				Paths: []PathInfo{currentPath},
			}
			if currentChunk.Name != "" {
				currentChunk.Name += string(filepath.Separator)
			}
		} else {
			currentChunk.Paths = append(currentChunk.Paths, currentPath)
		}
	}

	// Add the last chunk
	chunks = append(chunks, currentChunk)

	// Combine similar chunks
	chunks = combineSimlarChunks(chunks)

	return chunks
}

func shouldStartNewChunk(prev, current PathInfo, currentChunk DirChunk) bool {
	prevParent := getParentDir(prev.Path)
	currentParent := getParentDir(current.Path)

	// Start a new chunk if:
	// 1. The parent directory changes
	if prevParent != currentParent {
		return true
	}

	// 2. There's a significant change in path depth
	prevDepth := strings.Count(prev.Path, "/")
	currentDepth := strings.Count(current.Path, "/")
	if prevDepth-currentDepth > 1 || currentDepth-prevDepth > 1 {
		return true
	}

	// 3. We've reached the max chunk size
	if len(currentChunk.Paths) >= MAX_CHUNK_SIZE {
		return true
	}

	return false
}

func combineSimlarChunks(inputChunks []DirChunk) []DirChunk {
	if len(inputChunks) <= 1 {
		return inputChunks
	}

	var combinedChunks []DirChunk
	combinedChunks = append(combinedChunks, inputChunks[0])

	for i := 1; i < len(inputChunks); i++ {
		currentChunk := inputChunks[i]

		//combined := false

		for len(combinedChunks) > 0 {
			// Use lower max chunk size because we're combining chunks that are just "similar"
			if len(currentChunk.Paths)+len(combinedChunks[len(combinedChunks)-1].Paths) >= MAX_CHUNK_SIZE {
				break
			}

			previousChunk := combinedChunks[len(combinedChunks)-1]

			if areSimilarChunks(previousChunk, currentChunk) {
				newCombinedChunk := combineChunks(previousChunk, currentChunk)
				combinedChunks = combinedChunks[:len(combinedChunks)-1] // Remove the previous chunk
				currentChunk = newCombinedChunk                         // Set the combined chunk as the current one for potential further combinations
				//combined = true
			} else {
				break // Break if not similar, as we only check against the previous chunk
			}
		}

		//if !combined {
		combinedChunks = append(combinedChunks, currentChunk)
		//}
	}

	return combinedChunks
}

func combineChunks(chunk1, chunk2 DirChunk) DirChunk {
	// Combine names by finding the common prefix
	combinedName := longestCommonPrefix(chunk1.Name, chunk2.Name)

	// Combine paths
	combinedPaths := append(chunk1.Paths, chunk2.Paths...)

	return DirChunk{
		Name:  combinedName,
		Paths: combinedPaths,
	}
}

func areSimilarChunks(chunk1, chunk2 DirChunk) bool {
	// Check if the chunk names (parent directories) are similar
	if hasSimilarName(chunk1.Name, chunk2.Name) {
		// only do this when the new chunk is small
		if len(chunk2.Paths) < MAX_CHUNK_SIZE/4 {
			return true
		}
	}

	// TODO if chunk2 is VERY small, find if all paths in chunk2 have at least
	// one match in chunk1 that is very similar
	return false
}

func hasSimilarName(s1, s2 string) bool {
	commonPrefix := longestCommonPrefix(s1, s2)
	// one is fully contained within the other
	if float64(len(commonPrefix)) >= float64(min(len(s1), len(s2)))*1.0 {
		// and the other is at least x% of the length of the longer string
		if float64(len(commonPrefix)) >= float64(max(len(s1), len(s2)))*0.8 {
			return true
		}
	}

	similarity := utils.StringSimilarity(s1, s2)
	return similarity > 0.925
}

func longestCommonPrefix(s1, s2 string) string {
	minLen := min(len(s1), len(s2))
	for i := 0; i < minLen; i++ {
		if s1[i] != s2[i] {
			return s1[:i]
		}
	}
	return s1[:minLen]
}

func getParentDir(path string) string {
	parent := filepath.Dir(path)
	if parent == "." {
		return ""
	}
	return parent
}
