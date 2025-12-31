import type { LLMConfig } from './models'

export const capitalizeProvider = (provider: string): string => {
  if (provider === 'openai') return 'OpenAI'
  if (provider === 'anthropic') return 'Anthropic'
  if (provider === 'google') return 'Google'
  return provider.charAt(0).toUpperCase() + provider.slice(1)
}

export const getModelSummary = (config: LLMConfig): string => {
  const models: string[] = []
  const defaultModel = config.defaults?.[0]
  if (defaultModel) {
    if (defaultModel.model) {
      models.push(defaultModel.model)
    } else if (defaultModel.provider) {
      models.push(`${capitalizeProvider(defaultModel.provider)} (default)`)
    }
  }

  for (const [, configs] of Object.entries(config.useCaseConfigs || {})) {
    const ucConfig = configs?.[0]
    if (ucConfig) {
      if (ucConfig.model && !models.includes(ucConfig.model)) {
        models.push(ucConfig.model)
      } else if (ucConfig.provider && !ucConfig.model) {
        const providerDefault = `${capitalizeProvider(ucConfig.provider)} (default)`
        if (!models.includes(providerDefault)) {
          models.push(providerDefault)
        }
      }
    }
  }

  return models.length > 0 ? models.join(' + ') : 'No models configured'
}