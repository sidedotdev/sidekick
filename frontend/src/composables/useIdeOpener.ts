import { ref, type InjectionKey, type Ref } from 'vue'

export type IdeType = 'vscode' | 'intellij'

const SESSION_STORAGE_KEY = 'sidekick-preferred-ide'

export interface IdeOpener {
  showIdeSelector: Ref<boolean>
  pendingFilePath: Ref<string | null>
  openInIde: (absoluteFilePath: string) => void
  selectIde: (ide: IdeType) => void
  cancelIdeSelection: () => void
}

export const IDE_OPENER_KEY: InjectionKey<(relativePath: string) => void> = Symbol('ideOpener')

function openFileInIde(absoluteFilePath: string, ide: IdeType): void {
  let url: string
  if (ide === 'vscode') {
    url = `vscode://file/${absoluteFilePath}?windowId=_blank`
  } else {
    url = `idea://open?file=${encodeURIComponent(absoluteFilePath)}`
  }
  window.open(url, '_self')
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

  function openInIde(absoluteFilePath: string): void {
    const storedIde = getStoredIdePreference()
    if (storedIde) {
      openFileInIde(absoluteFilePath, storedIde)
    } else {
      pendingFilePath.value = absoluteFilePath
      showIdeSelector.value = true
    }
  }

  function selectIde(ide: IdeType): void {
    storeIdePreference(ide)
    if (pendingFilePath.value) {
      openFileInIde(pendingFilePath.value, ide)
    }
    pendingFilePath.value = null
    showIdeSelector.value = false
  }

  function cancelIdeSelection(): void {
    pendingFilePath.value = null
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