export function StringToToolChatProvider(provider: string): string {
  switch (provider.toLowerCase()) {
    case 'openai':
    case 'anthropic':
    case 'google':
      return provider.toLowerCase();
    default:
      throw new Error(`Unknown provider: ${provider}`);
  }
}