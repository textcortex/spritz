import { describe, it, expect, beforeEach, vi } from 'vite-plus/test';
import { createMockStorage } from '@/test/helpers';
import { createTranscript, applySessionUpdate } from './acp-transcript';
import { readCachedTranscript, writeCachedTranscript, evictCachedTranscript } from './acp-cache';

describe('acp-cache', () => {
  let mockStorage: Storage;

  beforeEach(() => {
    mockStorage = createMockStorage();
    // acp-cache uses window.sessionStorage via getStorage()
    Object.defineProperty(window, 'sessionStorage', { value: mockStorage, writable: true });
    Object.defineProperty(window, 'localStorage', { value: mockStorage, writable: true });
  });

  it('returns null for missing entries', () => {
    expect(readCachedTranscript('non-existent')).toBeNull();
  });

  it('writes and reads cached transcript', () => {
    const t = createTranscript();
    applySessionUpdate(t, { sessionUpdate: 'agent_message_chunk', content: 'Hello' });

    writeCachedTranscript('conv-1', t, {
      spritzName: 'test-spritz',
      sessionId: 'sess-1',
      preview: 'Hello',
    });

    const restored = readCachedTranscript('conv-1');
    expect(restored).not.toBeNull();
    expect(restored!.messages).toHaveLength(1);
    expect(restored!.messages[0].blocks[0].text).toBe('Hello');
  });

  it('evicts cached transcript', () => {
    const t = createTranscript();
    writeCachedTranscript('conv-1', t, {
      spritzName: 'test',
      sessionId: 'sess-1',
      preview: '',
    });

    evictCachedTranscript('conv-1');
    expect(readCachedTranscript('conv-1')).toBeNull();
  });

  it('evicts oldest entries beyond CACHE_LIMIT (25)', () => {
    // Write 26 entries
    for (let i = 0; i < 26; i++) {
      const t = createTranscript();
      writeCachedTranscript(`conv-${i}`, t, {
        spritzName: `spritz-${i}`,
        sessionId: `sess-${i}`,
        preview: '',
      });
    }

    // The oldest entry (conv-0) should have been evicted from the index
    // The newest entries should still be readable
    const indexRaw = mockStorage.getItem('spritz:acp:thread-index');
    const index = JSON.parse(indexRaw!);
    expect(index.length).toBeLessThanOrEqual(25);

    // The most recent entry should still be readable
    expect(readCachedTranscript('conv-25')).not.toBeNull();
  });

  it('handles storage errors gracefully', () => {
    const brokenStorage = {
      ...createMockStorage(),
      getItem: () => { throw new Error('quota exceeded'); },
      setItem: () => { throw new Error('quota exceeded'); },
    };
    Object.defineProperty(window, 'sessionStorage', { value: brokenStorage, writable: true });
    Object.defineProperty(window, 'localStorage', { value: brokenStorage, writable: true });

    // Should not throw
    expect(readCachedTranscript('any')).toBeNull();
    expect(() => writeCachedTranscript('any', createTranscript(), {
      spritzName: 'test',
      sessionId: 'sess',
      preview: '',
    })).not.toThrow();
  });
});
