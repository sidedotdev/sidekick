import type { CriteriaFulfillment } from './models'

export function parseCriteriaFulfillment(response: any): CriteriaFulfillment | null {
  const calls = Array.isArray(response?.output?.content)
    ? response.output.content
      .filter((block: any) => block?.type === 'tool_use')
      .map((block: any) => block.toolUse)
    : response?.toolCalls
  if (!Array.isArray(calls)) return null

  for (const call of calls) {
    // Older persisted responses may not include the tool name.
    if (!call || (call.name && call.name !== 'determine_criteria_fulfillment')) continue
    if (typeof call.arguments !== 'string') continue
    try {
      const result = JSON.parse(call.arguments)
      if (
        !result || typeof result !== 'object' || Array.isArray(result) ||
        typeof result.analysis !== 'string' ||
        typeof result.isFulfilled !== 'boolean' ||
        (result.confidence != null &&
          (typeof result.confidence !== 'number' || !Number.isFinite(result.confidence))) ||
        (result.feedbackMessage != null && typeof result.feedbackMessage !== 'string')
      ) continue
      return { ...result, confidence: result.confidence ?? undefined }
    } catch {
      // Incomplete streamed arguments are not yet a verdict.
    }
  }
  return null
}