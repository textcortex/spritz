import type { ACPTranscript, ThinkingChunk } from '@/types/acp';
import { extractACPText } from './acp-client';

function createId(prefix: string): string {
  return `${prefix}-${Date.now()}-${Math.random().toString(36).slice(2, 8)}`;
}

export function createTranscript(): ACPTranscript {
  return {
    messages: [],
    toolCallIndex: new Map(),
    availableCommands: [],
    currentMode: '',
    usage: null,
    thinkingChunks: [],
    thinkingActive: false,
    thinkingInsertIndex: 0,
    thinkingStartTime: 0,
    thinkingElapsedSeconds: 0,
  };
}

function rebuildToolCallIndex(transcript: ACPTranscript) {
  transcript.toolCallIndex = new Map();
  transcript.messages.forEach((message, index) => {
    if (message.role === 'tool' && message._toolCallId) {
      transcript.toolCallIndex.set(message._toolCallId, index);
    }
  });
}

function stringifyDetails(value: unknown): string {
  if (value === undefined || value === null || value === '') return '';
  if (typeof value === 'string') return value;
  try {
    return JSON.stringify(value, null, 2);
  } catch {
    return String(value);
  }
}

function humanizeUpdateType(type: string): string {
  return String(type || 'Update')
    .replace(/_/g, ' ')
    .replace(/\b\w/g, (match) => match.toUpperCase());
}

function appendStreamingText(transcript: ACPTranscript, role: 'user' | 'assistant', text: string) {
  const chunk = String(text || '');
  if (!chunk) return;
  const last = transcript.messages[transcript.messages.length - 1];
  if (last && last.role === role && last.streaming) {
    const textBlock = last.blocks.find((b) => b.type === 'text');
    if (textBlock) {
      textBlock.text = (textBlock.text || '') + chunk;
    } else {
      last.blocks.push({ type: 'text', text: chunk });
    }
    return;
  }
  transcript.messages.push({
    role,
    blocks: [{ type: 'text', text: chunk }],
    streaming: true,
  });
}

function appendHistoricalText(
  transcript: ACPTranscript,
  role: 'user' | 'assistant',
  text: string,
  messageKey: string = '',
) {
  const value = String(text || '');
  if (!value) return;
  const normalizedKey = String(messageKey || '').trim();
  const last = transcript.messages[transcript.messages.length - 1];
  if (normalizedKey && last && last.role === role && last._toolCallId === normalizedKey) {
    const textBlock = last.blocks.find((b) => b.type === 'text');
    if (textBlock) {
      textBlock.text = (textBlock.text || '') + value;
    } else {
      last.blocks.push({ type: 'text', text: value });
    }
    return;
  }
  transcript.messages.push({
    role,
    blocks: [{ type: 'text', text: value }],
    streaming: false,
    _toolCallId: normalizedKey,
  });
}

interface UpdateResult {
  toast?: { type: string; message: string };
  conversationTitle?: string;
}

