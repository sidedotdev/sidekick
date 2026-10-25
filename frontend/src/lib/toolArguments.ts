/** Parses complete tool-call arguments, wrapping unparseable text so it can still be rendered. */
export function parseLlm2ToolArguments(args: string): object {
  try {
    return JSON.parse(args) as object
  } catch {
    return { raw: args }
  }
}