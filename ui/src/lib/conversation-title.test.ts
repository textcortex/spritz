import { describe, expect, it } from 'vite-plus/test';
import { buildFallbackConversationTitle, hasDurableConversationTitle } from '@/lib/conversation-title';

describe('conversation title helpers', () => {
  it('builds a normalized fallback title from the first prompt', () => {
    expect(buildFallbackConversationTitle('  Help me   debug the failing login test  ')).toBe(
      'Help me debug the failing login test',
    );
  });

  it('caps fallback titles to the UI limit', () => {
    expect(buildFallbackConversationTitle('x'.repeat(120))).toHaveLength(80);
  });

  it('returns an empty title for undefined prompt input', () => {
    expect(buildFallbackConversationTitle(undefined)).toBe('');
  });

  it('treats the placeholder title as non-durable', () => {
    expect(hasDurableConversationTitle('New conversation')).toBe(false);
    expect(hasDurableConversationTitle('')).toBe(false);
  });

  it('treats generated or fallback titles as durable after first send', () => {
    expect(hasDurableConversationTitle('Fix flaky login test')).toBe(true);
  });
});
