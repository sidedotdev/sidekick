export interface ParsedDiff {
  oldFile: { fileName: string | null; fileLang: string | null };
  newFile: { fileName: string | null; fileLang: string | null };
  hunks: string[];
  linesAdded: number;
  linesRemoved: number;
  linesUnchanged: number;
  firstLineNumber: number | null;
  isRename: boolean;
}

export const getFileLanguage = (fileName: string | undefined): string | null => {
  if (!fileName) return null;
  const extension = fileName.split('.').pop();
  // Add more mappings as needed
  const languageMap: { [key: string]: string } = {
    'js': 'javascript',
    'ts': 'typescript',
    'py': 'python',
    'go': 'go',
    'vue': 'vue',
    'json': 'json',
    'md': 'markdown',
    'html': 'html',
    'css': 'css',
    'scss': 'scss',
    'yaml': 'yaml',
    'yml': 'yaml',
    'xml': 'xml',
    'sh': 'bash',
    'bash': 'bash',
    'zsh': 'bash',
    'fish': 'bash',
    'dockerfile': 'dockerfile',
    'sql': 'sql',
    'rs': 'rust',
    'cpp': 'cpp',
    'c': 'c',
    'java': 'java',
    'kt': 'kotlin',
    'swift': 'swift',
    'rb': 'ruby',
    'php': 'php',
    'cs': 'csharp',
    'fs': 'fsharp',
    'vb': 'vbnet',
    'r': 'r',
    'scala': 'scala',
    'clj': 'clojure',
    'hs': 'haskell',
    'elm': 'elm',
    'ex': 'elixir',
    'exs': 'elixir',
    'erl': 'erlang',
    'lua': 'lua',
    'pl': 'perl',
    'pm': 'perl',
    'dart': 'dart',
    'nim': 'nim',
    'zig': 'zig',
    'toml': 'toml',
    'ini': 'ini',
    'cfg': 'ini',
    'conf': 'ini',
    'properties': 'properties',
    'gitignore': 'gitignore',
    'env': 'dotenv',
    'txt': 'text',
  };
  return languageMap[extension?.toLowerCase() ?? ''] || null;
};

// Git emits combined diffs (`@@@ -a,b -c,d +e,f @@@`, one prefix column per
// parent) for unmerged paths during a merge. They carry more information than
// a two-way diff but unified-diff renderers can't display them.
export const isCombinedDiff = (hunks: string[]): boolean => {
  return hunks.some(hunk => /^@@@+ /m.test(hunk));
};

const combinedHunkHeaderRe = /^(@@+) ((?:-\d+(?:,\d+)? )+)\+(\d+)(?:,(\d+))? @@+(.*)$/;

const parseRange = (range: string): { start: number; count: number } => {
  const [start, count] = range.split(',');
  return { start: parseInt(start, 10), count: count === undefined ? 1 : parseInt(count, 10) };
};

export const combinedDiffParentCount = (diff: string): number | null => {
  for (const line of diff.split('\n')) {
    const match = combinedHunkHeaderRe.exec(line.replace(/\r$/, ''));
    if (match && match[1].length > 2) {
      return match[1].length - 1;
    }
  }
  return null;
};

const combinedDiffFilePath = (lines: string[]): string | null => {
  for (const line of lines) {
    const clean = line.replace(/\r$/, '');
    const headerMatch = clean.match(/^diff --(?:cc|combined) (.+)$/);
    if (headerMatch) return headerMatch[1];
    const newFile = extractFileFromHeader(clean, '+++ ');
    if (newFile) return newFile;
  }
  return null;
};

