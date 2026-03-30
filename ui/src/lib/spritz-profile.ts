import type { ConversationInfo } from '@/types/acp';
import type { Spritz } from '@/types/spritz';

function normalizeText(value: unknown): string {
  if (typeof value !== 'string') {
    return '';
  }
  return value.trim();
}

export function getSpritzProfileName(spritz?: Spritz | null): string {
  if (!spritz) {
    return '';
  }
  return (
    normalizeText(spritz.status?.profile?.name) ||
    normalizeText(spritz.status?.acp?.agentInfo?.title) ||
    normalizeText(spritz.status?.acp?.agentInfo?.name) ||
    normalizeText(spritz.metadata?.name)
  );
}

export function getSpritzProfileImageUrl(spritz?: Spritz | null): string {
  return normalizeText(spritz?.status?.profile?.imageUrl);
}

export function getConversationAgentName(
  conversation?: ConversationInfo | null,
  spritz?: Spritz | null,
): string {
  return (
    getSpritzProfileName(spritz) ||
    normalizeText(conversation?.spec?.spritzName)
  );
}

export function getConversationAgentImageUrl(
  _conversation?: ConversationInfo | null,
  spritz?: Spritz | null,
): string {
  return getSpritzProfileImageUrl(spritz);
}

export function getAgentInitials(name: string): string {
  const words = name
    .split(/\s+/)
    .map((word) => word.trim())
    .filter(Boolean);
  if (words.length === 0) {
    return '?';
  }
  if (words.length === 1) {
    return words[0].slice(0, 2).toUpperCase();
  }
  return `${words[0][0] || ''}${words[1][0] || ''}`.toUpperCase();
}
