package diffanalysis

import (
	"errors"
	"slices"
	"testing"
)

func TestGetSymbolDelta_AddedFunction(t *testing.T) {
	t.Parallel()

	// New content has two functions (old content had just existingFunc)
	newContent := `package main

func existingFunc() {
	println("existing")
}

func newFunc() {
	println("new")
}
`

	// Create a diff that adds the new function
	diff := `diff --git a/main.go b/main.go
--- a/main.go
+++ b/main.go
@@ -3,3 +3,7 @@ package main
 func existingFunc() {
 	println("existing")
 }
+
+func newFunc() {
+	println("new")
+}
`

	files, err := ParseUnifiedDiff(diff)
	if err != nil {
		t.Fatalf("failed to parse diff: %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("expected 1 file, got %d", len(files))
	}

	delta, err := GetSymbolDelta(files[0], newContent)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should have one added symbol
	if len(delta.AddedSymbols) != 1 {
		t.Errorf("expected 1 added symbol, got %d: %v", len(delta.AddedSymbols), delta.AddedSymbols)
	} else if delta.AddedSymbols[0] != "newFunc" {
		t.Errorf("expected added symbol 'newFunc', got '%s'", delta.AddedSymbols[0])
	}

	// Should have no removed symbols
	if len(delta.RemovedSymbols) != 0 {
		t.Errorf("expected 0 removed symbols, got %d: %v", len(delta.RemovedSymbols), delta.RemovedSymbols)
	}

	// Should have no changed symbols (existingFunc wasn't modified)
	if len(delta.ChangedSymbols) != 0 {
		t.Errorf("expected 0 changed symbols, got %d: %v", len(delta.ChangedSymbols), delta.ChangedSymbols)
	}
}

func TestGetSymbolDelta_RemovedFunction(t *testing.T) {
	t.Parallel()

	// New content has one function (the other was removed)
	newContent := `package main

func remainingFunc() {
	println("remaining")
}
`

	// Diff shows removal of a function
	diff := `diff --git a/main.go b/main.go
--- a/main.go
+++ b/main.go
@@ -1,9 +1,5 @@
 package main
 
-func removedFunc() {
-	println("removed")
-}
-
 func remainingFunc() {
 	println("remaining")
 }
`

	files, err := ParseUnifiedDiff(diff)
	if err != nil {
		t.Fatalf("failed to parse diff: %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("expected 1 file, got %d", len(files))
	}

	delta, err := GetSymbolDelta(files[0], newContent)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should have no added symbols
	if len(delta.AddedSymbols) != 0 {
		t.Errorf("expected 0 added symbols, got %d: %v", len(delta.AddedSymbols), delta.AddedSymbols)
	}

	// Should have one removed symbol
	if len(delta.RemovedSymbols) != 1 {
		t.Errorf("expected 1 removed symbol, got %d: %v", len(delta.RemovedSymbols), delta.RemovedSymbols)
	} else if delta.RemovedSymbols[0] != "removedFunc" {
		t.Errorf("expected removed symbol 'removedFunc', got '%s'", delta.RemovedSymbols[0])
	}

	// Should have no changed symbols
	if len(delta.ChangedSymbols) != 0 {
		t.Errorf("expected 0 changed symbols, got %d: %v", len(delta.ChangedSymbols), delta.ChangedSymbols)
	}
}

func TestGetSymbolDelta_ChangedFunction(t *testing.T) {
	t.Parallel()

	// New content has modified function body
	newContent := `package main

func myFunc() {
	println("modified")
	println("extra line")
}
`

	// Diff shows modification inside the function
	diff := `diff --git a/main.go b/main.go
--- a/main.go
+++ b/main.go
@@ -1,5 +1,6 @@
 package main
 
 func myFunc() {
-	println("original")
+	println("modified")
+	println("extra line")
 }
`

	files, err := ParseUnifiedDiff(diff)
	if err != nil {
		t.Fatalf("failed to parse diff: %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("expected 1 file, got %d", len(files))
	}

	delta, err := GetSymbolDelta(files[0], newContent)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should have no added symbols
	if len(delta.AddedSymbols) != 0 {
		t.Errorf("expected 0 added symbols, got %d: %v", len(delta.AddedSymbols), delta.AddedSymbols)
	}

	// Should have no removed symbols
	if len(delta.RemovedSymbols) != 0 {
		t.Errorf("expected 0 removed symbols, got %d: %v", len(delta.RemovedSymbols), delta.RemovedSymbols)
	}

	// Should have one changed symbol
	if len(delta.ChangedSymbols) != 1 {
		t.Errorf("expected 1 changed symbol, got %d: %v", len(delta.ChangedSymbols), delta.ChangedSymbols)
	} else if delta.ChangedSymbols[0] != "myFunc" {
		t.Errorf("expected changed symbol 'myFunc', got '%s'", delta.ChangedSymbols[0])
	}
}

func TestGetSymbolDelta_MultiHunkLineShift(t *testing.T) {
	t.Parallel()

	// This test verifies that when earlier hunks add lines, the changed symbol
	// detection still correctly identifies the symbol that was modified in a later hunk.
	newContent := `package main

func firstFunc() {
	println("first")
	println("added line 1")
	println("added line 2")
}

func secondFunc() {
	println("second modified")
}

func thirdFunc() {
	println("third")
}
`

	// Diff adds lines to firstFunc and modifies secondFunc
	// The line numbers in the new file for secondFunc are shifted due to additions in firstFunc
	diff := `diff --git a/main.go b/main.go
--- a/main.go
+++ b/main.go
@@ -1,13 +1,15 @@
 package main
 
 func firstFunc() {
 	println("first")
+	println("added line 1")
+	println("added line 2")
 }
 
 func secondFunc() {
-	println("second")
+	println("second modified")
 }
 
 func thirdFunc() {
 	println("third")
 }
`

	files, err := ParseUnifiedDiff(diff)
	if err != nil {
		t.Fatalf("failed to parse diff: %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("expected 1 file, got %d", len(files))
	}

	delta, err := GetSymbolDelta(files[0], newContent)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should have no added or removed symbols
	if len(delta.AddedSymbols) != 0 {
		t.Errorf("expected 0 added symbols, got %d: %v", len(delta.AddedSymbols), delta.AddedSymbols)
	}
	if len(delta.RemovedSymbols) != 0 {
		t.Errorf("expected 0 removed symbols, got %d: %v", len(delta.RemovedSymbols), delta.RemovedSymbols)
	}

	// Should have two changed symbols: firstFunc and secondFunc
	if len(delta.ChangedSymbols) != 2 {
		t.Errorf("expected 2 changed symbols, got %d: %v", len(delta.ChangedSymbols), delta.ChangedSymbols)
	}

	// Check that both expected symbols are in the changed list
	expectedChanged := []string{"firstFunc", "secondFunc"}
	for _, expected := range expectedChanged {
		found := false
		for _, actual := range delta.ChangedSymbols {
			if actual == expected {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected changed symbol '%s' not found in %v", expected, delta.ChangedSymbols)
		}
	}

	// thirdFunc should NOT be in changed symbols
	for _, sym := range delta.ChangedSymbols {
		if sym == "thirdFunc" {
			t.Errorf("thirdFunc should not be in changed symbols")
		}
	}
}

func TestGetSymbolDelta_NewFile(t *testing.T) {
	t.Parallel()

	newContent := `package main

func newFileFunc() {
	println("new file")
}
`

	diff := `diff --git a/newfile.go b/newfile.go
new file mode 100644
--- /dev/null
+++ b/newfile.go
@@ -0,0 +1,5 @@
+package main
+
+func newFileFunc() {
+	println("new file")
+}
`

	files, err := ParseUnifiedDiff(diff)
	if err != nil {
		t.Fatalf("failed to parse diff: %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("expected 1 file, got %d", len(files))
	}

	delta, err := GetSymbolDelta(files[0], newContent)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// All symbols in a new file should be "added"
	if len(delta.AddedSymbols) != 1 {
		t.Errorf("expected 1 added symbol, got %d: %v", len(delta.AddedSymbols), delta.AddedSymbols)
	}
	if len(delta.RemovedSymbols) != 0 {
		t.Errorf("expected 0 removed symbols, got %d: %v", len(delta.RemovedSymbols), delta.RemovedSymbols)
	}
	if len(delta.ChangedSymbols) != 0 {
		t.Errorf("expected 0 changed symbols, got %d: %v", len(delta.ChangedSymbols), delta.ChangedSymbols)
	}
}

func TestGetSymbolDelta_DeletedFile(t *testing.T) {
	t.Parallel()

	// For a deleted file, newContent is empty
	newContent := ""

	diff := `diff --git a/deleted.go b/deleted.go
deleted file mode 100644
--- a/deleted.go
+++ /dev/null
@@ -1,5 +0,0 @@
-package main
-
-func deletedFunc() {
-	println("deleted")
-}
`

	files, err := ParseUnifiedDiff(diff)
	if err != nil {
		t.Fatalf("failed to parse diff: %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("expected 1 file, got %d", len(files))
	}

	delta, err := GetSymbolDelta(files[0], newContent)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// All symbols in a deleted file should be "removed"
	if len(delta.AddedSymbols) != 0 {
		t.Errorf("expected 0 added symbols, got %d: %v", len(delta.AddedSymbols), delta.AddedSymbols)
	}
	if len(delta.RemovedSymbols) != 1 {
		t.Errorf("expected 1 removed symbol, got %d: %v", len(delta.RemovedSymbols), delta.RemovedSymbols)
	}
	if len(delta.ChangedSymbols) != 0 {
		t.Errorf("expected 0 changed symbols, got %d: %v", len(delta.ChangedSymbols), delta.ChangedSymbols)
	}
}

func TestGetSymbolDelta_BinaryFile(t *testing.T) {
	t.Parallel()

	diff := `diff --git a/image.png b/image.png
Binary files a/image.png and b/image.png differ
`

	files, err := ParseUnifiedDiff(diff)
	if err != nil {
		t.Fatalf("failed to parse diff: %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("expected 1 file, got %d", len(files))
	}

	_, err = GetSymbolDelta(files[0], "")
	if !errors.Is(err, ErrBinaryFile) {
		t.Errorf("expected ErrBinaryFile, got %v", err)
	}
}

func TestGetSymbolDelta_UnsupportedLanguage(t *testing.T) {
	t.Parallel()

	diff := `diff --git a/readme.md b/readme.md
--- a/readme.md
+++ b/readme.md
@@ -1 +1 @@
-# Old Title
+# New Title
`

	files, err := ParseUnifiedDiff(diff)
	if err != nil {
		t.Fatalf("failed to parse diff: %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("expected 1 file, got %d", len(files))
	}

	_, err = GetSymbolDelta(files[0], "# New Title\n")
	if !errors.Is(err, ErrUnsupportedLanguage) {
		t.Errorf("expected ErrUnsupportedLanguage, got %v", err)
	}
}

func TestGetSymbolDelta_Python(t *testing.T) {
	t.Parallel()

	newContent := `def existing_func():
    print("existing")

def new_func():
    print("new")
`

	diff := `diff --git a/main.py b/main.py
--- a/main.py
+++ b/main.py
@@ -1,2 +1,5 @@
 def existing_func():
     print("existing")
+
+def new_func():
+    print("new")
`

	files, err := ParseUnifiedDiff(diff)
	if err != nil {
		t.Fatalf("failed to parse diff: %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("expected 1 file, got %d", len(files))
	}

	delta, err := GetSymbolDelta(files[0], newContent)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(delta.AddedSymbols) != 1 {
		t.Errorf("expected 1 added symbol, got %d: %v", len(delta.AddedSymbols), delta.AddedSymbols)
	}
}

func TestGetSymbolDelta_TypeScript(t *testing.T) {
	t.Parallel()

	newContent := `function existingFunc(): void {
    console.log("existing");
}

function newFunc(): void {
    console.log("new");
}
`

	diff := `diff --git a/main.ts b/main.ts
--- a/main.ts
+++ b/main.ts
@@ -1,3 +1,7 @@
 function existingFunc(): void {
     console.log("existing");
 }
+
+function newFunc(): void {
+    console.log("new");
+}
`

	files, err := ParseUnifiedDiff(diff)
	if err != nil {
		t.Fatalf("failed to parse diff: %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("expected 1 file, got %d", len(files))
	}

	delta, err := GetSymbolDelta(files[0], newContent)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(delta.AddedSymbols) != 1 {
		t.Errorf("expected 1 added symbol, got %d: %v", len(delta.AddedSymbols), delta.AddedSymbols)
	}
}

func TestGetSymbolDelta_MultipleChanges(t *testing.T) {
	t.Parallel()

	// Test a complex scenario with added, removed, and changed symbols
	newContent := `package main

func unchangedFunc() {
	println("unchanged")
}

func modifiedFunc() {
	println("modified body")
}

func addedFunc() {
	println("added")
}
`

	diff := `diff --git a/main.go b/main.go
--- a/main.go
+++ b/main.go
@@ -1,13 +1,13 @@
 package main
 
 func unchangedFunc() {
 	println("unchanged")
 }
 
 func modifiedFunc() {
-	println("original body")
+	println("modified body")
 }
 
-func removedFunc() {
-	println("removed")
+func addedFunc() {
+	println("added")
 }
`

	files, err := ParseUnifiedDiff(diff)
	if err != nil {
		t.Fatalf("failed to parse diff: %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("expected 1 file, got %d", len(files))
	}

	delta, err := GetSymbolDelta(files[0], newContent)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Check added symbols
	if len(delta.AddedSymbols) != 1 {
		t.Errorf("expected 1 added symbol, got %d: %v", len(delta.AddedSymbols), delta.AddedSymbols)
	} else if !slices.Contains(delta.AddedSymbols, "addedFunc") {
		t.Errorf("expected 'addedFunc' in added symbols, got %v", delta.AddedSymbols)
	}

	// Check removed symbols
	if len(delta.RemovedSymbols) != 1 {
		t.Errorf("expected 1 removed symbol, got %d: %v", len(delta.RemovedSymbols), delta.RemovedSymbols)
	} else if !slices.Contains(delta.RemovedSymbols, "removedFunc") {
		t.Errorf("expected 'removedFunc' in removed symbols, got %v", delta.RemovedSymbols)
	}

	// Check changed symbols
	if len(delta.ChangedSymbols) != 1 {
		t.Errorf("expected 1 changed symbol, got %d: %v", len(delta.ChangedSymbols), delta.ChangedSymbols)
	} else if !slices.Contains(delta.ChangedSymbols, "modifiedFunc") {
		t.Errorf("expected 'modifiedFunc' in changed symbols, got %v", delta.ChangedSymbols)
	}
}
