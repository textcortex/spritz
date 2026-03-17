import type { ACPTranscript } from '@/types/acp';
import { serializeTranscript, hydrateTranscript } from './acp-transcript';

const CACHE_LIMIT = 25;
const INDEX_KEY = 'spritz:acp:thread-index';
const THREAD_PREFIX = 'spritz:acp:thread:';

function getStorage(): Storage {
  return window.sessionStorage || window.localStorage;
}

interface CacheEntry {
  conversationId: string;
  spritzName: string;
  sessionId: string;
  updatedAt: string;
  preview: string;
}

function readIndex(): CacheEntry[] {
  try {
    const raw = getStorage().getItem(INDEX_KEY);
    return raw ? JSON.parse(raw) : [];
  } catch {
    return [];
  }
}

function writeIndex(entries: CacheEntry[]): void {
  try {
    getStorage().setItem(INDEX_KEY, JSON.stringify(entries.slice(0, CACHE_LIMIT)));
  } catch {
    // ignore
  }
}

export function readCachedTranscript(conversationId: string): ACPTranscript | null {
  try {
    const raw = getStorage().getItem(THREAD_PREFIX + conversationId);
    if (!raw) return null;
    return hydrateTranscript(JSON.parse(raw));
  } catch {
    return null;
  }
}

export function writeCachedTranscript(
  conversationId: string,
  transcript: ACPTranscript,
  meta: { spritzName: string; sessionId: string; preview: string },
): void {
  try {
    const storage = getStorage();
    storage.setItem(THREAD_PREFIX + conversationId, JSON.stringify(serializeTranscript(transcript)));

    const entries = readIndex().filter((e) => e.conversationId !== conversationId);
    entries.unshift({
      conversationId,
      spritzName: meta.spritzName,
      sessionId: meta.sessionId,
      updatedAt: new Date().toISOString(),
      preview: meta.preview,
    });

    // Evict oldest beyond limit
    while (entries.length > CACHE_LIMIT) {
      const removed = entries.pop();
      if (removed) {
        try {
          storage.removeItem(THREAD_PREFIX + removed.conversationId);
        } catch {
          // ignore
        }
      }
    }
    writeIndex(entries);
  } catch {
    // ignore
  }
}

export function evictCachedTranscript(conversationId: string): void {
  try {
    getStorage().removeItem(THREAD_PREFIX + conversationId);
    const entries = readIndex().filter((e) => e.conversationId !== conversationId);
    writeIndex(entries);
  } catch {
    // ignore
  }
}
