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