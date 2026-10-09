import type { ModelsData } from './store'

/**
 * Model ids offered for the provider, newest release first. When the provider
 * isn't in models.dev, every provider's models are offered, since it may
 * proxy any of them.
 */
export const modelCandidates = (modelsData: ModelsData, provider: string): string[] => {
  const providers = modelsData[provider] ? [modelsData[provider]] : Object.values(modelsData)
  const releaseDates = new Map<string, string>()
  for (const { models } of providers) {
    for (const [id, info] of Object.entries(models ?? {})) {
      const releaseDate = info.release_date ?? ''
      if (releaseDate >= (releaseDates.get(id) ?? '')) releaseDates.set(id, releaseDate)
    }
  }
  // models.dev dates are YYYY-MM-DD (occasionally YYYY-MM), so they order lexically
  return [...releaseDates.keys()].sort(
    (a, b) => releaseDates.get(b)!.localeCompare(releaseDates.get(a)!) || a.localeCompare(b),
  )
}

export const searchModels = (modelsData: ModelsData, provider: string, query: string): string[] => {
  const needle = query.toLowerCase()
  return modelCandidates(modelsData, provider).filter((id) => id.toLowerCase().includes(needle))
}

/** The newest model whose id starts with the typed text, for inline completion. */
export const completeModel = (modelsData: ModelsData, provider: string, typed: string): string | undefined => {
  if (!typed) return undefined
  const prefix = typed.toLowerCase()
  return modelCandidates(modelsData, provider).find((id) => id.toLowerCase().startsWith(prefix))
}