import { describe, it, expect } from 'vitest'
import {
  applyServiceTier,
  findServiceTierOptions,
  keepOfferedTier,
  nonStandardTierLabel,
  requiresUsageCredits,
  selectedServiceTierId,
} from './serviceTiers'
import type { ModelsData } from './store'

const modelsData: ModelsData = {
  openai: {
    models: {
      'gpt-6': {
        experimental: {
          modes: {
            ultrafast: { provider: { body: { service_tier: 'ultrafast' } } },
            pro: { provider: { body: { reasoning: { mode: 'pro' } } } },
            fast: { provider: { body: { service_tier: 'priority' } } },
            flex: { provider: { body: { service_tier: 'flex' } } },
          },
        },
      },
      'gpt-4': {},
    },
  },
  anthropic: {
    models: {
      'claude-opus-5': {
        experimental: {
          modes: {
            fast: { provider: { body: { speed: 'fast' }, headers: { 'anthropic-beta': 'fast-mode' } } },
          },
        },
      },
    },
  },
}

describe('findServiceTierOptions', () => {
  it('lists standard first then tiers ordered slowest to fastest, ignoring non-tier modes', () => {
    const options = findServiceTierOptions(modelsData, 'openai', 'gpt-6', {})
    expect(options.map((o) => [o.label, o.field, o.value])).toEqual([
      ['Standard', null, ''],
      ['Flex', 'serviceTier', 'flex'],
      ['Fast', 'serviceTier', 'priority'],
      ['Ultrafast', 'serviceTier', 'ultrafast'],
    ])
  })

  it('maps anthropic fast mode to the speed field', () => {
    const options = findServiceTierOptions(modelsData, 'anthropic', 'claude-opus-5', {})
    expect(options.map((o) => [o.label, o.field, o.value])).toEqual([
      ['Standard', null, ''],
      ['Fast', 'speed', 'fast'],
    ])
  })

  it('offers only standard for models without tiers or unknown models', () => {
    expect(findServiceTierOptions(modelsData, 'openai', 'gpt-4', {})).toHaveLength(1)
    expect(findServiceTierOptions(modelsData, 'openai', 'nope', {})).toHaveLength(1)
    expect(findServiceTierOptions(modelsData, 'google', 'gemini', {})).toHaveLength(1)
  })

  it('falls back to any provider listing the model when the provider is unknown to models.dev', () => {
    const options = findServiceTierOptions(modelsData, 'my-openai-proxy', 'gpt-6', {})
    expect(options.map((o) => o.label)).toEqual(['Standard', 'Flex', 'Fast', 'Ultrafast'])
  })

  it('keeps a configured tier visible even when the model does not list it', () => {
    const options = findServiceTierOptions(modelsData, 'anthropic', 'claude-opus-4-6', { speed: 'fast' })
    expect(options.map((o) => [o.label, o.field, o.value])).toEqual([
      ['Standard', null, ''],
      ['Fast', 'speed', 'fast'],
    ])
  })
})

describe('selectedServiceTierId / applyServiceTier', () => {
  it('round-trips each option through the config fields', () => {
    const options = findServiceTierOptions(modelsData, 'openai', 'gpt-6', {})
    for (const option of options) {
      const selection = applyServiceTier(option)
      expect(selectedServiceTierId(options, selection)).toBe(option.id)
    }
  })

  it('clears the other tier field when switching tiers', () => {
    const [standard, fast] = findServiceTierOptions(modelsData, 'anthropic', 'claude-opus-5', {})
    expect(applyServiceTier(fast)).toEqual({ serviceTier: '', speed: 'fast' })
    expect(applyServiceTier(standard)).toEqual({ serviceTier: '', speed: '' })
  })
})

describe('keepOfferedTier', () => {
  const ultrafast = { serviceTier: 'ultrafast', speed: '' }
  const fast = { serviceTier: '', speed: 'fast' }
  const standard = { serviceTier: '', speed: '' }

  it.each([
    ['keeps a tier the model offers', 'openai', 'gpt-6', ultrafast, ultrafast],
    ['keeps anthropic fast mode on a model that offers it', 'anthropic', 'claude-opus-5', fast, fast],
    ['drops a tier from another provider', 'anthropic', 'claude-opus-5', ultrafast, standard],
    ['drops a tier the model does not list', 'openai', 'gpt-4', ultrafast, standard],
    ['drops a tier when the model is unknown', 'openai', 'not-a-model', ultrafast, standard],
    ['keeps standard', 'openai', 'gpt-6', standard, standard],
  ])('%s', (_name, provider, model, tier, expected) => {
    expect(keepOfferedTier(modelsData, provider, model, tier)).toEqual(expected)
  })

  it('keeps the tier until models data is available', () => {
    expect(keepOfferedTier({}, 'anthropic', 'claude-opus-5', ultrafast)).toEqual(ultrafast)
  })
})

describe('nonStandardTierLabel', () => {
  it.each([
    [{}, ''],
    [{ serviceTier: '', speed: '' }, ''],
    [{ serviceTier: 'default' }, ''],
    [{ serviceTier: 'auto' }, ''],
    [{ serviceTier: 'standard_only' }, ''],
    [{ serviceTier: 'ultrafast' }, 'Ultrafast'],
    [{ serviceTier: 'priority' }, 'Priority'],
    [{ speed: 'fast' }, 'Fast'],
    [{ serviceTier: 'priority', speed: 'fast' }, 'Priority, Fast'],
  ])('labels %j as %j', (tier, expected) => {
    expect(nonStandardTierLabel(tier)).toBe(expected)
  })
})

describe('requiresUsageCredits', () => {
  it.each([
    ['anthropic', { serviceTier: '', speed: 'fast' }, true],
    ['anthropic', { serviceTier: '', speed: '' }, false],
    ['openai', { serviceTier: 'ultrafast', speed: '' }, false],
    ['openai', { serviceTier: '', speed: 'fast' }, false],
  ])('%s with %j -> %s', (provider, tier, expected) => {
    expect(requiresUsageCredits(provider, tier)).toBe(expected)
  })
})