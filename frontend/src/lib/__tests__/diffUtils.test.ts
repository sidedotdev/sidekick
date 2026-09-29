import { describe, it, expect } from 'vitest';
import { parseDiff, getFileLanguage, isCombinedDiff, combinedDiffParentCount, projectCombinedDiff } from '../diffUtils';

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

  it('should extract file names from --- and +++ headers when diff --git parsing fails', () => {
    const diffWithWeirdHeader = `diff --git a/some weird header
--- a/src/real-file.ts
+++ b/src/real-file.ts
@@ -1,3 +1,4 @@
 line1
+added
 line2
 line3`;

    const result = parseDiff(diffWithWeirdHeader);
    
    expect(result).toHaveLength(1);
    expect(result[0].newFile.fileName).toBe('src/real-file.ts');
    expect(result[0].oldFile.fileName).toBe('src/real-file.ts');
  });

  it('should handle \\r\\n line endings', () => {
    const diffWithCRLF = "diff --git a/src/test.js b/src/test.js\r\nindex 1234567..abcdefg 100644\r\n--- a/src/test.js\r\n+++ b/src/test.js\r\n@@ -1,2 +1,3 @@\r\n line1\r\n+added\r\n line2\r\n";

    const result = parseDiff(diffWithCRLF);
    
    expect(result).toHaveLength(1);
    expect(result[0].oldFile.fileName).toBe('src/test.js');
    expect(result[0].newFile.fileName).toBe('src/test.js');
  });

  it('should handle new file with /dev/null old path', () => {
    const newFileDiff = `diff --git a/src/new.ts b/src/new.ts
new file mode 100644
index 0000000..1234567
--- /dev/null
+++ b/src/new.ts
@@ -0,0 +1,2 @@
+line1
+line2`;

    const result = parseDiff(newFileDiff);
    
    expect(result).toHaveLength(1);
    // oldFile retains the path from the diff --git header since --- /dev/null
    // doesn't provide a usable override
    expect(result[0].oldFile.fileName).toBe('src/new.ts');
    expect(result[0].newFile.fileName).toBe('src/new.ts');
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

  describe('isCombinedDiff', () => {
    it('detects hunks with combined (multi-parent) headers', () => {
      expect(isCombinedDiff(['diff --cc a.txt\n@@@ -1,2 -1,2 +1,3 @@@\n +ours\n+ theirs\n  shared'])).toBe(true);
      expect(isCombinedDiff(['diff --combined a.txt\n@@@@ -1 -1 -1 +1,2 @@@@\n+++x'])).toBe(true);
    });

    it('does not flag two-way hunks, even in a diff --cc file or with @@@ inside content', () => {
      expect(isCombinedDiff(['diff --cc a.txt\n@@ -1,2 +1,3 @@\n ours\n+theirs'])).toBe(false);
      expect(isCombinedDiff(['diff --git a/a.txt b/a.txt\n@@ -1 +1 @@\n-x\n+@@@ not a header'])).toBe(false);
      expect(isCombinedDiff([])).toBe(false);
    });
  });

  describe('projectCombinedDiff', () => {
    // Real merge-conflict resolution diff: `- ` is a line only parent 1 had,
    // ` -` a line only parent 2 had, `++` a line neither had, ` +` a line
    // parent 1 already had, `+ ` a line parent 2 already had.
    const combinedDiff = [
      'diff --cc Justfile',
      'index 07a3b20,2a11cd1..0000000',
      '--- a/Justfile',
      '+++ b/Justfile',
      '@@@ -7,5 -7,6 +7,9 @@@ install',
      '  release *args:',
      '  \t./scripts/release.sh {{args}}',
      '  ',
      ' +publish-web *args:',
      '- \t./scripts/publish-web.sh {{args}}',
      '++\t./scripts/publish-web.sh {{args}}',
      '++',
      '+ # Publish placeholder',
      '+ publish-node-placeholder *args:',
      ' -\tcd node && npm publish {{args}}',
      '++\tcd node && npm publish {{args}}',
      '',
    ].join('\n');

    it('reports the number of parents from the hunk header', () => {
      expect(combinedDiffParentCount(combinedDiff)).toBe(2);
      expect(combinedDiffParentCount('diff --cc a\n@@@@ -1 -1 -1 +1,2 @@@@\n+++x')).toBe(3);
      expect(combinedDiffParentCount('diff --git a/a b/a\n@@ -1 +1 @@\n-x\n+y')).toBe(null);
    });

    it('projects onto parent 1, dropping rows that only parent 2 removed', () => {
      expect(projectCombinedDiff(combinedDiff, 0)).toBe([
        'diff --git a/Justfile b/Justfile',
        '--- a/Justfile',
        '+++ b/Justfile',
        '@@ -7,5 +7,9 @@ install',
        ' release *args:',
        ' \t./scripts/release.sh {{args}}',
        ' ',
        ' publish-web *args:',
        '-\t./scripts/publish-web.sh {{args}}',
        '+\t./scripts/publish-web.sh {{args}}',
        '+',
        '+# Publish placeholder',
        '+publish-node-placeholder *args:',
        '+\tcd node && npm publish {{args}}',
        '',
      ].join('\n'));
    });

    it('projects onto parent 2, dropping rows that only parent 1 removed', () => {
      expect(projectCombinedDiff(combinedDiff, 1)).toBe([
        'diff --git a/Justfile b/Justfile',
        '--- a/Justfile',
        '+++ b/Justfile',
        '@@ -7,6 +7,9 @@ install',
        ' release *args:',
        ' \t./scripts/release.sh {{args}}',
        ' ',
        '+publish-web *args:',
        '+\t./scripts/publish-web.sh {{args}}',
        '+',
        ' # Publish placeholder',
        ' publish-node-placeholder *args:',
        '-\tcd node && npm publish {{args}}',
        '+\tcd node && npm publish {{args}}',
        '',
      ].join('\n'));
    });

    it('handles multiple hunks, three parents and no-newline markers', () => {
      const diff = [
        'diff --combined a.txt',
        '@@@@ -1,2 -1,1 -1,2 +1,2 @@@@',
        '   shared',
        '- - gone',
        '+++new',
        '@@@@ -10,1 -9,1 -10,1 +9,1 @@@@',
        '---last',
        '\\ No newline at end of file',
        '+++LAST',
        '\\ No newline at end of file',
        '',
      ].join('\n');
      expect(projectCombinedDiff(diff, 1)).toBe([
        'diff --git a/a.txt b/a.txt',
        '--- a/a.txt',
        '+++ b/a.txt',
        '@@ -1,1 +1,2 @@',
        ' shared',
        '+new',
        '@@ -9,1 +9,1 @@',
        '-last',
        '\\ No newline at end of file',
        '+LAST',
        '\\ No newline at end of file',
        '',
      ].join('\n'));
    });

    it('omits hunks that are empty from the selected parent\'s point of view', () => {
      const diff = [
        'diff --cc a.txt',
        '@@@ -1,0 -1,1 +1,0 @@@',
        ' -only parent 2 had this',
        '@@@ -5,1 -6,1 +5,1 @@@',
        '- x',
        '++y',
        '',
      ].join('\n');
      expect(projectCombinedDiff(diff, 0)).toBe([
        'diff --git a/a.txt b/a.txt',
        '--- a/a.txt',
        '+++ b/a.txt',
        '@@ -5,1 +5,1 @@',
        '-x',
        '+y',
        '',
      ].join('\n'));
    });

    it('returns null for parents out of range, malformed rows or inconsistent counts', () => {
      expect(projectCombinedDiff(combinedDiff, 2)).toBe(null);
      expect(projectCombinedDiff('diff --cc a\n@@@ -1,1 -1,1 +1,1 @@@\n?!x\n', 0)).toBe(null);
      expect(projectCombinedDiff('diff --cc a\n@@@ -1,1 -1,1 +1,2 @@@\n  x\n', 0)).toBe(null);
      expect(projectCombinedDiff('diff --git a/a b/a\n@@ -1 +1 @@\n-x\n+y\n', 0)).toBe(null);
    });
  });

  it('should handle combined diff format (diff --cc)', () => {
    const combinedDiff = `diff --cc scripts/lint_chat_history_append/main.go
index 32293236,12d21e3c..00000000
--- a/scripts/lint_chat_history_append/main.go
+++ b/scripts/lint_chat_history_append/main.go
@@ -1,5 +1,6 @@
 package main
 
+import "fmt"
 func main() {
   return
 }`;

    const result = parseDiff(combinedDiff);

    expect(result).toHaveLength(1);
    expect(result[0].oldFile.fileName).toBe('scripts/lint_chat_history_append/main.go');
    expect(result[0].newFile.fileName).toBe('scripts/lint_chat_history_append/main.go');
    expect(result[0].oldFile.fileLang).toBe('go');
    expect(result[0].linesAdded).toBe(1);
    expect(result[0].isRename).toBe(false);
  });

  it('should handle combined diff without --- +++ headers', () => {
    const combinedDiff = `diff --cc scripts/main.go
index 32293236,12d21e3c..00000000
@@ -1,3 +1,4 @@
 package main
+import "fmt"
 func main() {
 }`;

    const result = parseDiff(combinedDiff);

    expect(result).toHaveLength(1);
    expect(result[0].oldFile.fileName).toBe('scripts/main.go');
    expect(result[0].newFile.fileName).toBe('scripts/main.go');
    expect(result[0].isRename).toBe(false);
  });

  it('should detect rename and set isRename', () => {
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
    expect(result[0].isRename).toBe(true);
  });

  it('should set isRename false when old and new paths are the same', () => {
    const sameFileDiff = `diff --git a/src/test.js b/src/test.js
index 1234567..abcdefg 100644
--- a/src/test.js
+++ b/src/test.js
@@ -1,2 +1,3 @@
 line1
+added
 line2`;

    const result = parseDiff(sameFileDiff);

    expect(result).toHaveLength(1);
    expect(result[0].isRename).toBe(false);
  });

  it('should not treat new file as rename when diff --git header has dev/null', () => {
    const newFileDiff = `diff --git a/dev/null b/src/newfile.ts
new file mode 100644
index 0000000..1234567
--- /dev/null
+++ b/src/newfile.ts
@@ -0,0 +1,2 @@
+line1
+line2`;

    const result = parseDiff(newFileDiff);

    expect(result).toHaveLength(1);
    expect(result[0].newFile.fileName).toBe('src/newfile.ts');
    expect(result[0].isRename).toBe(false);
  });

  it('should not treat deleted file as rename when diff --git header has dev/null', () => {
    const deletedFileDiff = `diff --git a/src/oldfile.js b/dev/null
deleted file mode 100644
index 1234567..0000000
--- a/src/oldfile.js
+++ /dev/null
@@ -1,2 +0,0 @@
-line1
-line2`;

    const result = parseDiff(deletedFileDiff);

    expect(result).toHaveLength(1);
    expect(result[0].oldFile.fileName).toBe('src/oldfile.js');
    expect(result[0].isRename).toBe(false);
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