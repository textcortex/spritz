#!/usr/bin/env node

import { randomUUID } from "node:crypto";
import fs from "node:fs";
import path from "node:path";
import { createRequire } from "node:module";
import { Readable, Writable } from "node:stream";
import { pathToFileURL } from "node:url";

const GATEWAY_CLIENT_NAMES = {
  CLI: "cli",
  CONTROL_UI: "openclaw-control-ui",
};

const GATEWAY_CLIENT_MODES = {
  CLI: "cli",
  WEBCHAT: "webchat",
};

const TRUTHY_VALUES = new Set(["1", "true", "yes", "on"]);
const DEFAULT_OPENCLAW_PACKAGE_ROOT = "/usr/local/lib/node_modules/openclaw";
const DEFAULT_FALLBACK_AGENT_ID = "main";
const DEFAULT_FALLBACK_SESSION_PREFIX = "spritz-acp";
const DEFAULT_ACP_PROTOCOL_VERSION = 1;
const DEFAULT_LIVE_TOOL_TRANSCRIPT_SYNC_INTERVAL_MS = 400;
const DEFAULT_OPENCLAW_ACP_AGENT_INFO = {
  name: "openclaw-acp",
  title: "OpenClaw ACP Gateway",
};
const SILENT_REPLY_TOKEN = "NO_REPLY";
const SUPPRESSED_LEADING_ASSISTANT_TAG_NAMES = ["think", "thinking"];
const UUIDISH_SESSION_ID_PATTERN =
  /^[0-9a-f]{8}-[0-9a-f]{4}-[1-8][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/i;

type ParsedArgs = {
  defaultSessionKey?: string;
  defaultSessionLabel?: string;
  gatewayPassword?: string;
  gatewayToken?: string;
  gatewayUrl?: string;
  help?: boolean;
  prefixCwd?: boolean;
  provenanceMode?: string;
  requireExistingSession?: boolean;
  resetSession?: boolean;
  verbose?: boolean;
};

type PromptTranscriptSyncState = {
  transcriptMessageCursor?: number;
  transcriptSyncDisabled?: boolean;
};

type PromptToolTranscriptSyncHooks = {
  clearInterval?: typeof globalThis.clearInterval;
  setInterval?: typeof globalThis.setInterval;
  toolTranscriptSyncIntervalMs?: number;
};

type LazyGatewayHooks = {
  onStop?: () => void;
  waitUntilReady?: () => Promise<void> | void;
};

type GatewayAgentClassHooks = PromptToolTranscriptSyncHooks & {
  ensureGatewayReady?: () => Promise<void> | void;
};

type GatewayClientOptionsParams = {
  connectionUrl: string;
  gatewayPassword?: string;
  gatewayToken?: string;
  trustedProxyControlUi?: boolean;
};

/**
 * Returns whether the image-owned ACP adapter should impersonate a trusted-proxy
 * Control UI client instead of the normal CLI ACP client.
 */
export function useTrustedProxyControlUiBridge(env = process.env) {
  const raw = env.SPRITZ_OPENCLAW_ACP_USE_CONTROL_UI_BRIDGE;
  if (typeof raw !== "string") {
    return false;
  }
  return TRUTHY_VALUES.has(raw.trim().toLowerCase());
}

function normalizeBridgeToken(value, fallback) {
  if (typeof value !== "string") {
    return fallback;
  }
  const normalized = value
    .trim()
    .toLowerCase()
    .replace(/[^a-z0-9_-]+/g, "-")
    .replace(/^-+|-+$/g, "");
  return normalized || fallback;
}

function normalizeHistoryContent(content) {
  if (Array.isArray(content)) {
    return content.filter((item) => item && typeof item === "object");
  }
  if (typeof content === "string" && content.trim()) {
    return [{ type: "text", text: content }];
  }
  return [];
}

const silentExactRegexByToken = new Map();
const silentTrailingRegexByToken = new Map();
const silentLeadingRegexByToken = new Map();
const silentLeadingAttachedRegexByToken = new Map();
const leadingSuppressedAssistantBlockRegexByTagSet = new Map();
const leadingSuppressedAssistantTagPrefixesByTagSet = new Map();

/**
 * Returns whether text is exactly the OpenClaw silent reply token.
 */
function isSilentReplyText(text, token = SILENT_REPLY_TOKEN) {
  if (!text) {
    return false;
  }
  let regex = silentExactRegexByToken.get(token);
  if (!regex) {
    regex = new RegExp(`^\\s*${token.replace(/[.*+?^${}()|[\]\\]/g, "\\$&")}\\s*$`, "i");
    silentExactRegexByToken.set(token, regex);
  }
  return regex.test(text);
}

/**
 * Removes a trailing silent token from mixed-content OpenClaw assistant text.
 */
function stripSilentToken(text, token = SILENT_REPLY_TOKEN) {
  let regex = silentTrailingRegexByToken.get(token);
  if (!regex) {
    regex = new RegExp(
      `(?:^|\\s+|\\*+)${token.replace(/[.*+?^${}()|[\]\\]/g, "\\$&")}\\s*$`,
      "i",
    );
    silentTrailingRegexByToken.set(token, regex);
  }
  return text.replace(regex, "").trim();
}

/**
 * Returns whether text starts with a glued leading silent token like
 * `NO_REPLYActual answer`.
 */
function startsWithSilentToken(text, token = SILENT_REPLY_TOKEN) {
  if (!text) {
    return false;
  }
  let regex = silentLeadingAttachedRegexByToken.get(token);
  if (!regex) {
    regex = new RegExp(
      `^\\s*(?:${token.replace(/[.*+?^${}()|[\]\\]/g, "\\$&")}\\s+)*${token.replace(/[.*+?^${}()|[\]\\]/g, "\\$&")}(?=[\\p{L}\\p{N}])`,
      "iu",
    );
    silentLeadingAttachedRegexByToken.set(token, regex);
  }
  return regex.test(text);
}

/**
 * Removes one or more leading silent tokens from assistant text.
 */
function stripLeadingSilentToken(text, token = SILENT_REPLY_TOKEN) {
  let regex = silentLeadingRegexByToken.get(token);
  if (!regex) {
    regex = new RegExp(`^(?:\\s*${token.replace(/[.*+?^${}()|[\]\\]/g, "\\$&")})+\\s*`, "i");
    silentLeadingRegexByToken.set(token, regex);
  }
  return text.replace(regex, "").trim();
}

/**
 * Returns whether text is an uppercase lead fragment of `NO_REPLY` during
 * streaming, for example `NO`, `NO_`, or `NO_RE`.
 */
function isSilentReplyPrefixText(text, token = SILENT_REPLY_TOKEN) {
  if (!text) {
    return false;
  }
  const trimmed = text.trimStart();
  if (!trimmed || trimmed !== trimmed.toUpperCase()) {
    return false;
  }
  const normalized = trimmed.toUpperCase();
  if (normalized.length < 2 || /[^A-Z_]/.test(normalized)) {
    return false;
  }
  const tokenUpper = token.toUpperCase();
  if (!tokenUpper.startsWith(normalized)) {
    return false;
  }
  if (normalized.includes("_")) {
    return true;
  }
  return tokenUpper === SILENT_REPLY_TOKEN && normalized === "NO";
}

function buildSuppressedAssistantTagKey(tagNames = SUPPRESSED_LEADING_ASSISTANT_TAG_NAMES) {
  return tagNames.join("|").toLowerCase();
}

function getLeadingSuppressedAssistantBlockRegex(
  tagNames = SUPPRESSED_LEADING_ASSISTANT_TAG_NAMES,
) {
  const key = buildSuppressedAssistantTagKey(tagNames);
  let regex = leadingSuppressedAssistantBlockRegexByTagSet.get(key);
  if (!regex) {
    const tagPattern = tagNames.map((tag) => tag.replace(/[.*+?^${}()|[\]\\]/g, "\\$&")).join("|");
    regex = new RegExp(
      `^(?:\\s*<(?:${tagPattern})\\b[^>]*>[\\s\\S]*?<\\/(?:${tagPattern})>\\s*)+`,
      "iu",
    );
    leadingSuppressedAssistantBlockRegexByTagSet.set(key, regex);
  }
  return regex;
}

function stripLeadingSuppressedAssistantBlocks(
  text,
  tagNames = SUPPRESSED_LEADING_ASSISTANT_TAG_NAMES,
) {
  return text.replace(getLeadingSuppressedAssistantBlockRegex(tagNames), "").trim();
}

function isSuppressedAssistantTagPrefixText(
  text,
  tagNames = SUPPRESSED_LEADING_ASSISTANT_TAG_NAMES,
) {
  if (!text) {
    return false;
  }
  const normalized = text.trimStart().toLowerCase();
  if (!normalized) {
    return false;
  }
  const key = buildSuppressedAssistantTagKey(tagNames);
  let prefixes = leadingSuppressedAssistantTagPrefixesByTagSet.get(key);
  if (!prefixes) {
    prefixes = new Set(["<", "</"]);
    for (const tagName of tagNames) {
      for (const tagPrefix of [`<${tagName}`, `</${tagName}`]) {
        for (let index = 1; index <= tagPrefix.length; index += 1) {
          prefixes.add(tagPrefix.slice(0, index));
        }
        prefixes.add(`${tagPrefix}>`);
      }
    }
    leadingSuppressedAssistantTagPrefixesByTagSet.set(key, prefixes);
  }
  return prefixes.has(normalized);
}

/**
 * Normalizes OpenClaw assistant text so silent control tokens never become
 * ACP-visible assistant output.
 */
function normalizeAssistantTextForAcp(text, { suppressLeadFragments = false } = {}) {
  if (typeof text !== "string") {
    return "";
  }
  let normalized = text.trim();
  if (!normalized) {
    return "";
  }
  if (isSilentReplyText(normalized)) {
    return "";
  }
  if (
    suppressLeadFragments &&
    (isSilentReplyPrefixText(normalized) ||
      isSuppressedAssistantTagPrefixText(normalized))
  ) {
    return "";
  }
  normalized = stripLeadingSuppressedAssistantBlocks(normalized);
  if (!normalized) {
    return "";
  }
  if (startsWithSilentToken(normalized)) {
    normalized = stripLeadingSilentToken(normalized);
  }
  if (normalized.toUpperCase().includes(SILENT_REPLY_TOKEN)) {
    normalized = stripSilentToken(normalized);
  }
  return normalized.trim();
}

/**
 * Sanitizes assistant content blocks before they are replayed or streamed over
 * ACP so OpenClaw silent control tokens stay internal to the wrapper.
 */
function sanitizeAssistantContentForAcp(content, { suppressLeadFragments = false } = {}) {
  const sanitized = [];
  for (const item of normalizeHistoryContent(content)) {
    const type = normalizeContentItemType(item);
    if (type !== "text") {
      sanitized.push(item);
      continue;
    }
    const sourceText =
      typeof item.text === "string"
        ? item.text
        : typeof item.content === "string"
          ? item.content
          : "";
    const normalizedText = normalizeAssistantTextForAcp(sourceText, {
      suppressLeadFragments,
    });
    if (!normalizedText) {
      continue;
    }
    sanitized.push({
      ...item,
      ...(typeof item.text === "string" ? { text: normalizedText } : {}),
      ...(typeof item.content === "string" ? { content: normalizedText } : {}),
      ...(typeof item.text !== "string" && typeof item.content !== "string"
        ? { text: normalizedText }
        : {}),
    });
  }
  return sanitized;
}

function readTextFromHistoryContent(content) {
  return content
    .map((item) => {
      if (typeof item.text === "string" && item.text.trim()) {
        return item.text;
      }
      if (typeof item.content === "string" && item.content.trim()) {
        return item.content;
      }
      return "";
    })
    .filter(Boolean)
    .join("\n")
    .trim();
}

function stripInjectedUserMessageEnvelope(text) {
  if (typeof text !== "string") {
    return "";
  }

  let normalized = text.trim();
  normalized = normalized.replace(
    /^Sender \(untrusted metadata\):\n```json\n[\s\S]*?\n```\n\n/u,
    "",
  );
  normalized = normalized.replace(/^\[[^\n]*Working directory:[^\n]*\]\n\n/u, "");
  normalized = normalized.replace(/^\[Working directory:[^\n]*\]\n\n/u, "");
  return normalized.trim();
}

function extractVisibleUserHistoryText(content) {
  return stripInjectedUserMessageEnvelope(readTextFromHistoryContent(content));
}

function extractPromptText(prompt) {
  if (!Array.isArray(prompt)) {
    return "";
  }

  return prompt
    .map((item) => {
      if (!item || typeof item !== "object" || Array.isArray(item)) {
        return "";
      }
      return typeof item.text === "string" ? item.text.trim() : "";
    })
    .filter(Boolean)
    .join("\n")
    .trim();
}

/**
 * Resolves the pending ACP prompt for a gateway event. Tool events can carry a
 * gateway-engine run ID that differs from the client-generated prompt
 * idempotency key, so we fall back to the sole prompt sharing the session key.
 */
export function findPendingPromptBySessionKey(pendingPrompts, sessionKey, runId) {
  if (!(pendingPrompts instanceof Map)) {
    return undefined;
  }

  let sessionMatch;
  let sessionMatchCount = 0;

  for (const pending of pendingPrompts.values()) {
    if (!pending || pending.sessionKey !== sessionKey) {
      continue;
    }

    sessionMatchCount += 1;
    if (!runId || pending.idempotencyKey === runId) {
      return pending;
    }
    if (!sessionMatch) {
      sessionMatch = pending;
    }
  }

  if (runId && sessionMatchCount === 1) {
    return sessionMatch;
  }

  return undefined;
}

function normalizeContentItemType(item) {
  return typeof item?.type === "string" ? item.type.toLowerCase() : "";
}

function isToolCallContentItem(item) {
  return ["toolcall", "tool_call", "tooluse", "tool_use"].includes(
    normalizeContentItemType(item),
  );
}

function buildHistoryToolCallUpdate(item) {
  const toolCallId =
    (typeof item.id === "string" && item.id.trim()) ||
    (typeof item.toolCallId === "string" && item.toolCallId.trim()) ||
    (typeof item.tool_call_id === "string" && item.tool_call_id.trim()) ||
    "";
  if (!toolCallId) {
    return null;
  }
  const toolName =
    (typeof item.name === "string" && item.name.trim()) ||
    (typeof item.toolName === "string" && item.toolName.trim()) ||
    (typeof item.tool_name === "string" && item.tool_name.trim()) ||
    "tool";
  const rawInput =
    item.arguments ??
    item.args ??
    item.input ??
    item.rawInput ??
    undefined;
  return {
    sessionUpdate: "tool_call",
    toolCallId,
    title: `${toolName}`,
    status: "completed",
    rawInput,
    type: toolName,
  };
}

function buildHistoryToolResultUpdate(message, content) {
  const toolCallId =
    (typeof message.toolCallId === "string" && message.toolCallId.trim()) ||
    (typeof message.tool_call_id === "string" && message.tool_call_id.trim()) ||
    (typeof message.id === "string" && message.id.trim()) ||
    "";
  if (!toolCallId) {
    return null;
  }
  const rawOutput = readTextFromHistoryContent(content) || message.result || message.output || "";
  return {
    sessionUpdate: "tool_call_update",
    toolCallId,
    status: message.is_error || message.isError ? "failed" : "completed",
    rawOutput,
  };
}

function ensurePendingToolLifecycleState(pending) {
  if (!pending) {
    return {
      toolCalls: new Map(),
      startedToolCallIds: new Set(),
      completedToolCallIds: new Set(),
    };
  }

  if (!(pending.toolCalls instanceof Map)) {
    pending.toolCalls = new Map();
  }
  if (!(pending.startedToolCallIds instanceof Set)) {
    pending.startedToolCallIds = new Set();
  }
  if (!(pending.completedToolCallIds instanceof Set)) {
    pending.completedToolCallIds = new Set();
  }

  return {
    toolCalls: pending.toolCalls,
    startedToolCallIds: pending.startedToolCallIds,
    completedToolCallIds: pending.completedToolCallIds,
  };
}

function rememberObservedToolLifecycleEvent(pending, payload) {
  const stream = payload?.stream;
  const phase = payload?.data?.phase;
  const toolCallId = payload?.data?.toolCallId;
  if (!pending || stream !== "tool" || !toolCallId) {
    return;
  }

  const { toolCalls, startedToolCallIds, completedToolCallIds } =
    ensurePendingToolLifecycleState(pending);

  if (phase === "start") {
    startedToolCallIds.add(toolCallId);
    if (!toolCalls.has(toolCallId)) {
      const toolName =
        (typeof payload?.data?.name === "string" && payload.data.name.trim()) || "tool";
      toolCalls.set(toolCallId, {
        title: `${toolName}`,
        rawInput: payload?.data?.args,
        type: toolName,
      });
    }
    return;
  }

  if (phase === "result") {
    startedToolCallIds.add(toolCallId);
    completedToolCallIds.add(toolCallId);
    toolCalls.delete(toolCallId);
  }
}

/**
 * Emits live tool_call updates from assistant content blocks when the ACP
 * bundle only streams text/thinking and leaves tool blocks to transcript
 * replay. This keeps the UI in sync before a refresh.
 */
export async function emitLiveToolCallContentUpdates(agent, sessionId, pending, messageData) {
  if (!pending) {
    return;
  }

  const content = normalizeHistoryContent(messageData?.content);
  if (content.length === 0) {
    return;
  }

  const { toolCalls, startedToolCallIds, completedToolCallIds } =
    ensurePendingToolLifecycleState(pending);

  for (const item of content) {
    if (!isToolCallContentItem(item)) {
      continue;
    }

    const update = buildHistoryToolCallUpdate(item);
    if (
      !update ||
      startedToolCallIds.has(update.toolCallId) ||
      completedToolCallIds.has(update.toolCallId)
    ) {
      continue;
    }

    startedToolCallIds.add(update.toolCallId);
    toolCalls.set(update.toolCallId, {
      title: update.title,
      rawInput: update.rawInput,
      type: update.type,
    });

    await agent.connection.sessionUpdate({
      sessionId,
      update: {
        ...update,
        status: "in_progress",
      },
    });
  }
}

/**
 * Marks any live tool calls as completed using the persisted gateway transcript
 * before the prompt finishes. Refresh already rebuilds this from history; this
 * closes the gap for the live stream.
 */
export async function syncPendingToolCallTranscriptUpdates(agent, sessionId, pending) {
  if (
    !pending?.sessionKey ||
    !agent?.gateway?.request ||
    pending.transcriptSyncDisabled ||
    !Number.isInteger(pending.transcriptMessageCursor)
  ) {
    return;
  }

  if (pending.transcriptToolSyncPromise) {
    return await pending.transcriptToolSyncPromise;
  }

  const syncPromise = (async () => {
    let transcript;
    try {
      transcript = await agent.gateway.request("sessions.get", {
        key: pending.sessionKey,
        limit: 1000,
      });
    } catch (error) {
      if (hasMissingOperatorReadScope(error)) {
        pending.transcriptSyncDisabled = true;
        agent.log?.(
          `syncPendingToolCallTranscriptUpdates: skipping transcript fetch for ${sessionId}; gateway transcript read requires operator.read`,
        );
        return;
      }
      throw error;
    }

    const transcriptMessages = Array.isArray(transcript?.messages) ? transcript.messages : [];
    const baselineCursor = Math.max(0, pending.transcriptMessageCursor ?? 0);
    const nextCursor = transcriptMessages.length;
    const candidateMessages =
      baselineCursor >= nextCursor
        ? []
        : transcriptMessages.slice(Math.min(baselineCursor, nextCursor));
    pending.transcriptMessageCursor = nextCursor;

    const { toolCalls, startedToolCallIds, completedToolCallIds } =
      ensurePendingToolLifecycleState(pending);
    const transcriptToolCalls = [];
    const transcriptToolResults = [];

    for (const rawMessage of candidateMessages) {
      if (!rawMessage || typeof rawMessage !== "object") {
        continue;
      }

      const role = typeof rawMessage.role === "string" ? rawMessage.role.toLowerCase() : "";
      const content = normalizeHistoryContent(rawMessage.content);

      if (role === "assistant") {
        for (const item of content) {
          if (!isToolCallContentItem(item)) {
            continue;
          }
          const update = buildHistoryToolCallUpdate(item);
          if (!update) {
            continue;
          }
          transcriptToolCalls.push(update);
        }
        continue;
      }

      if (["toolresult", "tool_result", "tool"].includes(role)) {
        const update = buildHistoryToolResultUpdate(rawMessage, content);
        if (update) {
          transcriptToolResults.push(update);
        }
      }
    }

    for (const update of transcriptToolCalls) {
      if (
        startedToolCallIds.has(update.toolCallId) ||
        completedToolCallIds.has(update.toolCallId)
      ) {
        continue;
      }

      startedToolCallIds.add(update.toolCallId);
      toolCalls.set(update.toolCallId, {
        title: update.title,
        rawInput: update.rawInput,
        type: update.type,
      });
      await agent.connection.sessionUpdate({
        sessionId,
        update: {
          ...update,
          status: "in_progress",
        },
      });
    }

    for (const update of transcriptToolResults) {
      if (completedToolCallIds.has(update.toolCallId)) {
        continue;
      }

      completedToolCallIds.add(update.toolCallId);
      startedToolCallIds.add(update.toolCallId);
      toolCalls.delete(update.toolCallId);
      await agent.connection.sessionUpdate({
        sessionId,
        update,
      });
    }
  })();

  pending.transcriptToolSyncPromise = syncPromise;
  try {
    await syncPromise;
  } finally {
    if (pending.transcriptToolSyncPromise === syncPromise) {
      delete pending.transcriptToolSyncPromise;
    }
  }
}

async function capturePromptTranscriptSyncState(
  agent,
  sessionId,
  sessionKey,
): Promise<PromptTranscriptSyncState> {
  if (!sessionKey || !agent?.gateway?.request) {
    return { transcriptSyncDisabled: true };
  }

  try {
    const transcript = await agent.gateway.request("sessions.get", {
      key: sessionKey,
      limit: 1000,
    });
    const messages = Array.isArray(transcript?.messages) ? transcript.messages : [];
    return {
      transcriptMessageCursor: messages.length,
      transcriptSyncDisabled: false,
    };
  } catch (error) {
    if (hasMissingOperatorReadScope(error)) {
      agent.log?.(
        `prompt: disabling transcript sync for ${sessionId}; gateway transcript read requires operator.read`,
      );
      return { transcriptSyncDisabled: true };
    }

    agent.log?.(
      `prompt: disabling transcript sync for ${sessionId}; failed to capture transcript baseline: ${String(error)}`,
    );
    return { transcriptSyncDisabled: true };
  }
}

function startPromptToolTranscriptSync(
  agent,
  sessionId,
  pending,
  hooks: PromptToolTranscriptSyncHooks = {},
) {
  if (!pending?.sessionKey || !agent?.gateway?.request) {
    return () => {};
  }

  const setTimer = hooks.setInterval ?? globalThis.setInterval;
  const clearTimer = hooks.clearInterval ?? globalThis.clearInterval;
  const intervalMs =
    Number.isFinite(hooks.toolTranscriptSyncIntervalMs) &&
    hooks.toolTranscriptSyncIntervalMs > 0
      ? hooks.toolTranscriptSyncIntervalMs
      : DEFAULT_LIVE_TOOL_TRANSCRIPT_SYNC_INTERVAL_MS;

  const timer = setTimer(() => {
    if (agent.pendingPrompts?.get?.(sessionId) !== pending) {
      clearTimer(timer);
      return;
    }
    void syncPendingToolCallTranscriptUpdates(agent, sessionId, pending).catch((error) => {
      agent.log?.(
        `startPromptToolTranscriptSync: transcript sync failed for ${sessionId}: ${String(error)}`,
      );
    });
  }, intervalMs);

  return () => {
    clearTimer(timer);
  };
}

/**
 * Converts persisted OpenClaw session transcript entries into ACP session updates so
 * `session/load` can reconstruct prior transcript state for any ACP client.
 */
export function buildHistoryReplayUpdates(messages = []) {
  if (!Array.isArray(messages)) {
    return [];
  }

  const updates = [];
  for (const [index, rawMessage] of messages.entries()) {
    if (!rawMessage || typeof rawMessage !== "object") {
      continue;
    }
    const historyMessageId =
      (typeof rawMessage.id === "string" && rawMessage.id.trim()) || `history-${index}`;
    const role = typeof rawMessage.role === "string" ? rawMessage.role.toLowerCase() : "";
    const content = normalizeHistoryContent(rawMessage.content);

    if (role === "user") {
      const text = extractVisibleUserHistoryText(content);
      if (text) {
        updates.push({
          sessionUpdate: "user_message_chunk",
          historyMessageId,
          content: { type: "text", text },
        });
      }
      continue;
    }

    if (role === "assistant") {
      const sanitizedContent = sanitizeAssistantContentForAcp(content);
      for (const item of sanitizedContent) {
        const type = typeof item.type === "string" ? item.type.toLowerCase() : "";
        if (["toolcall", "tool_call", "tooluse", "tool_use"].includes(type)) {
          const toolUpdate = buildHistoryToolCallUpdate(item);
          if (toolUpdate) {
            updates.push(toolUpdate);
          }
        }
      }
      const text = readTextFromHistoryContent(sanitizedContent);
      if (text) {
        updates.push({
          sessionUpdate: "agent_message_chunk",
          historyMessageId,
          content: { type: "text", text },
        });
      }
      continue;
    }

    if (role === "toolresult" || role === "tool_result" || role === "tool") {
      const toolResultUpdate = buildHistoryToolResultUpdate(rawMessage, content);
      if (toolResultUpdate) {
        updates.push(toolResultUpdate);
      }
    }
  }

  return updates;
}

function hasMissingOperatorReadScope(value, seen = new Set()) {
  if (typeof value === "string") {
    return value.toLowerCase().includes("missing scope: operator.read");
  }
  if (!value || typeof value !== "object" || seen.has(value)) {
    return false;
  }
  seen.add(value);
  if (Array.isArray(value)) {
    return value.some((entry) => hasMissingOperatorReadScope(entry, seen));
  }
  return (
    hasMissingOperatorReadScope(value.message, seen) ||
    hasMissingOperatorReadScope(value.errorMessage, seen) ||
    hasMissingOperatorReadScope(value.data, seen) ||
    hasMissingOperatorReadScope(value.cause, seen)
  );
}

async function replayGatewayTranscript(agent, session) {
  if (!agent?.gateway?.request || !agent?.connection?.sessionUpdate) {
    return;
  }

  let transcript;
  try {
    transcript = await agent.gateway.request("sessions.get", {
      key: session.sessionKey,
      limit: 1000,
    });
  } catch (error) {
    if (hasMissingOperatorReadScope(error)) {
      agent.log?.(
        `replayGatewayTranscript: skipping transcript replay for ${session.sessionId}; gateway transcript read requires operator.read`,
      );
      return;
    }
    throw error;
  }

  const updates = buildHistoryReplayUpdates(transcript?.messages);
  for (const update of updates) {
    await agent.connection.sessionUpdate({
      sessionId: session.sessionId,
      update,
    });
  }
}

/**
 * Returns the deterministic gateway session key used for ACP session IDs that
 * do not already carry an explicit gateway session key.
 */
export function buildBridgeFallbackSessionKey(sessionId, env = process.env) {
  const normalizedSessionID =
    typeof sessionId === "string" && sessionId.trim() ? sessionId.trim() : randomUUID();
  const agentId = normalizeBridgeToken(
    env.SPRITZ_OPENCLAW_ACP_FALLBACK_AGENT_ID,
    DEFAULT_FALLBACK_AGENT_ID,
  );
  const prefix = normalizeBridgeToken(
    env.SPRITZ_OPENCLAW_ACP_FALLBACK_SESSION_PREFIX,
    DEFAULT_FALLBACK_SESSION_PREFIX,
  );
  return `agent:${agentId}:${prefix}:${normalizedSessionID}`;
}

/**
 * Preserves explicit/listed gateway session keys and only maps ACP-generated
 * UUID session IDs onto deterministic OpenClaw gateway session keys.
 */
export function resolveBridgeFallbackSessionKey(sessionId, env = process.env) {
  const normalized = typeof sessionId === "string" ? sessionId.trim() : "";
  if (!normalized) {
    return buildBridgeFallbackSessionKey("", env);
  }
  if (!UUIDISH_SESSION_ID_PATTERN.test(normalized)) {
    return normalized;
  }
  return buildBridgeFallbackSessionKey(normalized, env);
}

/**
 * Lazily starts the OpenClaw gateway connection only when ACP session methods
 * actually need it. This prevents initialize-only ACP probes from opening and
 * then abruptly tearing down gateway webchat sessions.
 */
export function createLazyGatewayController(gateway, hooks: LazyGatewayHooks = {}) {
  let ensureReadyPromise = null;
  let stopped = false;

  return {
    async ensureReady() {
      if (stopped) {
        throw new Error("Gateway controller has already been stopped.");
      }
      if (!ensureReadyPromise) {
        gateway.start();
        ensureReadyPromise = Promise.resolve()
          .then(() => hooks.waitUntilReady?.())
          .catch((error) => {
            throw error;
          });
      }
      return ensureReadyPromise;
    },
    stop() {
      if (stopped) {
        return;
      }
      stopped = true;
      hooks.onStop?.();
      gateway.stop();
    },
  };
}

/**
 * Builds the static ACP metadata advertised by the Spritz OpenClaw adapter.
 * This mirrors the ACP initialize surface the runtime exposes over WebSocket.
 */
export function buildSpritzOpenclawAcpMetadata(version = "unknown") {
  return {
    protocolVersion: DEFAULT_ACP_PROTOCOL_VERSION,
    agentCapabilities: {
      loadSession: true,
      promptCapabilities: {
        image: true,
        audio: false,
        embeddedContext: true,
      },
      mcp: {
        http: false,
        sse: false,
      },
    },
    agentInfo: {
      ...DEFAULT_OPENCLAW_ACP_AGENT_INFO,
      version,
    },
    authMethods: [],
  };
}

/**
 * Extends OpenClaw's ACP gateway agent so the default ACP session flow maps to
 * normal agent-scoped gateway sessions instead of ACP runtime session keys.
 */
export function createSpritzAcpGatewayAgentClass(
  AcpGatewayAgent,
  env = process.env,
  hooks: GatewayAgentClassHooks = {},
) {
  return class SpritzOpenclawAcpGatewayAgent extends AcpGatewayAgent {
    findPendingBySessionKey(sessionKey, runId) {
      return findPendingPromptBySessionKey(this.pendingPrompts, sessionKey, runId);
    }

    async handleAgentEvent(evt) {
      const payload = evt?.payload;
      const pending =
        payload?.sessionKey && payload?.stream === "tool"
          ? this.findPendingBySessionKey(payload.sessionKey, payload.runId)
          : undefined;
      const result = await super.handleAgentEvent(evt);
      if (pending) {
        rememberObservedToolLifecycleEvent(pending, payload);
      }
      return result;
    }

    async handleDeltaEvent(sessionId, messageData) {
      const pending = this.pendingPrompts?.get?.(sessionId);
      if (pending) {
        await emitLiveToolCallContentUpdates(this, sessionId, pending, messageData);
      }
      const sanitizedMessageData =
        messageData && typeof messageData === "object"
          ? {
              ...messageData,
              content: sanitizeAssistantContentForAcp(messageData.content, {
                suppressLeadFragments: true,
              }),
            }
          : messageData;
      return await super.handleDeltaEvent(sessionId, sanitizedMessageData);
    }

    async finishPrompt(sessionId, pending, stopReason) {
      await syncPendingToolCallTranscriptUpdates(this, sessionId, pending);
      return await super.finishPrompt(sessionId, pending, stopReason);
    }

    async newSession(params) {
      await hooks.ensureGatewayReady?.();
      if (params.mcpServers.length > 0) {
        this.log(`ignoring ${params.mcpServers.length} MCP servers`);
      }
      this.enforceSessionCreateRateLimit("newSession");

      const sessionId = randomUUID();
      const session = this.sessionStore.createSession({
        sessionId,
        sessionKey: resolveBridgeFallbackSessionKey(sessionId, env),
        cwd: params.cwd,
      });
      this.log(`newSession: ${session.sessionId} -> ${session.sessionKey}`);
      await this.sendAvailableCommands(session.sessionId);
      return { sessionId: session.sessionId };
    }

    async loadSession(params) {
      await hooks.ensureGatewayReady?.();
      if (params.mcpServers.length > 0) {
        this.log(`ignoring ${params.mcpServers.length} MCP servers`);
      }
      if (!this.sessionStore.hasSession(params.sessionId)) {
        this.enforceSessionCreateRateLimit("loadSession");
      }

      const session = this.sessionStore.createSession({
        sessionId: params.sessionId,
        sessionKey: resolveBridgeFallbackSessionKey(params.sessionId, env),
        cwd: params.cwd,
      });
      this.log(`loadSession: ${session.sessionId} -> ${session.sessionKey}`);
      await replayGatewayTranscript(this, session);
      await this.sendAvailableCommands(session.sessionId);
      return {};
    }

    async prompt(params) {
      await hooks.ensureGatewayReady?.();
      const session = this.sessionStore?.getSession?.(params.sessionId);
      const transcriptSyncState = await capturePromptTranscriptSyncState(
        this,
        params.sessionId,
        session?.sessionKey,
      );

      const promptText = extractPromptText(params?.prompt);
      if (promptText && session) {
        await this.connection.sessionUpdate({
          sessionId: params.sessionId,
          update: {
            sessionUpdate: "user_message_chunk",
            content: { type: "text", text: promptText },
          },
        });
      }

      const promptPromise = super.prompt(params);
      const pending = this.pendingPrompts?.get?.(params.sessionId);
      if (pending) {
        pending.transcriptMessageCursor = transcriptSyncState.transcriptMessageCursor;
        pending.transcriptSyncDisabled = transcriptSyncState.transcriptSyncDisabled;
      }
      const stopPromptToolSync =
        pending && !pending.transcriptSyncDisabled
          ? startPromptToolTranscriptSync(this, params.sessionId, pending, hooks)
          : () => {};

      try {
        return await promptPromise;
      } finally {
        stopPromptToolSync();
      }
    }
  };
}

/**
 * Parses the CLI args accepted by the image-owned ACP wrapper. The wrapper
 * accepts the leading `acp` subcommand so it can be dropped in place of the
 * normal `openclaw` binary.
 */
export function parseArgs(
  argv,
  helpers = { readSecretFromFile: defaultReadSecretFromFile },
): ParsedArgs {
  const args = normalizeCliArgs(argv);
  const opts: ParsedArgs = {};
  let tokenFile;
  let passwordFile;

  for (let index = 0; index < args.length; index += 1) {
    const arg = args[index];
    if (arg === "--url" || arg === "--gateway-url") {
      opts.gatewayUrl = args[index + 1];
      index += 1;
      continue;
    }
    if (arg === "--token" || arg === "--gateway-token") {
      opts.gatewayToken = args[index + 1];
      index += 1;
      continue;
    }
    if (arg === "--token-file" || arg === "--gateway-token-file") {
      tokenFile = args[index + 1];
      index += 1;
      continue;
    }
    if (arg === "--password" || arg === "--gateway-password") {
      opts.gatewayPassword = args[index + 1];
      index += 1;
      continue;
    }
    if (arg === "--password-file" || arg === "--gateway-password-file") {
      passwordFile = args[index + 1];
      index += 1;
      continue;
    }
    if (arg === "--session") {
      opts.defaultSessionKey = args[index + 1];
      index += 1;
      continue;
    }
    if (arg === "--session-label") {
      opts.defaultSessionLabel = args[index + 1];
      index += 1;
      continue;
    }
    if (arg === "--require-existing") {
      opts.requireExistingSession = true;
      continue;
    }
    if (arg === "--reset-session") {
      opts.resetSession = true;
      continue;
    }
    if (arg === "--no-prefix-cwd") {
      opts.prefixCwd = false;
      continue;
    }
    if (arg === "--provenance") {
      const normalized = normalizeAcpProvenanceMode(args[index + 1]);
      if (!normalized) {
        throw new Error("Invalid --provenance value. Use off, meta, or meta+receipt.");
      }
      opts.provenanceMode = normalized;
      index += 1;
      continue;
    }
    if (arg === "--verbose" || arg === "-v") {
      opts.verbose = true;
      continue;
    }
    if (arg === "--help" || arg === "-h") {
      opts.help = true;
      continue;
    }
  }

  if (typeof opts.gatewayToken === "string" && tokenFile?.trim()) {
    throw new Error("Use either --token or --token-file.");
  }
  if (typeof opts.gatewayPassword === "string" && passwordFile?.trim()) {
    throw new Error("Use either --password or --password-file.");
  }
  if (tokenFile?.trim()) {
    opts.gatewayToken = helpers.readSecretFromFile(tokenFile, "Gateway token");
  }
  if (passwordFile?.trim()) {
    opts.gatewayPassword = helpers.readSecretFromFile(passwordFile, "Gateway password");
  }

  return opts;
}

/**
 * Builds the Gateway client profile used by the wrapper. In trusted-proxy mode
 * the bridge must connect as a Control UI operator session so OpenClaw applies
 * browser-style trusted-proxy auth instead of pairing-oriented CLI auth.
 */
export function buildGatewayClientOptions(params: GatewayClientOptionsParams) {
  const base = {
    url: params.connectionUrl,
    clientDisplayName: "ACP",
    clientVersion: "acp",
    role: "operator",
  };

  if (params.trustedProxyControlUi) {
    return {
      ...base,
      clientName: GATEWAY_CLIENT_NAMES.CONTROL_UI,
      mode: GATEWAY_CLIENT_MODES.WEBCHAT,
      token: undefined,
      password: undefined,
    };
  }

  return {
    ...base,
    clientName: GATEWAY_CLIENT_NAMES.CLI,
    mode: GATEWAY_CLIENT_MODES.CLI,
    token: params.gatewayToken,
    password: params.gatewayPassword,
  };
}

function normalizeCliArgs(argv) {
  if (argv[0] === "acp") {
    return argv.slice(1);
  }
  return argv.slice();
}

function normalizeAcpProvenanceMode(value) {
  if (typeof value !== "string") {
    return undefined;
  }
  const normalized = value.trim().toLowerCase();
  if (!normalized) {
    return undefined;
  }
  if (normalized === "off" || normalized === "meta" || normalized === "meta+receipt") {
    return normalized;
  }
  return undefined;
}

function defaultReadSecretFromFile(filePath, label) {
  try {
    return fs.readFileSync(filePath, "utf8").trim();
  } catch (error) {
    throw new Error(`Failed to read ${label} from ${filePath}: ${String(error)}`);
  }
}

export function resolveOpenclawPackageRoot(env = process.env) {
  const raw = env.SPRITZ_OPENCLAW_PACKAGE_ROOT;
  if (typeof raw === "string" && raw.trim()) {
    return raw.trim();
  }
  return DEFAULT_OPENCLAW_PACKAGE_ROOT;
}

export async function importOpenclawDependency(specifier, env = process.env) {
  const packageRoot = resolveOpenclawPackageRoot(env);
  const requireFromOpenclaw = createRequire(path.join(packageRoot, "package.json"));
  const resolvedPath = requireFromOpenclaw.resolve(specifier);
  return await import(pathToFileURL(resolvedPath).href);
}

export async function loadAcpSdk(env = process.env) {
  return await importOpenclawDependency("@agentclientprotocol/sdk", env);
}

export async function loadOpenclawCompat(env = process.env) {
  const packageRoot = resolveOpenclawPackageRoot(env);
  const resolvedPath = path.join(packageRoot, "dist", "spritz-acp-compat.js");
  return await import(pathToFileURL(resolvedPath).href);
}

export function readOpenclawPackageVersion(env = process.env) {
  const packageRoot = resolveOpenclawPackageRoot(env);
  const packageJSONPath = path.join(packageRoot, "package.json");
  try {
    const parsed = JSON.parse(fs.readFileSync(packageJSONPath, "utf8"));
    const version = typeof parsed.version === "string" ? parsed.version.trim() : "";
    return version || "unknown";
  } catch {
    return "unknown";
  }
}

async function serveSpritzOpenclawAcp(opts: ParsedArgs = {}, env = process.env) {
  const sdk = await loadAcpSdk(env);
  const {
    AcpGatewayAgent,
    GatewayClient,
    buildGatewayConnectionDetails,
    loadConfig,
    resolveGatewayConnectionAuth,
  } = await loadOpenclawCompat(env);

  const AgentSideConnection =
    sdk.AgentSideConnection ?? sdk.default?.AgentSideConnection;
  const ndJsonStream = sdk.ndJsonStream ?? sdk.default?.ndJsonStream;
  if (!AgentSideConnection || !ndJsonStream) {
    throw new Error("Failed to load ACP SDK from the installed OpenClaw package.");
  }

  const cfg = loadConfig();
  const connection = buildGatewayConnectionDetails({
    config: cfg,
    url: opts.gatewayUrl,
  });
  const gatewayUrlOverrideSource =
    connection.urlSource === "cli --url"
      ? "cli"
      : connection.urlSource === "env OPENCLAW_GATEWAY_URL"
        ? "env"
        : undefined;
  const creds = await resolveGatewayConnectionAuth({
    config: cfg,
    explicitAuth: {
      token: opts.gatewayToken,
      password: opts.gatewayPassword,
    },
    env,
    urlOverride: gatewayUrlOverrideSource ? connection.url : undefined,
    urlOverrideSource: gatewayUrlOverrideSource,
  });

  const trustedProxyControlUi = useTrustedProxyControlUiBridge(env);
  let agent: any = null;
  let onClosed: () => void = () => {};
  const closed = new Promise<void>((resolve) => {
    onClosed = resolve;
  });
  let stopped = false;
  let onGatewayReadyResolve: () => void = () => {};
  let onGatewayReadyReject: (error: Error) => void = () => {};
  let gatewayReadySettled = false;
  const gatewayReady = new Promise<void>((resolve, reject) => {
    onGatewayReadyResolve = resolve;
    onGatewayReadyReject = reject;
  });
  const resolveGatewayReady = () => {
    if (gatewayReadySettled) {
      return;
    }
    gatewayReadySettled = true;
    onGatewayReadyResolve();
  };
  const rejectGatewayReady = (error) => {
    if (gatewayReadySettled) {
      return;
    }
    gatewayReadySettled = true;
    onGatewayReadyReject(error instanceof Error ? error : new Error(String(error)));
  };

  const gateway = new GatewayClient({
    ...buildGatewayClientOptions({
      connectionUrl: connection.url,
      gatewayToken: trustedProxyControlUi ? undefined : creds.token,
      gatewayPassword: trustedProxyControlUi ? undefined : creds.password,
      trustedProxyControlUi,
    }),
    onEvent: (event) => {
      void agent?.handleGatewayEvent(event);
    },
    onHelloOk: () => {
      resolveGatewayReady();
      agent?.handleGatewayReconnect();
    },
    onConnectError: (error) => {
      rejectGatewayReady(error);
    },
    onClose: (code, reason) => {
      if (!stopped) {
        rejectGatewayReady(new Error(`gateway closed before ready (${code}): ${reason}`));
      }
      agent?.handleGatewayDisconnect(`${code}: ${reason}`);
      if (stopped) {
        onClosed();
      }
    },
  });

  const gatewayController = createLazyGatewayController(gateway, {
    waitUntilReady: () => gatewayReady,
    onStop: () => {
      resolveGatewayReady();
      onClosed();
    },
  });

  const shutdown = () => {
    if (stopped) {
      return;
    }
    stopped = true;
    gatewayController.stop();
  };

  process.once("SIGINT", shutdown);
  process.once("SIGTERM", shutdown);

  const input = Writable.toWeb(process.stdout);
  const output = Readable.toWeb(process.stdin);
  const stream = ndJsonStream(input, output);
  const SpritzAcpGatewayAgent = createSpritzAcpGatewayAgentClass(AcpGatewayAgent, env, {
    ensureGatewayReady: () => gatewayController.ensureReady(),
  });

  new AgentSideConnection((connectionInstance: unknown) => {
    agent = new (SpritzAcpGatewayAgent as any)(connectionInstance, gateway, opts);
    agent.start();
    return agent;
  }, stream);

  return closed;
}

function printHelp() {
  console.log(`Usage: spritz-openclaw-acp-wrapper [acp] [options]

Image-owned ACP wrapper for Spritz OpenClaw workloads.

Options:
  --url <url>             Gateway WebSocket URL
  --token <token>         Gateway auth token
  --token-file <path>     Read gateway auth token from file
  --password <password>   Gateway auth password
  --password-file <path>  Read gateway auth password from file
  --session <key>         Default session key
  --session-label <label> Default session label to resolve
  --require-existing      Fail if the session key/label does not exist
  --reset-session         Reset the session key before first use
  --no-prefix-cwd         Do not prefix prompts with the working directory
  --provenance <mode>     ACP provenance mode: off, meta, or meta+receipt
  --verbose, -v           Verbose logging to stderr
  --help, -h              Show this help message
`);
}

async function main() {
  const opts = parseArgs(process.argv.slice(2));
  if (opts.help) {
    printHelp();
    return;
  }
  await serveSpritzOpenclawAcp(opts);
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  main().catch((error) => {
    console.error(String(error));
    process.exit(1);
  });
}