export function applySessionUpdate(
  transcript: ACPTranscript,
  update: Record<string, unknown>,
  options: { historical?: boolean } = {},
): UpdateResult | null {
  const type = (update?.sessionUpdate as string) || 'unknown';
  const historical = Boolean(options.historical);

  if (type === 'user_message_chunk') {
    const text = extractACPText(update.content);
    if (/^\s*<(?:command-name|command-message|command-args|local-command-stdout)\b/i.test(text)) return null;

    // Bake historical thinking
    if (historical && transcript.thinkingChunks.length > 0) {
      const hadWebTools = transcript.thinkingChunks.some((c) => c.kind === 'tool' && c.toolKind);
      if (hadWebTools) {
        const insertIdx = transcript.thinkingInsertIndex || transcript.messages.length;
        transcript.messages.splice(insertIdx, 0, {
          role: 'thinking_done',
          blocks: [],
          _toolCallId: undefined,
        });
        rebuildToolCallIndex(transcript);
      }
    }

    transcript.thinkingChunks = [];
    transcript.thinkingActive = false;
    transcript.thinkingInsertIndex = 0;
    transcript.thinkingStartTime = 0;
    transcript.thinkingElapsedSeconds = 0;

    if (historical) {
      appendHistoricalText(transcript, 'user', text, (update.historyMessageId || update.messageId) as string);
    } else {
      appendStreamingText(transcript, 'user', text);
    }
    return null;
  }

  if (type === 'agent_thought_chunk') {
    const text = extractACPText(update.content);
    if (!text) return null;
    if (!transcript.thinkingActive && !transcript.thinkingChunks.length) {
      transcript.thinkingInsertIndex = transcript.messages.length;
      if (!historical) transcript.thinkingStartTime = Date.now();
    }
    if (!historical) transcript.thinkingActive = true;
    const last = transcript.thinkingChunks[transcript.thinkingChunks.length - 1];
    if (last && last.kind === 'thought') {
      last.text += text;
    } else {
      transcript.thinkingChunks.push({ kind: 'thought', text });
    }
    return null;
  }

  if (type === 'agent_message_chunk') {
    const text = extractACPText(update.content);
    if (/^\s*<(?:command-name|command-message|command-args|local-command-stdout)\b/i.test(text)) return null;
    if (historical) {
      appendHistoricalText(transcript, 'assistant', text, (update.historyMessageId || update.messageId) as string);
    } else {
      appendStreamingText(transcript, 'assistant', text);
    }
    return null;
  }

  if (type === 'tool_call' || type === 'tool_call_update') {
    const toolCallId = (update.toolCallId as string) || '';
    if (type === 'tool_call') {
      if (!transcript.thinkingActive && !transcript.thinkingChunks.length) {
        transcript.thinkingInsertIndex = transcript.messages.length;
        if (!historical) transcript.thinkingStartTime = transcript.thinkingStartTime || Date.now();
      }
      if (!historical) transcript.thinkingActive = true;
      const meta = update._meta as Record<string, Record<string, string>> | undefined;
      const realToolName = (meta?.claudeCode?.toolName || '').toLowerCase();
      const isSearch = realToolName === 'websearch';
      const isFetch = realToolName === 'webfetch';
      const toolKind = isSearch ? 'search' : isFetch ? 'fetch' : undefined;
      const title = ((update.title as string) || '').replace(/^"|"$/g, '').trim();
      transcript.thinkingChunks.push({
        kind: 'tool',
        text: title || realToolName,
        toolKind,
        _toolCallId: toolCallId,
      });

      // Also add as tool message
      const resultText = stringifyDetails(update.rawOutput);
      const inputText = stringifyDetails(update.rawInput);
      transcript.messages.push({
        role: 'tool',
        title: (update.title as string) || 'Tool call',
        status: (update.status as string) || 'pending',
        blocks: [
          ...(inputText ? [{ type: 'details' as const, title: 'Input', text: inputText }] : []),
          ...(resultText ? [{ type: 'details' as const, title: 'Result', text: resultText }] : []),
        ],
        _toolCallId: toolCallId,
      });
      transcript.toolCallIndex.set(toolCallId, transcript.messages.length - 1);
    }
    if (type === 'tool_call_update' && toolCallId) {
      const existingIdx = transcript.toolCallIndex.get(toolCallId);
      if (existingIdx !== undefined) {
        const existing = transcript.messages[existingIdx];
        if (update.title) existing.title = (update.title as string).replace(/^"|"$/g, '').trim();
        if (update.status) existing.status = update.status as string;
        const resultText = stringifyDetails(update.rawOutput);
        if (resultText) {
          const resultBlock = existing.blocks.find((b) => b.title === 'Result');
          if (resultBlock) {
            resultBlock.text = resultText;
          } else {
            existing.blocks.push({ type: 'details', title: 'Result', text: resultText });
          }
        }
      }
    }
    return null;
  }

  if (type === 'available_commands_update') {
    transcript.availableCommands = Array.isArray(update.availableCommands)
      ? (update.availableCommands as string[])
      : [];
    return null;
  }

  if (type === 'current_mode_update') {
    transcript.currentMode = String(update.mode || update.currentMode || '').trim();
    return null;
  }

  if (type === 'usage_update') {
    const used = typeof update.used === 'number' ? update.used : 0;
    const size = typeof update.size === 'number' ? update.size : 0;
    transcript.usage = { label: String(update.label || 'Usage'), used, size };
    return null;
  }

  if (type === 'session_info_update') {
    return {
      conversationTitle: (update?.title as string) || '',
    };
  }

  if (type === 'plan') {
    transcript.messages.push({
      role: 'plan',
      title: 'Plan',
      blocks: [
        {
          type: 'plan',
          entries: Array.isArray(update.entries)
            ? (update.entries as Array<{ text: string; done?: boolean }>)
            : [],
        },
      ],
    });
    return null;
  }

  // Silent types
  if (['heartbeat', 'ping', 'pong', 'ack'].includes(type)) return null;

  // Unknown update: show as system message
  transcript.messages.push({
    role: 'system',
    title: humanizeUpdateType(type),
    blocks: [{ type: 'details', title: 'Payload', text: stringifyDetails(update) }],
  });
  return null;
}