// Reduces a combined diff to a plain two-way diff from one parent to the
// result by reading only that parent's prefix column. Per git's semantics, a
// row carrying '-' in any column is absent from the result: it is a deletion
// if the selected column is '-' and otherwise belongs to neither side, so it
// is dropped. Only regions git recorded in the combined diff are covered, as
// `--cc` omits hunks where a single parent changed. Returns null when the
// input isn't a well-formed combined diff or its counts don't add up.
export const projectCombinedDiff = (diff: string, parentIndex: number): string | null => {
  const lines = diff.split('\n');
  const filePath = combinedDiffFilePath(lines) ?? 'file';
  const output = [`diff --git a/${filePath} b/${filePath}`, `--- a/${filePath}`, `+++ b/${filePath}`];

  let sawHunk = false;
  let i = 0;
  while (i < lines.length) {
    const header = combinedHunkHeaderRe.exec(lines[i].replace(/\r$/, ''));
    if (!header || header[1].length < 3) {
      i++;
      continue;
    }
    sawHunk = true;

    const parentCount = header[1].length - 1;
    const oldRanges = header[2].trim().split(' ').map(range => parseRange(range.slice(1)));
    if (oldRanges.length !== parentCount || parentIndex < 0 || parentIndex >= parentCount) {
      return null;
    }
    const oldRange = oldRanges[parentIndex];
    const newRange = { start: parseInt(header[3], 10), count: header[4] === undefined ? 1 : parseInt(header[4], 10) };
    const heading = header[5];

    const body: string[] = [];
    let oldCount = 0;
    let newCount = 0;
    let lastRowOmitted = false;
    i++;
    for (; i < lines.length && !/^@@+ /.test(lines[i]); i++) {
      const line = lines[i];
      if (line === '' && i === lines.length - 1) break;
      if (line.startsWith('\\')) {
        if (!lastRowOmitted) body.push(line);
        continue;
      }
      if (line.length < parentCount) return null;

      const prefix = line.slice(0, parentCount);
      const content = line.slice(parentCount);
      const marker = prefix[parentIndex];
      lastRowOmitted = false;
      if (prefix.includes('-')) {
        if (marker === '-') {
          body.push('-' + content);
          oldCount++;
        } else if (marker === ' ') {
          lastRowOmitted = true;
        } else {
          return null;
        }
      } else if (marker === '+') {
        body.push('+' + content);
        newCount++;
      } else if (marker === ' ') {
        body.push(' ' + content);
        oldCount++;
        newCount++;
      } else {
        return null;
      }
    }

    if (oldCount !== oldRange.count || newCount !== newRange.count) return null;
    if (body.length === 0) continue;
    output.push(`@@ -${oldRange.start},${oldRange.count} +${newRange.start},${newRange.count} @@${heading}`, ...body);
  }

  if (!sawHunk) return null;
  output.push('');
  return output.join('\n');
};

const parseHunkHeader = (line: string): { oldStart: number; oldCount: number; newStart: number; newCount: number } | null => {
  const match = line.match(/^@@ -(\d+)(?:,(\d+))? \+(\d+)(?:,(\d+))? @@/);
  if (!match) return null;
  
  return {
    oldStart: parseInt(match[1], 10),
    oldCount: parseInt(match[2] || '1', 10),
    newStart: parseInt(match[3], 10),
    newCount: parseInt(match[4] || '1', 10),
  };
};

const calculateLineCounts = (diffContent: string): { added: number; removed: number; unchanged: number } => {
  const lines = diffContent.split('\n');
  let added = 0;
  let removed = 0;
  let unchanged = 0;
  
  for (const line of lines) {
    // Skip metadata lines
    if (line.startsWith('diff ') || line.startsWith('index ') || 
        line.startsWith('--- ') || line.startsWith('+++ ') ||
        line.startsWith('@@')) {
      continue;
    }
    
    // Count actual diff content lines
    if (line.startsWith('+')) {
      added++;
    } else if (line.startsWith('-')) {
      removed++;
    } else if (line.startsWith(' ')) {
      // Count all context lines (including empty ones)
      unchanged++;
    }
  }
  
  return { added, removed, unchanged };
};

