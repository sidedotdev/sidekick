export interface ParsedDiff {
  oldFile: { fileName: string | null; fileLang: string | null };
  newFile: { fileName: string | null; fileLang: string | null };
  hunks: string[];
  linesAdded: number;
  linesRemoved: number;
  linesUnchanged: number;
  firstLineNumber: number | null;
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

export const parseDiff = (diffString: string): ParsedDiff[] => {
  if (!diffString || diffString.trim() === '') {
    return [];
  }
  
  // Split by diff headers, but keep the headers
  const files = diffString.split(/^(?=diff --git)/m).filter(file => file.trim() !== '');
  
  return files.map(file => {
    const lines = file.split('\n');
    const diffHeader = lines.find(line => line.startsWith('diff --git'));
    
    if (!diffHeader) {
      // Fallback for malformed diffs
      return {
        oldFile: { fileName: null, fileLang: null },
        newFile: { fileName: null, fileLang: null },
        hunks: [file],
        linesAdded: 0,
        linesRemoved: 0,
        linesUnchanged: 0,
        firstLineNumber: null,
      };
    }
    
    // Extract file paths from diff header
    const pathMatch = diffHeader.match(/^diff --git a\/(.+) b\/(.+)$/);
    const oldFile = pathMatch ? pathMatch[1] : null;
    const newFile = pathMatch ? pathMatch[2] : null;
    
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
    };
  });
};