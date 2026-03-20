const CONVERSATION_TITLE_MAX_LENGTH = 80;
const DEFAULT_CONVERSATION_TITLE = 'New conversation';

export function buildFallbackConversationTitle(prompt?: string | null): string {
  const normalized = String(prompt || '').trim().replace(/\s+/g, ' ');
  if (!normalized) return '';
  return normalized.slice(0, CONVERSATION_TITLE_MAX_LENGTH).trim();
}

export function hasDurableConversationTitle(title?: string | null): boolean {
  const normalized = String(title || '').trim();
  return normalized !== '' && normalized !== DEFAULT_CONVERSATION_TITLE;
}
