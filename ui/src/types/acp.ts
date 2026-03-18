export interface ACPBlock {
  type: 'text' | 'details' | 'plan' | 'tags' | 'keyValue';
  text?: string;
  title?: string;
  open?: boolean;
  entries?: Array<{ text?: string; done?: boolean; label?: string; value?: string; content?: string; status?: string; priority?: string; name?: string }>;
  items?: Array<{ label?: string; name?: string; title?: string }>;
  key?: string;
  value?: string;
  streaming?: boolean;
  _renderedLength?: number;
}

export interface ACPMessage {
  role: 'user' | 'assistant' | 'tool' | 'system' | 'thinking' | 'thinking_done' | 'plan';
  blocks: ACPBlock[];
  title?: string;
  status?: string;
  tone?: string;
  meta?: string;
  streaming?: boolean;
  _toolCallId?: string;
  _thinkingChunks?: ThinkingChunk[];
  _thinkingElapsedSeconds?: number;
}

export interface ThinkingChunk {
  kind: string;
  text: string;
  url?: string;
  toolKind?: string;
  _toolCallId?: string;
  toolName?: string;
  status?: string;
  input?: string;
  result?: string;
}

export interface ACPTranscript {
  messages: ACPMessage[];
  toolCallIndex: Map<string, number>;
  availableCommands: Array<string | { name: string; description?: string }>;
  currentMode: string;
  usage: { label: string; used: number; size: number } | null;
  thinkingChunks: ThinkingChunk[];
  thinkingActive: boolean;
  thinkingInsertIndex: number;
  thinkingStartTime: number;
  thinkingElapsedSeconds: number;
}

export interface ConversationInfo {
  metadata: {
    name: string;
    namespace?: string;
  };
  spec: {
    sessionId: string;
    title?: string;
    cwd?: string;
    spritzName?: string;
  };
  status?: {
    bindingState?: string;
    lastActivityAt?: string;
  };
}

export interface AgentInfo {
  name?: string;
  title?: string;
  version?: string;
}

export interface PermissionEntry {
  params: unknown;
  respond: (result: unknown) => void;
  reject: (message?: string) => void;
}

export interface SessionUpdate {
  sessionUpdate?: {
    type: string;
    [key: string]: unknown;
  };
}

export interface ACPClientOptions {
  wsUrl: string;
  conversation: ConversationInfo | null;
  onStatus?: (text: string) => void;
  onReadyChange?: (ready: boolean) => void;
  onAgentInfo?: (info: AgentInfo | null) => void;
  onUpdate?: (update: SessionUpdate, options?: { historical?: boolean }) => void;
  onPermissionRequest?: (entry: PermissionEntry) => void;
  onPromptStateChange?: (inFlight: boolean) => void;
  onClose?: (reason: string) => void;
  onProtocolError?: (err: unknown) => void;
}

export interface ACPClient {
  start: () => Promise<void>;
  getConversationId: () => string;
  getSessionId: () => string;
  matchesConversation: (target: ConversationInfo | null) => boolean;
  isReady: () => boolean;
  sendPrompt: (text: string) => Promise<unknown>;
  cancelPrompt: () => void;
  dispose: () => void;
}
