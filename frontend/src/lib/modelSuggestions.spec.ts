import { describe, it, expect } from 'vitest'
import { completeModel, modelCandidates, searchModels } from './modelSuggestions'
import type { ModelsData } from './store'

const modelsData: ModelsData = {
  anthropic: {
    models: {
      'claude-opus-4-8': { release_date: '2026-01-20' },
      'claude-opus-5': { release_date: '2026-05-01' },
      'claude-opus-5-5': { release_date: '2026-08-12' },
      'claude-sonnet-5': { release_date: '2026-05-01' },
      'claude-legacy': {},
    },
  },
  openai: {
    models: {
      'gpt-6-astra': { release_date: '2026-09-04' },
      'gpt-5': { release_date: '2025-08' },
    },
  },
  proxy: {
    models: {
      'gpt-6-astra': { release_date: '2026-09-05' },
    },
  },
}

describe('modelCandidates', () => {
  it('orders the provider models newest release first, undated last', () => {
    expect(modelCandidates(modelsData, 'anthropic')).toEqual([
      'claude-opus-5-5',
      'claude-opus-5',
      'claude-sonnet-5',
      'claude-opus-4-8',
      'claude-legacy',
    ])
  })

  it('offers every provider model once, newest first, for an unknown provider', () => {
    const candidates = modelCandidates(modelsData, 'unknown')
    expect(candidates.slice(0, 3)).toEqual(['gpt-6-astra', 'claude-opus-5-5', 'claude-opus-5'])
    expect(candidates.filter((id) => id === 'gpt-6-astra')).toHaveLength(1)
    expect(candidates.at(-1)).toBe('claude-legacy')
  })
})

describe('searchModels', () => {
  it('matches anywhere in the id, case-insensitively, newest first', () => {
    expect(searchModels(modelsData, 'anthropic', 'OPUS')).toEqual(['claude-opus-5-5', 'claude-opus-5', 'claude-opus-4-8'])
  })
})

describe('completeModel', () => {
  it.each([
    ['claude-o', 'claude-opus-5-5'],
    ['claude-opus-5', 'claude-opus-5-5'],
    ['claude-opus-4', 'claude-opus-4-8'],
    ['CLAUDE-S', 'claude-sonnet-5'],
    ['opus', undefined],
    ['', undefined],
  ])('completes %j to %j', (typed, expected) => {
    expect(completeModel(modelsData, 'anthropic', typed)).toBe(expected)
  })
})