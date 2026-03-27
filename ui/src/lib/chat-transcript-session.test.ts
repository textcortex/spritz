import { describe, expect, it } from 'vite-plus/test';
import {
  applyChatTranscriptUpdate,
  createChatTranscriptSession,
  finalizeConnectedTranscriptSession,
  finalizePromptStreaming,
  noteReplayState,
} from './chat-transcript-session';

describe('chat-transcript-session', () => {
  it('marks replayed transcript updates when historical messages arrive', () => {
    const session = createChatTranscriptSession('');
    noteReplayState(session, true);

    applyChatTranscriptUpdate(session, {
      sessionUpdate: 'agent_message_chunk',
      historyMessageId: 'assistant-1',
      content: 'hello',
    }, { historical: true });

    expect(session.replaySawTranscriptUpdate).toBe(true);
  });

  it('finalizes prompt streaming in place', () => {
    const session = createChatTranscriptSession('');
    applyChatTranscriptUpdate(session, {
      sessionUpdate: 'agent_message_chunk',
      content: 'hello',
    });

    expect(session.transcript.messages[0].streaming).toBe(true);
    finalizePromptStreaming(session);
    expect(session.transcript.messages[0].streaming).toBe(false);
  });

  it('replaces stale local transcript state when canonical replay starts', () => {
    const session = createChatTranscriptSession('');
    applyChatTranscriptUpdate(session, {
      sessionUpdate: 'agent_message_chunk',
      content: 'Based on the German residence law documents...',
    });
    finalizePromptStreaming(session);

    noteReplayState(session, true);

    expect(session.transcript.messages).toEqual([]);
    expect(session.cacheHydrated).toBe(false);
    expect(session.replaySawTranscriptUpdate).toBe(false);
  });

  it('drops stale cached state when replay had no transcript-bearing updates', () => {
    const session = createChatTranscriptSession('');
    session.cacheHydrated = true;

    applyChatTranscriptUpdate(session, {
      sessionUpdate: 'available_commands_update',
      availableCommands: ['cmd'],
    }, { historical: true });

    const transcript = finalizeConnectedTranscriptSession(session);

    expect(transcript.messages).toEqual([]);
    expect(session.cacheHydrated).toBe(false);
  });
});
