import type { ACPTranscript } from '@/types/acp';
import {
  applySessionUpdate,
  createTranscript,
  finalizeHistoricalThinking,
  finalizeStreaming,
  getPreviewText,
  isTranscriptBearingUpdate,
} from './acp-transcript';
import { evictCachedTranscript, readCachedTranscript, writeCachedTranscript } from './acp-cache';

export interface ChatTranscriptSession {
  transcript: ACPTranscript;
  cacheHydrated: boolean;
  replaySawTranscriptUpdate: boolean;
}

type ChatTranscriptUpdateResult = ReturnType<typeof applySessionUpdate>;

/**
 * Initializes transcript state from cache for a conversation, if any exists.
 */
export function createChatTranscriptSession(conversationId: string): ChatTranscriptSession {
  const transcript = conversationId ? (readCachedTranscript(conversationId) || createTranscript()) : createTranscript();
  return {
    transcript,
    cacheHydrated: transcript.messages.length > 0,
    replaySawTranscriptUpdate: false,
  };
}

/**
 * Clears stale cached transcript state after ACP reports that the session was replaced.
 */
export function replaceChatTranscriptSession(conversationId: string): ChatTranscriptSession {
  evictCachedTranscript(conversationId);
  return {
    transcript: createTranscript(),
    cacheHydrated: false,
    replaySawTranscriptUpdate: false,
  };
}

/**
 * Resets replay bookkeeping when ACP begins replaying historical updates.
 */
export function noteReplayState(session: ChatTranscriptSession, replaying: boolean): void {
  if (replaying) {
    session.replaySawTranscriptUpdate = false;
  }
}

/**
 * Applies one ACP update and records whether replay delivered transcript-bearing history.
 */
export function applyChatTranscriptUpdate(
  session: ChatTranscriptSession,
  update: Record<string, unknown>,
  options: { historical?: boolean } = {},
): ChatTranscriptUpdateResult {
  if (options.historical && isTranscriptBearingUpdate(update)) {
    session.replaySawTranscriptUpdate = true;
  }
  return applySessionUpdate(session.transcript, update, options);
}

/**
 * Finalizes in-flight streaming markers after a prompt finishes.
 */
export function finalizePromptStreaming(session: ChatTranscriptSession): ACPTranscript {
  finalizeStreaming(session.transcript);
  return session.transcript;
}

/**
 * Finalizes replay state once the socket is connected and durable history is available.
 */
export function finalizeConnectedTranscriptSession(session: ChatTranscriptSession): ACPTranscript {
  if (session.cacheHydrated && !session.replaySawTranscriptUpdate) {
    session.transcript = createTranscript();
  }
  finalizeHistoricalThinking(session.transcript);
  session.cacheHydrated = false;
  return session.transcript;
}

/**
 * Persists the latest transcript snapshot and preview for the active conversation session.
 */
export function persistChatTranscriptSession(
  session: ChatTranscriptSession,
  options: { conversationId: string; spritzName: string; sessionId: string },
): void {
  writeCachedTranscript(options.conversationId, session.transcript, {
    spritzName: options.spritzName,
    sessionId: options.sessionId,
    preview: getPreviewText(session.transcript),
  });
}
