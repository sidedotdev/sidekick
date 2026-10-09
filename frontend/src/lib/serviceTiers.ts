import type { ModelConfig } from './models'
import type { ModelInfo, ModelMode, ModelsData } from './store'

export type ServiceTierField = 'serviceTier' | 'speed'

export type ServiceTierSelection = Required<Pick<ModelConfig, ServiceTierField>>

export interface ServiceTierOption {
  id: string
  label: string
  // null for the standard tier, which leaves every tier field unset
  field: ServiceTierField | null
  value: string
}

// Request body keys in models.dev modes that select a tier, mapped to the
// ModelConfig field the backend forwards as that key.
const TIER_BODY_FIELDS: Record<string, ServiceTierField> = {
  service_tier: 'serviceTier',
  speed: 'speed',
}

const TIER_FIELDS: ServiceTierField[] = ['serviceTier', 'speed']

// Slowest/cheapest first; unlisted modes sort after these
const MODE_ORDER = ['flex', 'fast', 'priority', 'ultrafast']

const STANDARD_TIER: ServiceTierOption = { id: '', label: 'Standard', field: null, value: '' }

const capitalize = (value: string): string => value.charAt(0).toUpperCase() + value.slice(1)

const optionId = (field: ServiceTierField, value: string): string => `${field}:${value}`

const modeRank = (name: string): number => {
  const index = MODE_ORDER.indexOf(name)
  return index < 0 ? MODE_ORDER.length : index
}

const findModelInfo = (modelsData: ModelsData, provider: string, model: string): ModelInfo | undefined => {
  if (!model) return undefined
  const providerInfo = modelsData[provider]
  if (providerInfo?.models) return providerInfo.models[model]
  // Unknown providers may still proxy a known model
  return Object.values(modelsData)
    .map((p) => p.models?.[model])
    .find((info) => info?.experimental?.modes)
}

// Modes that change anything beyond a single tier field (e.g. a reasoning
// mode) can't be represented by the config, so they aren't tiers.
const tierFromMode = (name: string, mode: ModelMode | undefined): ServiceTierOption | null => {
  const entries = Object.entries(mode?.provider?.body ?? {})
  if (entries.length !== 1) return null
  const [key, value] = entries[0]
  const field = TIER_BODY_FIELDS[key]
  if (!field || typeof value !== 'string' || !value) return null
  return { id: optionId(field, value), label: capitalize(name), field, value }
}

// Lists the standard tier followed by the model's tiers per models.dev. A
// tier already set in the config is always included, even if models.dev
// doesn't list it, so it stays visible and can be changed.
export const findServiceTierOptions = (
  modelsData: ModelsData,
  provider: string,
  model: string,
  current: Partial<ServiceTierSelection>,
): ServiceTierOption[] => {
  const modes = findModelInfo(modelsData, provider, model)?.experimental?.modes ?? {}
  const tiers = Object.entries(modes)
    .sort(([a], [b]) => modeRank(a) - modeRank(b) || a.localeCompare(b))
    .map(([name, mode]) => tierFromMode(name, mode))
    .filter((tier): tier is ServiceTierOption => tier !== null)

  const options = [STANDARD_TIER]
  for (const tier of tiers) {
    if (!options.some((o) => o.id === tier.id)) options.push(tier)
  }
  for (const field of TIER_FIELDS) {
    const value = current[field]
    if (value && !options.some((o) => o.id === optionId(field, value))) {
      options.push({ id: optionId(field, value), label: capitalize(value), field, value })
    }
  }
  return options
}

export const selectedServiceTierId = (
  options: ServiceTierOption[],
  current: Partial<ServiceTierSelection>,
): string => options.find((o) => o.field && current[o.field] === o.value)?.id ?? STANDARD_TIER.id

export const applyServiceTier = (option: ServiceTierOption): ServiceTierSelection => ({
  serviceTier: option.field === 'serviceTier' ? option.value : '',
  speed: option.field === 'speed' ? option.value : '',
})