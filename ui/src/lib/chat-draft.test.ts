import { describe, it, expect, beforeEach } from 'vite-plus/test';
import { createMockStorage } from '@/test/helpers';
import { readChatDraft, writeChatDraft, clearChatDraft } from './chat-draft';

describe('chat draft storage', () => {
  let mockStorage: Storage;

  beforeEach(() => {
    mockStorage = createMockStorage();
    Object.defineProperty(globalThis, 'localStorage', { value: mockStorage, writable: true });
  });

  it('reads null when no draft exists', () => {
    expect(readChatDraft('covo', 'conv-1')).toBeNull();
  });

  it('writes and restores a draft for an instance/conversation pair', () => {
    writeChatDraft('covo', 'conv-1', 'draft text');
    expect(readChatDraft('covo', 'conv-1')).toBe('draft text');
  });

  it('clears storage for whitespace-only drafts', () => {
    writeChatDraft('covo', 'conv-1', 'draft text');
    writeChatDraft('covo', 'conv-1', '   ');
    expect(readChatDraft('covo', 'conv-1')).toBeNull();
  });

  it('ignores malformed JSON safely', () => {
    localStorage.setItem('spritz:chat-drafts', '{bad json');
    expect(readChatDraft('covo', 'conv-1')).toBeNull();
    expect(localStorage.getItem('spritz:chat-drafts')).toBeNull();
  });

  it('does not cross-read another instance or conversation draft', () => {
    writeChatDraft('covo', 'conv-1', 'first');
    writeChatDraft('other', 'conv-1', 'second');
    writeChatDraft('covo', 'conv-2', 'third');

    expect(readChatDraft('covo', 'conv-1')).toBe('first');
    expect(readChatDraft('other', 'conv-1')).toBe('second');
    expect(readChatDraft('covo', 'conv-2')).toBe('third');
  });

  it('clearChatDraft removes only the requested entry', () => {
    writeChatDraft('covo', 'conv-1', 'first');
    writeChatDraft('covo', 'conv-2', 'second');

    clearChatDraft('covo', 'conv-1');

    expect(readChatDraft('covo', 'conv-1')).toBeNull();
    expect(readChatDraft('covo', 'conv-2')).toBe('second');
  });
});
