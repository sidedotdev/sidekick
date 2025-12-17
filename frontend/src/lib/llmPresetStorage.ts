import type { LLMConfig, ModelConfig } from './models'

export const PRESETS_STORAGE_KEY = 'sidekick_model_presets'

export interface ModelPreset {
  id: string
  name: string
  config: LLMConfig
}

const modelConfigsEqual = (a: ModelConfig[], b: ModelConfig[]): boolean => {
  if (a.length !== b.length) return false
  const normalize = (c: ModelConfig) => `${c.provider}|${c.model}|${c.reasoningEffort || ''}`
  const setA = new Set(a.map(normalize))
  const setB = new Set(b.map(normalize))
  if (setA.size !== setB.size) return false
  for (const item of setA) {
    if (!setB.has(item)) return false
  }
  return true
}

export const llmConfigsEqual = (a: LLMConfig, b: LLMConfig): boolean => {
  if (!modelConfigsEqual(a.defaults || [], b.defaults || [])) return false

  const keysA = Object.keys(a.useCaseConfigs || {}).sort()
  const keysB = Object.keys(b.useCaseConfigs || {}).sort()
  if (keysA.length !== keysB.length) return false
  if (!keysA.every((k, i) => k === keysB[i])) return false

  for (const key of keysA) {
    if (!modelConfigsEqual(a.useCaseConfigs[key] || [], b.useCaseConfigs[key] || [])) {
      return false
    }
  }
  return true
}

let cachedPresets: ModelPreset[] | null = null
let cacheTimestamp = 0
const CACHE_TTL_MS = 1000

export const loadPresets = (): ModelPreset[] => {
  const now = Date.now()
  if (cachedPresets !== null && now - cacheTimestamp < CACHE_TTL_MS) {
    return cachedPresets
  }
  try {
    const stored = localStorage.getItem(PRESETS_STORAGE_KEY)
    const presets: ModelPreset[] = stored ? JSON.parse(stored) : []
    cachedPresets = presets
    cacheTimestamp = now
    return presets
  } catch {
    const presets: ModelPreset[] = []
    cachedPresets = presets
    cacheTimestamp = now
    return presets
  }
}

export const invalidatePresetsCache = () => {
  cachedPresets = null
  cacheTimestamp = 0
}

export const savePresets = (presets: ModelPreset[]) => {
  localStorage.setItem(PRESETS_STORAGE_KEY, JSON.stringify(presets))
  invalidatePresetsCache()
}