const extractFileFromHeader = (line: string, prefix: '--- ' | '+++ '): string | null => {
  const trimmed = line.replace(/\r$/, '');
  if (!trimmed.startsWith(prefix)) return null;
  const path = trimmed.slice(prefix.length);
  if (path === '/dev/null') return null;
  // Strip a/ or b/ prefix used by git diff
  const stripped = path.replace(/^[ab]\//, '');
  return stripped || null;
};

export const parseDiff = (diffString: string): ParsedDiff[] => {
  if (!diffString || diffString.trim() === '') {
    return [];
  }
  
  // Split by diff headers (--git, --cc, --combined), but keep the headers
  const files = diffString.split(/^(?=diff --(?:git|cc|combined))/m).filter(file => file.trim() !== '');
  
  return files.map(file => {
    const lines = file.split('\n');
    const diffHeader = lines.find(line => /^diff --(?:git|cc|combined)/.test(line));
    
    if (!diffHeader) {
      return {
        oldFile: { fileName: null, fileLang: null },
        newFile: { fileName: null, fileLang: null },
        hunks: [file],
        linesAdded: 0,
        linesRemoved: 0,
        linesUnchanged: 0,
        firstLineNumber: null,
        isRename: false,
      };
    }
    
    const cleanHeader = diffHeader.replace(/\r$/, '');
    let oldFile: string | null = null;
    let newFile: string | null = null;

    // Extract file paths from diff header
    const gitMatch = cleanHeader.match(/^diff --git a\/(.+?) b\/(.+)$/);
    if (gitMatch) {
      oldFile = gitMatch[1];
      newFile = gitMatch[2];
    } else {
      // Combined diff: "diff --cc path" or "diff --combined path"
      const combinedMatch = cleanHeader.match(/^diff --(?:cc|combined) (.+)$/);
      if (combinedMatch) {
        oldFile = combinedMatch[1];
        newFile = combinedMatch[1];
      }
    }

    // Use --- and +++ lines as authoritative source when available,
    // since they're unambiguous unlike the diff --git header
    const oldHeaderLine = lines.find(line => line.startsWith('--- '));
    const newHeaderLine = lines.find(line => line.startsWith('+++ '));
    const oldFromHeader = oldHeaderLine ? extractFileFromHeader(oldHeaderLine, '--- ') : null;
    const newFromHeader = newHeaderLine ? extractFileFromHeader(newHeaderLine, '+++ ') : null;
    if (oldFromHeader) oldFile = oldFromHeader;
    if (newFromHeader) newFile = newFromHeader;

    // Detect renames from explicit rename headers or differing old/new paths
    const renameFromLine = lines.find(line => line.startsWith('rename from '));
    const renameToLine = lines.find(line => line.startsWith('rename to '));
    if (renameFromLine) {
      oldFile = renameFromLine.replace(/\r$/, '').slice('rename from '.length);
    }
    if (renameToLine) {
      newFile = renameToLine.replace(/\r$/, '').slice('rename to '.length);
    }
    const isRename = oldFile != null && newFile != null && oldFile !== newFile
      && oldFile !== 'dev/null' && newFile !== 'dev/null';
    
    // Calculate line counts
    const { added, removed, unchanged } = calculateLineCounts(file);
    
    // Extract first line number from the first hunk header
    const firstHunkLine = lines.find(line => line.startsWith('@@'));
    const hunkHeader = firstHunkLine ? parseHunkHeader(firstHunkLine) : null;
    const firstLineNumber = hunkHeader?.newStart ?? null;
    
    return {
      oldFile: { fileName: oldFile, fileLang: getFileLanguage(oldFile || undefined) },
      newFile: { fileName: newFile, fileLang: getFileLanguage(newFile || undefined) },
      hunks: [file],
      linesAdded: added,
      linesRemoved: removed,
      linesUnchanged: unchanged,
      firstLineNumber,
      isRename,
    };
  });
};