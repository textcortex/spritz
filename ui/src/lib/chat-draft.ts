const CHAT_DRAFTS_STORAGE_KEY = 'spritz:chat-drafts';

interface ChatDraftRecord {
  draft: string;
}

type ChatDraftMap = Record<string, ChatDraftRecord>;

function normalizeString(value: unknown): string {
  if (value === undefined || value === null) return '';
  return String(value);
}

function trimString(value: unknown): string {
  return normalizeString(value).trim();
}

function buildDraftStorageKey(spritzName: string, conversationId: string): string {
  return `${trimString(spritzName)}::${trimString(conversationId)}`;
}

function sanitizeDraft(value: unknown): string | null {
  const draft = normalizeString(value);
  return draft.trim() ? draft : null;
}

function sanitizeDraftMap(raw: unknown): ChatDraftMap {
  if (!raw || typeof raw !== 'object' || Array.isArray(raw)) {
    return {};
  }

  const input = raw as Record<string, unknown>;
  const sanitized: ChatDraftMap = {};

  Object.entries(input).forEach(([key, value]) => {
    if (!key.trim() || !value || typeof value !== 'object' || Array.isArray(value)) {
      return;
    }
    const record = value as Record<string, unknown>;
    const draft = sanitizeDraft(record.draft);
    if (draft) {
      sanitized[key] = { draft };
    }
  });

  return sanitized;
}

function readDraftMap(): ChatDraftMap {
  try {
    const raw = localStorage.getItem(CHAT_DRAFTS_STORAGE_KEY);
    if (!raw) return {};
    return sanitizeDraftMap(JSON.parse(raw));
  } catch {
    try {
      localStorage.removeItem(CHAT_DRAFTS_STORAGE_KEY);
    } catch {
      // ignore
    }
    return {};
  }
}

function writeDraftMap(entries: ChatDraftMap): void {
  const sanitized = sanitizeDraftMap(entries);
  if (Object.keys(sanitized).length === 0) {
    try {
      localStorage.removeItem(CHAT_DRAFTS_STORAGE_KEY);
    } catch {
      // ignore
    }
    return;
  }

  try {
    localStorage.setItem(CHAT_DRAFTS_STORAGE_KEY, JSON.stringify(sanitized));
  } catch {
    // ignore
  }
}

export function readChatDraft(spritzName: string, conversationId: string): string | null {
  const key = buildDraftStorageKey(spritzName, conversationId);
  if (!key || key === '::') return null;
  return readDraftMap()[key]?.draft || null;
}

export function writeChatDraft(spritzName: string, conversationId: string, draft: string): void {
  const key = buildDraftStorageKey(spritzName, conversationId);
  if (!key || key === '::') return;

  const entries = readDraftMap();
  const sanitizedDraft = sanitizeDraft(draft);

  if (!sanitizedDraft) {
    delete entries[key];
    writeDraftMap(entries);
    return;
  }

  entries[key] = { draft: sanitizedDraft };
  writeDraftMap(entries);
}

export function clearChatDraft(spritzName: string, conversationId: string): void {
  const key = buildDraftStorageKey(spritzName, conversationId);
  if (!key || key === '::') return;

  const entries = readDraftMap();
  delete entries[key];
  writeDraftMap(entries);
}
