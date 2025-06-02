import { describe, it, expect } from 'vitest';
import { parseDiff, getFileLanguage } from '../diffUtils';

describe('getFileLanguage', () => {
  it('should return correct language for common file extensions', () => {
    expect(getFileLanguage('file.js')).toBe('javascript');
    expect(getFileLanguage('file.ts')).toBe('typescript');
    expect(getFileLanguage('file.py')).toBe('python');
    expect(getFileLanguage('file.go')).toBe('go');
    expect(getFileLanguage('file.vue')).toBe('vue');
    expect(getFileLanguage('file.json')).toBe('json');
    expect(getFileLanguage('file.md')).toBe('markdown');
  });

  it('should handle case insensitive extensions', () => {
    expect(getFileLanguage('file.JS')).toBe('javascript');
    expect(getFileLanguage('file.TS')).toBe('typescript');
    expect(getFileLanguage('file.PY')).toBe('python');
  });

  it('should return null for unknown extensions', () => {
    expect(getFileLanguage('file.unknown')).toBe(null);
    expect(getFileLanguage('file.xyz')).toBe(null);
  });

  it('should return null for files without extensions', () => {
    expect(getFileLanguage('README')).toBe(null);
    expect(getFileLanguage('Makefile')).toBe(null);
  });

  it('should return null for undefined or null input', () => {
    expect(getFileLanguage(undefined)).toBe(null);
    expect(getFileLanguage('')).toBe(null);
  });

  it('should handle files with multiple dots', () => {
    expect(getFileLanguage('file.test.js')).toBe('javascript');
    expect(getFileLanguage('component.spec.ts')).toBe('typescript');
  });
});

