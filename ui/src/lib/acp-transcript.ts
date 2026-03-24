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

/* ── HTML error detection (ported from main) ── */

function decodeHtmlEntities(text: string): string {
  return String(text || '')
    .replace(/&nbsp;/gi, ' ')
    .replace(/&amp;/gi, '&')
    .replace(/&lt;/gi, '<')
    .replace(/&gt;/gi, '>')
    .replace(/&quot;/gi, '"')
    .replace(/&#39;/gi, "'");
}

function stripHtmlTags(text: string): string {
  return decodeHtmlEntities(
    String(text || '')
      .replace(/<script[\s\S]*?<\/script>/gi, ' ')
      .replace(/<style[\s\S]*?<\/style>/gi, ' ')
      .replace(/<[^>]+>/g, ' '),
  )
    .replace(/\s+/g, ' ')
    .trim();
}

function extractHtmlTagText(html: string, tagName: string): string {
  const match = String(html || '').match(new RegExp(`<${tagName}[^>]*>([\\s\\S]*?)<\\/${tagName}>`, 'i'));
  return match ? stripHtmlTags(match[1]) : '';
}

function detectHtmlErrorDocument(text: string): { text: string; open: boolean; isError: boolean } | null {
  const raw = String(text || '').trim();
  if (!raw) return null;
  if (!/^\s*<(?:!doctype\s+html|html\b)/i.test(raw)) return null;

  const title = extractHtmlTagText(raw, 'title');
  const flattened = stripHtmlTags(raw);
  const codeMatch = flattened.match(/\berror code\s+(\d{3})\b/i) || title.match(/\b(\d{3})\b/);
  const hostMatches = [...flattened.matchAll(/\b([a-z0-9.-]+\.[a-z]{2,})\b/gi)].map((m) => m[1]);
  const host =
    hostMatches.sort((a, b) => b.length - a.length).find((v) => !/^cloudflare\.com$/i.test(v)) || '';
  const providerMatch = flattened.match(/\b(Cloudflare|Vercel|Netlify|nginx|Apache)\b/i);
  const summaryMatch =
    flattened.match(/\bThe web server reported [^.]+\./i) ||
    flattened.match(/\bThis page isn['']t working[^.]*\./i) ||
    flattened.match(/\bBad gateway\b/i);

  const parts: string[] = [];
  if (codeMatch?.[1]) parts.push(`HTTP ${codeMatch[1]}`);
  if (title) parts.push(title);
  else if (summaryMatch?.[0]) parts.push(summaryMatch[0]);
  else parts.push('HTML error response');
  if (host) parts.push(host);
  if (providerMatch?.[1]) parts.push(providerMatch[1]);

  return { text: parts.join(' · '), open: false, isError: true };
}

function normalizeToolResultText(rawOutput: unknown): { text: string; open: boolean; isError: boolean } {
  const text = stringifyDetails(rawOutput);
  if (!text) return { text: '', open: true, isError: false };
  const htmlError = detectHtmlErrorDocument(text);
  if (htmlError) return htmlError;
  return { text, open: true, isError: false };
}

/* ── Tool call upsert (ported from main) ── */

function buildToolBlocks(
  update: Record<string, unknown>,
  existing: ACPTranscript['messages'][number] | null,
) {
  const blocks: Array<{ type: 'details'; title: string; text: string; open: boolean }> = [];
  const inputText = stringifyDetails(update.rawInput);
  const normalizedResult = normalizeToolResultText(update.rawOutput);
  const resultText = normalizedResult.text;

  if (inputText) {
    blocks.push({ type: 'details', title: 'Input', text: inputText, open: false });
  } else if (existing) {
    const prior = existing.blocks.find((b) => b.type === 'details' && b.title === 'Input');
    if (prior) blocks.push({ type: 'details', title: 'Input', text: prior.text || '', open: false });
  }
  if (resultText) {
    blocks.push({ type: 'details', title: 'Result', text: resultText, open: normalizedResult.open });
  } else if (existing) {
    const prior = existing.blocks.find((b) => b.type === 'details' && b.title === 'Result');
    if (prior) blocks.push({ type: 'details', title: 'Result', text: prior.text || '', open: prior.open !== false });
  }

  return { blocks, isError: normalizedResult.isError, summary: normalizedResult.isError ? normalizedResult.text : '' };
}

function upsertToolCall(transcript: ACPTranscript, update: Record<string, unknown>) {
  const toolCallId = (update.toolCallId as string) || createId('tool');
  const existingIndex = transcript.toolCallIndex.get(toolCallId);
  const existing = existingIndex !== undefined ? transcript.messages[existingIndex] : null;
  const normalized = buildToolBlocks(update, existing);
  const title = ((update.title as string) || existing?.title || 'Tool call').replace(/^"|"$/g, '').trim();
  const status =
    normalized.isError && (!update.status || update.status === 'completed')
      ? 'failed'
      : (update.status as string) || existing?.status || 'pending';
  const tone = status === 'completed' ? 'success' : status === 'failed' ? 'danger' : 'info';
  const meta = (update.type as string) || existing?.meta || '';

  const next = {
    role: 'tool' as const,
    title,
    status,
    tone,
    meta,
    blocks: normalized.blocks,
    _toolCallId: toolCallId,
  };

  if (existing && existingIndex !== undefined) {
    transcript.messages[existingIndex] = { ...existing, ...next };
  } else {
    transcript.toolCallIndex.set(toolCallId, transcript.messages.length);
    transcript.messages.push({ ...next, streaming: false });
  }

  return normalized;
}

/* ── Tool kind classification (regex-based, matching main/staging) ── */

function classifyToolKind(name: string): 'fetch' | 'search' | 'generic' {
  const lower = (name || '').toLowerCase().replace(/[-_]/g, '');
  if (/search|query|grep|glob|find/.test(lower)) return 'search';
  if (/fetch|browse|readpage|navigate|webfetch/.test(lower)) return 'fetch';
  return 'generic';
}

function extractToolUrl(rawInput: unknown): string {
  let input = rawInput;
  if (typeof input === 'string') {
    try { input = JSON.parse(input); } catch { return (input as string).startsWith('http') ? (input as string) : ''; }
  }
  if (input && typeof input === 'object') {
    const obj = input as Record<string, unknown>;
    return String(obj.url || obj.query || obj.uri || '');
  }
  return '';
}

/* ── Streaming text helpers ── */

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
  transcript.messages.push({ role, blocks: [{ type: 'text', text: chunk }], streaming: true });
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

/* ── Main update processor ── */

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
    // Skip internal protocol messages
    if (/^\s*<(?:command-name|command-message|command-args|local-command-stdout)\b/i.test(text)) return null;
    if (/^\s*\[Request\s+(interrupted|cancelled|canceled)\b/i.test(text)) return null;

    const htmlError = detectHtmlErrorDocument(text);
    if (htmlError) return { toast: { type: 'error', message: htmlError.text } };

    // Bake historical thinking
    if (historical && transcript.thinkingChunks.length > 0) {
      bakeThinkingDone(transcript);
    }

    transcript.thinkingChunks = [];
    transcript.thinkingActive = false;
    transcript.thinkingInsertIndex = 0;
    transcript.thinkingStartTime = 0;
    transcript.thinkingElapsedSeconds = 0;

    // ACP is the source of truth for durable chat transcript messages. The
    // chat page should not append a separate local user bubble for the same
    // prompt; it should wait for this echoed user_message_chunk instead.
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
    if (/^\s*\[Request\s+(interrupted|cancelled|canceled)\b/i.test(text)) return null;

    const htmlError = detectHtmlErrorDocument(text);
    if (htmlError) return { toast: { type: 'error', message: htmlError.text } };

    if (historical) {
      appendHistoricalText(transcript, 'assistant', text, (update.historyMessageId || update.messageId) as string);
    } else {
      appendStreamingText(transcript, 'assistant', text);
    }
    return null;
  }

  if (type === 'tool_call' || type === 'tool_call_update') {
    // Start thinking session if needed
    if (type === 'tool_call') {
      if (!transcript.thinkingActive && !transcript.thinkingChunks.length) {
        transcript.thinkingInsertIndex = transcript.messages.length;
        if (!historical) transcript.thinkingStartTime = transcript.thinkingStartTime || Date.now();
      }
      if (!historical) transcript.thinkingActive = true;
    }

    // Check for errors in tool result (for toast only — don't add to messages)
    const normalizedResult = normalizeToolResultText(update.rawOutput);

    // Add to thinking chunks with full metadata (tools render inside the thinking timeline only)
    const toolCallId = (update.toolCallId as string) || createId('tool');
    const toolName = String(update.name || update.title || update.type || 'Tool call');
    const status = (update.status as string) || 'pending';
    const inputText = stringifyDetails(update.rawInput);
    const resultText = normalizedResult.text;
    const url = extractToolUrl(update.rawInput);
    const toolKind = classifyToolKind(toolName);

    // Upsert into thinkingChunks (matching main branch)
    const existingChunk = transcript.thinkingChunks.find((c) => c._toolCallId === toolCallId);
    if (existingChunk) {
      existingChunk.status = status;
      if (update.name || update.title) existingChunk.toolName = toolName;
      if (inputText) existingChunk.input = inputText;
      if (resultText) existingChunk.result = resultText;
      if (url) existingChunk.url = url;
      existingChunk.text = url || existingChunk.toolName || toolName;
    } else {
      transcript.thinkingChunks.push({
        kind: 'tool',
        toolKind,
        _toolCallId: toolCallId,
        toolName,
        status,
        text: url || toolName,
        url: url || undefined,
        input: inputText || undefined,
        result: resultText || undefined,
      });
    }

    // Return error toast if tool result had an error
    if (!historical && normalizedResult?.isError && normalizedResult.text) {
      return { toast: { type: 'error', message: normalizedResult.text } };
    }
    return null;
  }

  if (type === 'available_commands_update') {
    transcript.availableCommands = Array.isArray(update.availableCommands)
      ? (update.availableCommands as Array<string | { name: string; description?: string }>)
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
    const infoObj = (update?.sessionInfo || {}) as Record<string, unknown>;
    return { conversationTitle: (update?.title as string) || (infoObj.title as string) || '' };
  }

  if (type === 'plan') {
    const rawEntries = Array.isArray(update.entries) ? update.entries : [];
    // Normalize entries: main branch uses entry.content, not entry.text
    const entries = (rawEntries as Array<Record<string, unknown>>).map((e) => ({
      text: String(e.content || e.text || e.status || 'Pending step'),
      done: Boolean(e.done || e.status === 'completed'),
    }));
    // Skip empty plans
    if (entries.length === 0) return null;
    transcript.messages.push({
      role: 'plan',
      title: 'Plan',
      blocks: [{ type: 'plan', entries }],
    });
    return null;
  }

  if (type === 'config_option_update') {
    transcript.messages.push({
      role: 'system',
      title: 'Setting updated',
      tone: 'muted',
      blocks: [
        {
          type: 'keyValue',
          entries: [
            { label: String((update as Record<string, unknown>).key || ''), value: String((update as Record<string, unknown>).value || '') },
          ],
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

/* ── Bake thinking chunks into a thinking_done message ── */

function bakeThinkingDone(transcript: ACPTranscript) {
  const insertIdx = transcript.thinkingInsertIndex || transcript.messages.length;
  const elapsed = transcript.thinkingElapsedSeconds ||
    (transcript.thinkingStartTime ? Math.round((Date.now() - transcript.thinkingStartTime) / 1000) : 0);
  transcript.messages.splice(insertIdx, 0, {
    role: 'thinking_done',
    blocks: [],
    _thinkingChunks: [...transcript.thinkingChunks],
    _thinkingElapsedSeconds: elapsed,
  });
  rebuildToolCallIndex(transcript);
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

  if (transcript.thinkingChunks.length > 0) {
    bakeThinkingDone(transcript);
  }
  transcript.thinkingChunks = [];
  transcript.thinkingInsertIndex = 0;
  transcript.thinkingStartTime = 0;
  transcript.thinkingElapsedSeconds = 0;
}

/** Bake any leftover thinking chunks after history replay completes. */
export function finalizeHistoricalThinking(transcript: ACPTranscript): void {
  if (transcript.thinkingChunks.length > 0) {
    bakeThinkingDone(transcript);
  }
  transcript.thinkingChunks = [];
  transcript.thinkingActive = false;
  transcript.thinkingInsertIndex = 0;
  transcript.thinkingStartTime = 0;
  transcript.thinkingElapsedSeconds = 0;
}

/** Returns true if the update carries actual transcript content (messages, tools, etc.)
 *  as opposed to metadata-only updates (commands, mode, usage, session info). */
export function isTranscriptBearingUpdate(update: Record<string, unknown>): boolean {
  const type = (update?.sessionUpdate as string) || '';
  return ![
    '',
    'available_commands_update',
    'current_mode_update',
    'usage_update',
    'session_info_update',
  ].includes(type);
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
