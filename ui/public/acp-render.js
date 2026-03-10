(function (global) {
  const extractACPText =
    global.SpritzACPClient?.extractACPText ||
    function fallbackExtractACPText(value) {
      if (value === null || value === undefined) return '';
      if (typeof value === 'string') return value;
      if (Array.isArray(value)) {
        return value.map((item) => fallbackExtractACPText(item)).filter(Boolean).join('\n');
      }
      if (typeof value !== 'object') return String(value);
      if (typeof value.text === 'string') return value.text;
      if (value.type === 'content' && value.content) return fallbackExtractACPText(value.content);
      if (value.content) return fallbackExtractACPText(value.content);
      if (value.resource) {
        if (typeof value.resource.text === 'string') return value.resource.text;
        if (typeof value.resource.uri === 'string') return value.resource.uri;
      }
      return '';
    };

  function createId(prefix) {
    return `${prefix}-${Date.now()}-${Math.random().toString(36).slice(2, 8)}`;
  }

  function createTranscript() {
    return {
      messages: [],
      toolCallIndex: new Map(),
      availableCommands: [],
      currentMode: '',
      usage: null,
    };
  }

  function rebuildToolCallIndex(transcript) {
    transcript.toolCallIndex = new Map();
    transcript.messages.forEach((message, index) => {
      if (message?.kind === 'tool' && message.toolCallId) {
        transcript.toolCallIndex.set(message.toolCallId, index);
      }
    });
    return transcript;
  }

  function serializeTranscript(transcript) {
    return {
      messages: Array.isArray(transcript?.messages)
        ? transcript.messages.map((message) => ({
            id: message.id || '',
            kind: message.kind || 'system',
            title: message.title || '',
            status: message.status || '',
            tone: message.tone || '',
            meta: message.meta || '',
            blocks: Array.isArray(message.blocks) ? message.blocks : [],
            streaming: Boolean(message.streaming),
            toolCallId: message.toolCallId || '',
          }))
        : [],
      availableCommands: Array.isArray(transcript?.availableCommands) ? transcript.availableCommands : [],
      currentMode: typeof transcript?.currentMode === 'string' ? transcript.currentMode : '',
      usage:
        transcript?.usage && typeof transcript.usage === 'object'
          ? {
              label: typeof transcript.usage.label === 'string' ? transcript.usage.label : '',
              used: typeof transcript.usage.used === 'number' ? transcript.usage.used : null,
              size: typeof transcript.usage.size === 'number' ? transcript.usage.size : null,
            }
          : null,
    };
  }

  function hydrateTranscript(payload) {
    const transcript = createTranscript();
    transcript.messages = Array.isArray(payload?.messages)
      ? payload.messages
          .map((message) => sanitizeHydratedMessage(message))
          .filter(Boolean)
      : [];
    transcript.availableCommands = Array.isArray(payload?.availableCommands) ? payload.availableCommands : [];
    transcript.currentMode = typeof payload?.currentMode === 'string' ? payload.currentMode : '';
    transcript.usage =
      payload?.usage && typeof payload.usage === 'object'
        ? {
            label: typeof payload.usage.label === 'string' ? payload.usage.label : '',
            used: typeof payload.usage.used === 'number' ? payload.usage.used : null,
            size: typeof payload.usage.size === 'number' ? payload.usage.size : null,
          }
        : null;
    return rebuildToolCallIndex(transcript);
  }

  function stringifyDetails(value) {
    if (value === undefined || value === null || value === '') return '';
    if (typeof value === 'string') return value;
    try {
      return JSON.stringify(value, null, 2);
    } catch {
      return String(value);
    }
  }

  function excerpt(text, maxLength = 120) {
    const normalized = String(text || '').replace(/\s+/g, ' ').trim();
    if (!normalized) return '';
    if (normalized.length <= maxLength) return normalized;
    return `${normalized.slice(0, maxLength - 1)}…`;
  }

  function decodeHtmlEntities(text) {
    return String(text || '')
      .replace(/&nbsp;/gi, ' ')
      .replace(/&amp;/gi, '&')
      .replace(/&lt;/gi, '<')
      .replace(/&gt;/gi, '>')
      .replace(/&quot;/gi, '"')
      .replace(/&#39;/gi, "'");
  }

  function stripHtmlTags(text) {
    return decodeHtmlEntities(
      String(text || '')
        .replace(/<script[\s\S]*?<\/script>/gi, ' ')
        .replace(/<style[\s\S]*?<\/style>/gi, ' ')
        .replace(/<[^>]+>/g, ' '),
    )
      .replace(/\s+/g, ' ')
      .trim();
  }

  function extractHtmlTagText(html, tagName) {
    const match = String(html || '').match(new RegExp(`<${tagName}[^>]*>([\\s\\S]*?)<\\/${tagName}>`, 'i'));
    return match ? stripHtmlTags(match[1]) : '';
  }

  function detectHtmlErrorDocument(text) {
    const raw = String(text || '').trim();
    if (!raw) return null;
    if (!/^\s*<(?:!doctype\s+html|html\b)/i.test(raw)) {
      return null;
    }

    const title = extractHtmlTagText(raw, 'title');
    const flattened = stripHtmlTags(raw);
    const codeMatch =
      flattened.match(/\berror code\s+(\d{3})\b/i) ||
      title.match(/\b(\d{3})\b/);
    const hostMatches = [...flattened.matchAll(/\b([a-z0-9.-]+\.[a-z]{2,})\b/gi)].map((match) => match[1]);
    const host =
      hostMatches
        .sort((left, right) => right.length - left.length)
        .find((value) => !/^cloudflare\.com$/i.test(value)) || '';
    const providerMatch = flattened.match(/\b(Cloudflare|Vercel|Netlify|nginx|Apache)\b/i);
    const summaryMatch =
      flattened.match(/\bThe web server reported [^.]+\./i) ||
      flattened.match(/\bThis page isn[’']t working[^.]*\./i) ||
      flattened.match(/\bBad gateway\b/i);

    const parts = [];
    if (codeMatch?.[1]) {
      parts.push(`HTTP ${codeMatch[1]}`);
    }
    if (title) {
      parts.push(title);
    } else if (summaryMatch?.[0]) {
      parts.push(summaryMatch[0]);
    } else {
      parts.push('HTML error response');
    }
    if (host) {
      parts.push(host);
    }
    if (providerMatch?.[1]) {
      parts.push(providerMatch[1]);
    }

    return {
      text: parts.join(' · '),
      open: false,
      isError: true,
    };
  }

  function normalizeToolResultText(rawOutput) {
    const text = stringifyDetails(rawOutput);
    if (!text) {
      return { text: '', open: true, isError: false };
    }
    const htmlError = detectHtmlErrorDocument(text);
    if (htmlError) {
      return htmlError;
    }
    return {
      text,
      open: true,
      isError: false,
    };
  }

  function sanitizeHydratedBlock(kind, block) {
    if (!block || typeof block !== 'object') return null;
    if (block.type === 'text') {
      const htmlError = detectHtmlErrorDocument(block.text);
      if (htmlError) {
        return kind === 'tool'
          ? { ...block, type: 'details', title: 'Result', text: htmlError.text, open: false }
          : null;
      }
      return { ...block, text: String(block.text || '') };
    }
    if (block.type === 'details') {
      const htmlError = detectHtmlErrorDocument(block.text);
      if (htmlError) {
        return { ...block, text: htmlError.text, open: false };
      }
      return { ...block, text: String(block.text || '') };
    }
    return block;
  }

  function sanitizeHydratedMessage(message) {
    const kind = message?.kind || 'system';
    const hadHtmlError = Array.isArray(message?.blocks)
      ? message.blocks.some((block) => {
          if (!block || typeof block !== 'object') return false;
          const value = block.type === 'text' || block.type === 'details' ? block.text : '';
          return Boolean(detectHtmlErrorDocument(value));
        })
      : false;
    const blocks = Array.isArray(message?.blocks)
      ? message.blocks.map((block) => sanitizeHydratedBlock(kind, block)).filter(Boolean)
      : [];
    if (!blocks.length && (kind === 'assistant' || kind === 'user')) {
      return null;
    }
    return {
      id: message?.id || createId(kind || 'message'),
      kind,
      title: message?.title || '',
      status:
        kind === 'tool' && hadHtmlError && (!message?.status || message.status === 'completed')
          ? 'failed'
          : message?.status || '',
      tone:
        kind === 'tool' && hadHtmlError
          ? 'danger'
          : message?.tone || '',
      meta: message?.meta || '',
      blocks,
      streaming: Boolean(message?.streaming),
      toolCallId: message?.toolCallId || '',
      historyMessageId: message?.historyMessageId || '',
    };
  }

  function pushMessage(transcript, message) {
    transcript.messages.push({
      id: message.id || createId(message.kind || 'message'),
      kind: message.kind,
      title: message.title || '',
      status: message.status || '',
      tone: message.tone || '',
      meta: message.meta || '',
      blocks: Array.isArray(message.blocks) ? message.blocks : [],
      streaming: Boolean(message.streaming),
      toolCallId: message.toolCallId || '',
      historyMessageId: message.historyMessageId || '',
    });
    return transcript.messages[transcript.messages.length - 1];
  }

  function createTextBlocks(text) {
    const normalized = String(text || '');
    if (!normalized) return [];
    return [{ type: 'text', text: normalized }];
  }

  function appendHistoricalText(transcript, kind, text, messageKey = '') {
    const value = String(text || '');
    if (!value) return;
    const normalizedKey = String(messageKey || '').trim();
    const last = transcript.messages[transcript.messages.length - 1];
    if (normalizedKey && last && last.kind === kind && last.historyMessageId === normalizedKey) {
      const textBlock = last.blocks.find((block) => block.type === 'text');
      if (textBlock) {
        textBlock.text += value;
      } else {
        last.blocks.push({ type: 'text', text: value });
      }
      return;
    }
    pushMessage(transcript, {
      kind,
      streaming: false,
      historyMessageId: normalizedKey,
      blocks: createTextBlocks(value),
    });
  }

  function appendStreamingText(transcript, kind, text) {
    const chunk = String(text || '');
    if (!chunk) return;
    const last = transcript.messages[transcript.messages.length - 1];
    if (last && last.kind === kind && last.streaming) {
      const textBlock = last.blocks.find((block) => block.type === 'text');
      if (textBlock) {
        textBlock.text += chunk;
      } else {
        last.blocks.push({ type: 'text', text: chunk });
      }
      return;
    }
    pushMessage(transcript, {
      kind,
      streaming: true,
      blocks: createTextBlocks(chunk),
    });
  }

  function finalizeStreaming(transcript) {
    transcript.messages.forEach((message) => {
      if (message.kind === 'assistant' || message.kind === 'user') {
        message.streaming = false;
      }
    });
  }

  function buildToolBlocks(update, existing) {
    const blocks = [];
    const inputText = stringifyDetails(update.rawInput);
    const normalizedResult = normalizeToolResultText(update.rawOutput);
    const resultText = normalizedResult.text;
    if (inputText) {
      blocks.push({ type: 'details', title: 'Input', text: inputText, open: false });
    } else if (existing) {
      const priorInput = existing.blocks.find((block) => block.type === 'details' && block.title === 'Input');
      if (priorInput) blocks.push(priorInput);
    }
    if (resultText) {
      blocks.push({ type: 'details', title: 'Result', text: resultText, open: normalizedResult.open });
    } else if (existing) {
      const priorResult = existing.blocks.find((block) => block.type === 'details' && block.title === 'Result');
      if (priorResult) blocks.push(priorResult);
    }
    return {
      blocks,
      isError: normalizedResult.isError,
      summary: normalizedResult.isError ? normalizedResult.text : '',
    };
  }

  function upsertToolCall(transcript, update) {
    const toolCallId = update.toolCallId || createId('tool');
    const existingIndex = transcript.toolCallIndex.get(toolCallId);
    const existing = existingIndex !== undefined ? transcript.messages[existingIndex] : null;
    const normalizedBlocks = buildToolBlocks(update, existing);
    const title = update.title || existing?.title || 'Tool call';
    const status =
      normalizedBlocks.isError && (!update.status || update.status === 'completed')
        ? 'failed'
        : update.status || existing?.status || 'pending';
    const next = {
      kind: 'tool',
      title,
      status,
      tone: status === 'completed' ? 'success' : status === 'failed' ? 'danger' : 'info',
      meta: update.kind || existing?.meta || '',
      blocks: normalizedBlocks.blocks,
      toolCallId,
    };

    if (existing) {
      transcript.messages[existingIndex] = {
        ...existing,
        ...next,
      };
      return normalizedBlocks;
    }

    transcript.toolCallIndex.set(toolCallId, transcript.messages.length);
    pushMessage(transcript, next);
    return normalizedBlocks;
  }

  function createUsageBlocks(update) {
    const entries = [];
    if (typeof update.used === 'number') {
      entries.push({ label: 'Used', value: String(update.used) });
    }
    if (typeof update.size === 'number') {
      entries.push({ label: 'Budget', value: String(update.size) });
    }
    if (update.label) {
      entries.push({ label: 'Label', value: String(update.label) });
    }
    return entries;
  }

  function humanizeUpdateKind(kind) {
    return String(kind || 'Update')
      .replace(/_/g, ' ')
      .replace(/\b\w/g, (match) => match.toUpperCase());
  }

  function applySessionUpdate(transcript, update, options = {}) {
    const kind = update?.sessionUpdate || 'unknown';
    const historical = Boolean(options.historical);
    if (kind === 'user_message_chunk') {
      const text = extractACPText(update.content);
      const htmlError = detectHtmlErrorDocument(text);
      if (htmlError) {
        return {
          toast: {
            kind: 'error',
            message: htmlError.text,
          },
        };
      }
      if (historical) {
        appendHistoricalText(
          transcript,
          'user',
          text,
          update.historyMessageId || update.messageId,
        );
      } else {
        appendStreamingText(transcript, 'user', text);
      }
      return null;
    }
    if (kind === 'agent_message_chunk') {
      const text = extractACPText(update.content);
      const htmlError = detectHtmlErrorDocument(text);
      if (htmlError) {
        return {
          toast: {
            kind: 'error',
            message: htmlError.text,
          },
        };
      }
      if (historical) {
        appendHistoricalText(
          transcript,
          'assistant',
          text,
          update.historyMessageId || update.messageId,
        );
      } else {
        appendStreamingText(transcript, 'assistant', text);
      }
      return null;
    }
    if (kind === 'tool_call' || kind === 'tool_call_update') {
      const toolResult = upsertToolCall(transcript, update);
      if (!historical && toolResult?.isError && toolResult.summary) {
        return {
          toast: {
            kind: 'error',
            message: toolResult.summary,
          },
        };
      }
      return null;
    }
    if (kind === 'available_commands_update') {
      transcript.availableCommands = Array.isArray(update.availableCommands) ? update.availableCommands : [];
      return null;
    }
    if (kind === 'current_mode_update') {
      transcript.currentMode = String(update.mode || update.currentMode || '').trim();
      return null;
    }
    if (kind === 'usage_update') {
      transcript.usage = {
        label: String(update.label || 'Usage'),
        used: typeof update.used === 'number' ? update.used : null,
        size: typeof update.size === 'number' ? update.size : null,
      };
      if (transcript.usage.used === null && transcript.usage.size === null) {
        transcript.usage = null;
      }
      return null;
    }
    if (kind === 'plan') {
      pushMessage(transcript, {
        kind: 'plan',
        title: 'Plan',
        blocks: [
          {
            type: 'plan',
            entries: Array.isArray(update.entries) ? update.entries : [],
          },
        ],
      });
      return null;
    }
    if (kind === 'session_info_update') {
      return {
        conversationTitle: update?.title || update?.sessionInfo?.title || '',
      };
    }
    if (kind === 'config_option_update') {
      pushMessage(transcript, {
        kind: 'system',
        title: 'Setting updated',
        tone: 'muted',
        blocks: [
          {
            type: 'keyValue',
            entries: [
              { label: 'Key', value: String(update.key || '') },
              { label: 'Value', value: String(update.value || '') },
            ].filter((entry) => entry.value),
          },
        ],
      });
      return null;
    }
    pushMessage(transcript, {
      kind: 'system',
      title: humanizeUpdateKind(kind),
      tone: 'muted',
      blocks: [
        {
          type: 'details',
          title: 'Payload',
          text: stringifyDetails(update),
          open: false,
        },
      ],
    });
    return null;
  }

  function renderRichText(text) {
    const fragment = document.createDocumentFragment ? document.createDocumentFragment() : document.createElement('div');
    const source = String(text || '');
    const pattern = /```([\w-]+)?\n?([\s\S]*?)```/g;
    let lastIndex = 0;
    let match;
    while ((match = pattern.exec(source))) {
      const before = source.slice(lastIndex, match.index);
      appendParagraphs(fragment, before);
      const pre = document.createElement('pre');
      pre.className = 'acp-code-block';
      if (match[1]) {
        pre.dataset.language = match[1];
      }
      const code = document.createElement('code');
      code.textContent = match[2].trim();
      pre.appendChild(code);
      fragment.appendChild(pre);
      lastIndex = pattern.lastIndex;
    }
    appendParagraphs(fragment, source.slice(lastIndex));
    return fragment;
  }

  function appendParagraphs(parent, text) {
    const chunks = String(text || '')
      .split(/\n{2,}/)
      .map((part) => part.trim())
      .filter(Boolean);
    chunks.forEach((chunk) => {
      const paragraph = document.createElement('p');
      paragraph.className = 'acp-rich-paragraph';
      paragraph.textContent = chunk;
      parent.appendChild(paragraph);
    });
  }

  function renderBlock(block) {
    if (!block) return null;
    if (block.type === 'text') {
      const wrapper = document.createElement('div');
      wrapper.className = 'acp-block acp-block--text';
      wrapper.appendChild(renderRichText(block.text || ''));
      return wrapper;
    }
    if (block.type === 'plan') {
      const list = document.createElement('ol');
      list.className = 'acp-plan-list';
      (block.entries || []).forEach((entry) => {
        const item = document.createElement('li');
        const line = [entry.content || '', entry.status || '', entry.priority || ''].filter(Boolean).join(' · ');
        item.textContent = line || 'Pending step';
        list.appendChild(item);
      });
      return list;
    }
    if (block.type === 'tags') {
      const row = document.createElement('div');
      row.className = 'acp-tag-row';
      (block.items || []).forEach((item) => {
        const tag = document.createElement('span');
        tag.className = 'acp-tag';
        tag.textContent = item.label || item.name || '';
        if (item.title) tag.title = item.title;
        row.appendChild(tag);
      });
      return row;
    }
    if (block.type === 'keyValue') {
      const grid = document.createElement('dl');
      grid.className = 'acp-key-value';
      (block.entries || []).forEach((entry) => {
        const term = document.createElement('dt');
        term.textContent = entry.label || '';
        const value = document.createElement('dd');
        value.textContent = entry.value || '';
        grid.append(term, value);
      });
      return grid;
    }
    if (block.type === 'details') {
      const details = document.createElement('details');
      details.className = 'acp-details';
      details.open = Boolean(block.open);
      const summary = document.createElement('summary');
      summary.textContent = block.title || 'Details';
      const pre = document.createElement('pre');
      pre.className = 'acp-details-body';
      pre.textContent = block.text || '';
      details.append(summary, pre);
      return details;
    }
    return null;
  }

  function renderMessage(message) {
    const article = document.createElement('article');
    article.className = `acp-message acp-message--${message.kind}`;
    article.dataset.kind = message.kind;

    const bubble = document.createElement('div');
    bubble.className = message.kind === 'user' || message.kind === 'assistant' ? 'acp-bubble' : 'acp-event-card';

    if (message.title || message.status || message.meta) {
      const header = document.createElement('div');
      header.className = 'acp-message-meta';
      const title = document.createElement('strong');
      title.textContent = message.title || (message.kind === 'assistant' ? 'Assistant' : message.kind === 'user' ? 'You' : 'Update');
      header.appendChild(title);
      if (message.status || message.meta) {
        const meta = document.createElement('div');
        meta.className = 'acp-meta-stack';
        if (message.meta) {
          const metaText = document.createElement('span');
          metaText.className = 'acp-message-meta-text';
          metaText.textContent = message.meta;
          meta.appendChild(metaText);
        }
        if (message.status) {
          const badge = document.createElement('span');
          badge.className = 'acp-status-pill';
          badge.dataset.tone = message.tone || 'info';
          badge.textContent = message.status.replace(/_/g, ' ');
          meta.appendChild(badge);
        }
        header.appendChild(meta);
      }
      bubble.appendChild(header);
    }

    message.blocks.forEach((block) => {
      const node = renderBlock(block);
      if (node) bubble.appendChild(node);
    });

    article.appendChild(bubble);
    return article;
  }

  function getPreviewText(transcript) {
    for (let index = transcript.messages.length - 1; index >= 0; index -= 1) {
      const message = transcript.messages[index];
      if (!message) continue;
      if (message.kind === 'assistant' || message.kind === 'user') {
        const textBlock = message.blocks.find((block) => block.type === 'text' && block.text);
        if (textBlock) return excerpt(textBlock.text);
      }
      if (message.kind === 'tool') {
        return excerpt(`${message.title || 'Tool call'} · ${message.status || 'running'}`);
      }
    }
    return '';
  }

  function buildCommandItems(commands) {
    return (Array.isArray(commands) ? commands : []).map((command) => ({
      label: `/${command.name || 'command'}`,
      title: command.description || '',
    }));
  }

  function isTranscriptBearingUpdate(update) {
    const kind = update?.sessionUpdate || '';
    return ![
      '',
      'available_commands_update',
      'current_mode_update',
      'usage_update',
      'session_info_update',
    ].includes(kind);
  }

  global.SpritzACPRender = {
    buildCommandItems,
    createTranscript,
    applySessionUpdate,
    finalizeStreaming,
    getPreviewText,
    hydrateTranscript,
    isTranscriptBearingUpdate,
    renderMessage,
    serializeTranscript,
  };
})(window);
