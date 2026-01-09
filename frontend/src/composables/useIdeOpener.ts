import { ref, type InjectionKey, type Ref } from 'vue'

export type IdeType = 'vscode' | 'intellij'

const SESSION_STORAGE_KEY = 'sidekick-preferred-ide'

export interface IdeOpener {
  showIdeSelector: Ref<boolean>
  pendingFilePath: Ref<string | null>
  openInIde: (absoluteFilePath: string, lineNumber?: number | null, baseDir?: string) => void
  selectIde: (ide: IdeType) => void
  cancelIdeSelection: () => void
}

export const IDE_OPENER_KEY: InjectionKey<(relativePath: string, lineNumber?: number | null, baseDir?: string) => void> = Symbol('ideOpener')

async function openFileInIde(absoluteFilePath: string, ide: IdeType, lineNumber?: number | null, baseDir?: string): Promise<void> {
  const payload: { ide: IdeType; filePath: string; line?: number; baseDir?: string } = {
    ide,
    filePath: absoluteFilePath,
  }
  if (lineNumber) {
    payload.line = lineNumber
  }
  if (baseDir) {
    payload.baseDir = baseDir
  }

  const response = await fetch('/api/v1/open-in-ide', {
    method: 'POST',
    headers: {
      'Content-Type': 'application/json',
    },
    body: JSON.stringify(payload),
  })

  if (!response.ok) {
    console.error('Failed to open file in IDE:', await response.text())
  }
}

function getStoredIdePreference(): IdeType | null {
  const stored = sessionStorage.getItem(SESSION_STORAGE_KEY)
  if (stored === 'vscode' || stored === 'intellij') {
    return stored
  }
  return null
}

function storeIdePreference(ide: IdeType): void {
  sessionStorage.setItem(SESSION_STORAGE_KEY, ide)
}

export function useIdeOpener(): IdeOpener {
  const showIdeSelector = ref(false)
  const pendingFilePath = ref<string | null>(null)
  const pendingLineNumber = ref<number | null>(null)
  const pendingBaseDir = ref<string | null>(null)

  function openInIde(absoluteFilePath: string, lineNumber?: number | null, baseDir?: string): void {
    const storedIde = getStoredIdePreference()
    if (storedIde) {
      openFileInIde(absoluteFilePath, storedIde, lineNumber, baseDir)
    } else {
      pendingFilePath.value = absoluteFilePath
      pendingLineNumber.value = lineNumber ?? null
      pendingBaseDir.value = baseDir ?? null
      showIdeSelector.value = true
    }
  }

  function selectIde(ide: IdeType): void {
    storeIdePreference(ide)
    if (pendingFilePath.value) {
      openFileInIde(pendingFilePath.value, ide, pendingLineNumber.value, pendingBaseDir.value ?? undefined)
    }
    pendingFilePath.value = null
    pendingLineNumber.value = null
    pendingBaseDir.value = null
    showIdeSelector.value = false
  }

  function cancelIdeSelection(): void {
    pendingFilePath.value = null
    pendingLineNumber.value = null
    pendingBaseDir.value = null
    showIdeSelector.value = false
  }

  return {
    showIdeSelector,
    pendingFilePath,
    openInIde,
    selectIde,
    cancelIdeSelection,
  }
}