export function finalizeStreaming(transcript: ACPTranscript): void {
  transcript.messages.forEach((message) => {
    if (message.role === 'assistant' || message.role === 'user') {
      message.streaming = false;
    }
  });
  if (transcript.thinkingActive && transcript.thinkingStartTime) {
    transcript.thinkingElapsedSeconds = Math.round((Date.now() - transcript.thinkingStartTime) / 1000);
  }
  transcript.thinkingActive = false;

  const hasWebTools = transcript.thinkingChunks.some((c) => c.kind === 'tool' && c.toolKind);
  if (transcript.thinkingChunks.length > 0 && hasWebTools) {
    const insertIdx = transcript.thinkingInsertIndex || transcript.messages.length;
    transcript.messages.splice(insertIdx, 0, {
      role: 'thinking_done',
      blocks: [],
    });
    rebuildToolCallIndex(transcript);
  }
  transcript.thinkingChunks = [];
}

export function getPreviewText(transcript: ACPTranscript): string {
  const messages = transcript.messages;
  for (let i = messages.length - 1; i >= 0; i--) {
    const msg = messages[i];
    if (msg.role === 'assistant' || msg.role === 'user') {
      const textBlock = msg.blocks.find((b) => b.type === 'text');
      if (textBlock?.text) {
        const normalized = textBlock.text.replace(/\s+/g, ' ').trim();
        return normalized.length > 120 ? normalized.slice(0, 119) + '…' : normalized;
      }
    }
  }
  return '';
}

export function serializeTranscript(transcript: ACPTranscript): Record<string, unknown> {
  return {
    messages: transcript.messages.map((m) => ({
      ...m,
      blocks: m.blocks.map((b) => {
        const copy = { ...b };
        delete copy._renderedLength;
        return copy;
      }),
    })),
    availableCommands: transcript.availableCommands,
    currentMode: transcript.currentMode,
    usage: transcript.usage,
  };
}

export function hydrateTranscript(payload: Record<string, unknown>): ACPTranscript {
  const transcript = createTranscript();
  if (Array.isArray(payload?.messages)) {
    transcript.messages = payload.messages as ACPTranscript['messages'];
  }
  if (Array.isArray(payload?.availableCommands)) {
    transcript.availableCommands = payload.availableCommands as string[];
  }
  if (typeof payload?.currentMode === 'string') {
    transcript.currentMode = payload.currentMode;
  }
  rebuildToolCallIndex(transcript);
  return transcript;
}