describe('parseDiff', () => {
  it('should return empty array for empty or null input', () => {
    expect(parseDiff('')).toEqual([]);
    expect(parseDiff('   ')).toEqual([]);
  });

  it('should parse a single file diff correctly', () => {
    const singleFileDiff = `diff --git a/src/test.js b/src/test.js
index 1234567..abcdefg 100644
--- a/src/test.js
+++ b/src/test.js
@@ -1,3 +1,4 @@
 function test() {
+  console.log('added line');
   return true;
 }`;

    const result = parseDiff(singleFileDiff);
    
    expect(result).toHaveLength(1);
    expect(result[0].oldFile.fileName).toBe('src/test.js');
    expect(result[0].newFile.fileName).toBe('src/test.js');
    expect(result[0].oldFile.fileLang).toBe('javascript');
    expect(result[0].newFile.fileLang).toBe('javascript');
    expect(result[0].linesAdded).toBe(1);
    expect(result[0].linesRemoved).toBe(0);
    expect(result[0].linesUnchanged).toBe(3);
    expect(result[0].hunks).toHaveLength(1);
  });

  it('should parse multiple file diffs correctly', () => {
    const multiFileDiff = `diff --git a/src/file1.js b/src/file1.js
index 1234567..abcdefg 100644
--- a/src/file1.js
+++ b/src/file1.js
@@ -1,2 +1,3 @@
 line1
+added line
 line2
diff --git a/src/file2.py b/src/file2.py
index 7890123..fedcba9 100644
--- a/src/file2.py
+++ b/src/file2.py
@@ -1,3 +1,2 @@
 def test():
-    removed line
     return True`;

    const result = parseDiff(multiFileDiff);
    
    expect(result).toHaveLength(2);
    
    // First file
    expect(result[0].oldFile.fileName).toBe('src/file1.js');
    expect(result[0].newFile.fileName).toBe('src/file1.js');
    expect(result[0].oldFile.fileLang).toBe('javascript');
    expect(result[0].linesAdded).toBe(1);
    expect(result[0].linesRemoved).toBe(0);
    expect(result[0].linesUnchanged).toBe(2);
    
    // Second file
    expect(result[1].oldFile.fileName).toBe('src/file2.py');
    expect(result[1].newFile.fileName).toBe('src/file2.py');
    expect(result[1].oldFile.fileLang).toBe('python');
    expect(result[1].linesAdded).toBe(0);
    expect(result[1].linesRemoved).toBe(1);
    expect(result[1].linesUnchanged).toBe(2);
  });

  it('should handle file creation (new file)', () => {
    const newFileDiff = `diff --git a/src/newfile.ts b/src/newfile.ts
new file mode 100644
index 0000000..1234567
--- /dev/null
+++ b/src/newfile.ts
@@ -0,0 +1,3 @@
+export function newFunction() {
+  return 'hello';
+}`;

    const result = parseDiff(newFileDiff);
    
    expect(result).toHaveLength(1);
    expect(result[0].oldFile.fileName).toBe('src/newfile.ts');
    expect(result[0].newFile.fileName).toBe('src/newfile.ts');
    expect(result[0].oldFile.fileLang).toBe('typescript');
    expect(result[0].newFile.fileLang).toBe('typescript');
    expect(result[0].linesAdded).toBe(3);
    expect(result[0].linesRemoved).toBe(0);
    expect(result[0].linesUnchanged).toBe(0);
  });

  it('should handle file deletion', () => {
    const deletedFileDiff = `diff --git a/src/oldfile.js b/src/oldfile.js
deleted file mode 100644
index 1234567..0000000
--- a/src/oldfile.js
+++ /dev/null
@@ -1,3 +0,0 @@
-function oldFunction() {
-  return 'goodbye';
-}`;

    const result = parseDiff(deletedFileDiff);
    
    expect(result).toHaveLength(1);
    expect(result[0].oldFile.fileName).toBe('src/oldfile.js');
    expect(result[0].newFile.fileName).toBe('src/oldfile.js');
    expect(result[0].linesAdded).toBe(0);
    expect(result[0].linesRemoved).toBe(3);
    expect(result[0].linesUnchanged).toBe(0);
  });

  it('should handle file rename', () => {
    const renamedFileDiff = `diff --git a/src/oldname.js b/src/newname.js
similarity index 100%
rename from src/oldname.js
rename to src/newname.js
index 1234567..1234567 100644
--- a/src/oldname.js
+++ b/src/newname.js
@@ -1,3 +1,3 @@
 function test() {
-  return 'old';
+  return 'new';
 }`;

    const result = parseDiff(renamedFileDiff);
    
    expect(result).toHaveLength(1);
    expect(result[0].oldFile.fileName).toBe('src/oldname.js');
    expect(result[0].newFile.fileName).toBe('src/newname.js');
    expect(result[0].linesAdded).toBe(1);
    expect(result[0].linesRemoved).toBe(1);
    expect(result[0].linesUnchanged).toBe(2);
  });

  it('should handle complex diff with multiple hunks', () => {
    const complexDiff = `diff --git a/src/complex.js b/src/complex.js
index 1234567..abcdefg 100644
--- a/src/complex.js
+++ b/src/complex.js
@@ -1,5 +1,6 @@
 function first() {
+  console.log('added');
   return 1;
 }
 
 function second() {
@@ -10,8 +11,7 @@ function second() {
 }
 
 function third() {
-  const old = 'remove this';
-  const alsoOld = 'also remove';
+  const newVar = 'keep this';
   return 3;
 }`;

    const result = parseDiff(complexDiff);
    
    expect(result).toHaveLength(1);
    expect(result[0].oldFile.fileName).toBe('src/complex.js');
    expect(result[0].newFile.fileName).toBe('src/complex.js');
    expect(result[0].linesAdded).toBe(2);
    expect(result[0].linesRemoved).toBe(2);
    expect(result[0].linesUnchanged).toBe(10);
  });

  it('should handle malformed diff gracefully', () => {
    const malformedDiff = `not a real diff
just some random text
without proper headers`;

    const result = parseDiff(malformedDiff);
    
    expect(result).toHaveLength(1);
    expect(result[0].oldFile.fileName).toBe(null);
    expect(result[0].newFile.fileName).toBe(null);
    expect(result[0].oldFile.fileLang).toBe(null);
    expect(result[0].newFile.fileLang).toBe(null);
    expect(result[0].linesAdded).toBe(0);
    expect(result[0].linesRemoved).toBe(0);
    expect(result[0].linesUnchanged).toBe(0);
  });

  it('should handle diff with no changes (context only)', () => {
    const contextOnlyDiff = `diff --git a/src/unchanged.js b/src/unchanged.js
index 1234567..1234567 100644
--- a/src/unchanged.js
+++ b/src/unchanged.js
@@ -1,3 +1,3 @@
 function unchanged() {
   return 'same';
 }`;

    const result = parseDiff(contextOnlyDiff);
    
    expect(result).toHaveLength(1);
    expect(result[0].oldFile.fileName).toBe('src/unchanged.js');
    expect(result[0].newFile.fileName).toBe('src/unchanged.js');
    expect(result[0].linesAdded).toBe(0);
    expect(result[0].linesRemoved).toBe(0);
    expect(result[0].linesUnchanged).toBe(3);
  });
});