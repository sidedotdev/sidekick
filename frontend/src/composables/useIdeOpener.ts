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

function openFileInIde(absoluteFilePath: string, ide: IdeType, lineNumber?: number | null, baseDir?: string): void {
  const lineFragment = lineNumber ? `:${lineNumber}` : ''
  if (ide === 'vscode') {
    if (baseDir) {
      window.open(`vscode://file/${baseDir}?windowId=_blank`, '_self')
      setTimeout(() => {
        window.open(`vscode://file/${absoluteFilePath}${lineFragment}`, '_self')
      }, 250)
    } else {
      window.open(`vscode://file/${absoluteFilePath}${lineFragment}?windowId=_blank`, '_self')
    }
  } else {
    const url = `idea://open?file=${encodeURIComponent(absoluteFilePath)}${lineFragment}`
    window.open(url, '_self')
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