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
      thinkingChunks: [] as Array<{ kind: 'thought' | 'tool' | 'done'; text: string; url?: string; toolKind?: 'fetch' | 'search' | 'generic'; _toolCallId?: string; toolName?: string; status?: string; input?: string; result?: string }>,
      thinkingActive: false,
      thinkingInsertIndex: 0,
      thinkingStartTime: 0,
      thinkingElapsedSeconds: 0,
    };
  }

  function rebuildToolCallIndex(transcript) {
    transcript.toolCallIndex = new Map();
    transcript.messages.forEach((message, index) => {
      if (message?.type === 'tool' && message.toolCallId) {
        transcript.toolCallIndex.set(message.toolCallId, index);
      }
    });
    return transcript;
  }

  function serializeTranscript(transcript) {
    return {
      messages: Array.isArray(transcript?.messages)
        ? transcript.messages.map((message) => {
            if (message.type === 'thinking_done') {
              return {
                type: 'thinking_done',
                seconds: message.seconds || 0,
                chunks: Array.isArray(message.chunks) ? message.chunks : [],
              };
            }
            return {
              id: message.id || '',
              type: message.type || 'system',
              title: message.title || '',
              status: message.status || '',
              tone: message.tone || '',
              meta: message.meta || '',
              blocks: Array.isArray(message.blocks) ? message.blocks.map(function (b) {
                var copy = Object.assign({}, b);
                delete copy._renderedLength;
                return copy;
              }) : [],
              streaming: Boolean(message.streaming),
              toolCallId: message.toolCallId || '',
            };
          })
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

  function sanitizeHydratedBlock(type, block) {
    if (!block || typeof block !== 'object') return null;
    if (block.type === 'text') {
      const htmlError = detectHtmlErrorDocument(block.text);
      if (htmlError) {
        return type === 'tool'
          ? { ...block, type: 'details', title: 'Result', text: htmlError.text, open: false }
          : null;
      }
      var t = String(block.text || '');
      return { ...block, text: t, _renderedLength: t.length };
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
    if (message?.type === 'thinking_done') {
      return {
        type: 'thinking_done',
        seconds: message.seconds || 0,
        chunks: Array.isArray(message.chunks) ? message.chunks : [],
      };
    }
    const type = message?.type || message?.kind || 'system';
    const hadHtmlError = Array.isArray(message?.blocks)
      ? message.blocks.some((block) => {
          if (!block || typeof block !== 'object') return false;
          const value = block.type === 'text' || block.type === 'details' ? block.text : '';
          return Boolean(detectHtmlErrorDocument(value));
        })
      : false;
    const blocks = Array.isArray(message?.blocks)
      ? message.blocks.map((block) => sanitizeHydratedBlock(type, block)).filter(Boolean)
      : [];
    if (!blocks.length && (type === 'assistant' || type === 'user')) {
      return null;
    }
    return {
      id: message?.id || createId(type || 'message'),
      type,
      title: message?.title || '',
      status:
        type === 'tool' && hadHtmlError && (!message?.status || message.status === 'completed')
          ? 'failed'
          : message?.status || '',
      tone:
        type === 'tool' && hadHtmlError
          ? 'danger'
          : message?.tone || '',
      meta: message?.meta || '',
      blocks,
      streaming: Boolean(message?.streaming),
      toolCallId: message?.toolCallId || '',
      historyMessageId: message?.historyMessageId || '',
    };
  }

  function transcriptContainsHtmlError(transcript) {
    const messages = Array.isArray(transcript?.messages) ? transcript.messages : [];
    return messages.some((message) => {
      const blocks = Array.isArray(message?.blocks) ? message.blocks : [];
      return blocks.some((block) => {
        if (!block || typeof block !== 'object') return false;
        if (block.type !== 'text' && block.type !== 'details') return false;
        return Boolean(detectHtmlErrorDocument(block.text));
      });
    });
  }

  function pushMessage(transcript, message) {
    transcript.messages.push({
      id: message.id || createId(message.type || 'message'),
      type: message.type,
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

  function createTextBlocks(text, animated) {
    var normalized = String(text || '');
    if (!normalized) return [];
    var block = { type: 'text', text: normalized, _renderedLength: normalized.length };
    if (animated) block._renderedLength = 0;
    return [block];
  }

  function appendHistoricalText(transcript, type, text, messageKey = '') {
    const value = String(text || '');
    if (!value) return;
    const normalizedKey = String(messageKey || '').trim();
    const last = transcript.messages[transcript.messages.length - 1];
    if (normalizedKey && last && last.type === type && last.historyMessageId === normalizedKey) {
      const textBlock = last.blocks.find((block) => block.type === 'text');
      if (textBlock) {
        textBlock.text += value;
        textBlock._renderedLength = textBlock.text.length;
      } else {
        last.blocks.push({ type: 'text', text: value, _renderedLength: value.length });
      }
      return;
    }
    var histBlocks = createTextBlocks(value, false);
    histBlocks.forEach(function (b) { b._renderedLength = b.text.length; });
    pushMessage(transcript, {
      type,
      streaming: false,
      historyMessageId: normalizedKey,
      blocks: histBlocks,
    });
  }

  function appendStreamingText(transcript, type, text) {
    var chunk = String(text || '');
    if (!chunk) return;
    var last = transcript.messages[transcript.messages.length - 1];
    if (last && last.type === type && last.streaming) {
      var textBlock = last.blocks.find(function (b) { return b.type === 'text'; });
      if (textBlock) {
        textBlock.text += chunk;
      } else {
        last.blocks.push({ type: 'text', text: chunk, _renderedLength: 0 });
      }
      return;
    }
    pushMessage(transcript, {
      type,
      streaming: true,
      blocks: createTextBlocks(chunk, true),
    });
  }

  function finalizeStreaming(transcript) {
    transcript.messages.forEach((message) => {
      if (message.type === 'assistant' || message.type === 'user') {
        message.streaming = false;
        // Clean up animation classes
        if (message._el) {
          message._el.querySelectorAll('.ft-animate').forEach(function(el) {
            el.classList.remove('ft-animate');
          });
        }
        // Clear cached DOM so renderThread re-renders with full markdown
        delete message._el;
      }
    });
    if (transcript.thinkingActive && transcript.thinkingStartTime) {
      transcript.thinkingElapsedSeconds = Math.round((Date.now() - transcript.thinkingStartTime) / 1000);
    }
    transcript.thinkingActive = false;
    // Bake the completed thinking block into the message list so it persists
    // after the next turn resets thinkingChunks.
    if (transcript.thinkingChunks.length > 0) {
      const seconds = transcript.thinkingElapsedSeconds || 0;
      const insertIdx = transcript.thinkingInsertIndex || transcript.messages.length;
      const thinkingMessage = {
        type: 'thinking_done' as const,
        seconds,
        chunks: transcript.thinkingChunks.slice(),
      };
      transcript.messages.splice(insertIdx, 0, thinkingMessage);
      rebuildToolCallIndex(transcript);
    }
    // Clear live thinking state
    transcript.thinkingChunks = [];
  }

  /** Bake any leftover thinking chunks after historical replay ends. */
  function finalizeHistoricalThinking(transcript) {
    if (transcript.thinkingChunks.length > 0) {
      const insertIdx = transcript.thinkingInsertIndex || transcript.messages.length;
      transcript.messages.splice(insertIdx, 0, {
        type: 'thinking_done' as const,
        seconds: 0,
        chunks: transcript.thinkingChunks.slice(),
      });
      rebuildToolCallIndex(transcript);
    }
    transcript.thinkingChunks = [];
    transcript.thinkingActive = false;
    transcript.thinkingInsertIndex = 0;
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
      type: 'tool',
      title,
      status,
      tone: status === 'completed' ? 'success' : status === 'failed' ? 'danger' : 'info',
      meta: update.type || existing?.meta || '',
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

  function humanizeUpdateType(type) {
    return String(type || 'Update')
      .replace(/_/g, ' ')
      .replace(/\b\w/g, (match) => match.toUpperCase());
  }

  function applySessionUpdate(
    transcript,
    update,
    options: {
      historical?: boolean;
    } = {},
  ) {
    const type = update?.sessionUpdate || 'unknown';
    const historical = Boolean(options.historical);
    if (type === 'tool_call' || type === 'tool_call_update') {
      console.log('[ACP Debug] tool event received:', type, JSON.stringify(update).slice(0, 200));
    }
    if (type === 'user_message_chunk') {
      const text = extractACPText(update.content);
      if (/^\s*<(?:command-name|command-message|command-args|local-command-stdout)\b/i.test(text)) {
        return null;
      }
      const htmlError = detectHtmlErrorDocument(text);
      if (htmlError) {
        return {
          toast: {
            type: 'error',
            message: htmlError.text,
          },
        };
      }
      // Bake any accumulated historical thinking from the previous turn
      if (historical && transcript.thinkingChunks.length > 0) {
        const insertIdx = transcript.thinkingInsertIndex || transcript.messages.length;
        transcript.messages.splice(insertIdx, 0, {
          type: 'thinking_done' as const,
          seconds: 0,
          chunks: transcript.thinkingChunks.slice(),
        });
        rebuildToolCallIndex(transcript);
      }
      // Reset thinking state for the new turn
      transcript.thinkingChunks = [];
      transcript.thinkingActive = false;
      transcript.thinkingInsertIndex = 0;
      transcript.thinkingStartTime = 0;
      transcript.thinkingElapsedSeconds = 0;
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
    if (type === 'agent_thought_chunk') {
      const text = extractACPText(update.content);
      if (!text || /^\s*<(?:command-name|command-message|command-args|local-command-stdout)\b/i.test(text)) {
        return null;
      }
      if (!transcript.thinkingActive && !transcript.thinkingChunks.length) {
        transcript.thinkingInsertIndex = transcript.messages.length;
        if (!historical) transcript.thinkingStartTime = Date.now();
      }
      if (!historical) transcript.thinkingActive = true;
      // Merge consecutive thought fragments into one entry
      const last = transcript.thinkingChunks[transcript.thinkingChunks.length - 1];
      if (last && last.kind === 'thought') {
        last.text += text;
      } else {
        transcript.thinkingChunks.push({ kind: 'thought', text: text });
      }
      return null;
    }
    if (type === 'agent_message_chunk') {
      const text = extractACPText(update.content);
      if (/^\s*<(?:command-name|command-message|command-args|local-command-stdout)\b/i.test(text)) {
        return null;
      }
      const htmlError = detectHtmlErrorDocument(text);
      if (htmlError) {
        return {
          toast: {
            type: 'error',
            message: htmlError.text,
          },
        };
      }
      // Thinking stays active during response streaming;
      // finalizeStreaming() sets thinkingActive = false when done.
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
    if (type === 'tool_call' || type === 'tool_call_update') {
      // Initialize thinking state if needed
      if (!transcript.thinkingActive && !transcript.thinkingChunks.length) {
        transcript.thinkingInsertIndex = transcript.messages.length;
        if (!historical) transcript.thinkingStartTime = Date.now();
      }
      if (!historical) transcript.thinkingActive = true;

      const toolCallId = update.toolCallId || createId('tool');
      const toolName = update.name || update.title || update.type || 'Tool call';
      const status = update.status || 'pending';
      const inputText = stringifyDetails(update.rawInput);
      const normalizedResult = normalizeToolResultText(update.rawOutput);
      const resultText = normalizedResult.text;
      const url = extractToolUrl(update);

      // Classify tool kind by name
      const nameLower = (toolName || '').toLowerCase().replace(/[-_]/g, '');
      let toolKind: 'fetch' | 'search' | 'generic' = 'generic';
      if (/search|query|grep|glob|find/.test(nameLower)) toolKind = 'search';
      else if (/fetch|browse|readpage|navigate|webfetch/.test(nameLower)) toolKind = 'fetch';

      // Upsert into thinkingChunks
      const existing = transcript.thinkingChunks.find((c) => c._toolCallId === toolCallId);
      if (existing) {
        existing.status = status;
        if (update.name || update.title) existing.toolName = toolName;
        if (inputText) existing.input = inputText;
        if (resultText) existing.result = resultText;
        if (url) existing.url = url;
        existing.text = url || existing.toolName || toolName;
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

      // Toast on error
      if (!historical && normalizedResult.isError && normalizedResult.text) {
        return {
          toast: {
            type: 'error',
            message: normalizedResult.text,
          },
        };
      }
      return null;
    }
    if (type === 'available_commands_update') {
      transcript.availableCommands = Array.isArray(update.availableCommands) ? update.availableCommands : [];
      return null;
    }
    if (type === 'current_mode_update') {
      transcript.currentMode = String(update.mode || update.currentMode || '').trim();
      return null;
    }
    if (type === 'usage_update') {
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
    if (type === 'plan') {
      pushMessage(transcript, {
        type: 'plan',
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
    if (type === 'session_info_update') {
      return {
        conversationTitle: update?.title || update?.sessionInfo?.title || '',
      };
    }
    if (type === 'config_option_update') {
      pushMessage(transcript, {
        type: 'system',
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
    if (type === 'agent_thought_chunk') {
      const text = extractACPText(update.content);
      if (!text) return null;
      const last = transcript.messages[transcript.messages.length - 1];
      if (last && last.type === 'thinking') {
        const textBlock = last.blocks.find((block) => block.type === 'text');
        if (textBlock) {
          textBlock.text += text;
        } else {
          last.blocks.push({ type: 'text', text });
        }
      } else {
        pushMessage(transcript, {
          type: 'thinking',
          title: 'Thinking',
          tone: 'muted',
          blocks: [{ type: 'text', text }],
        });
      }
      return null;
    }
    // Silently ignore noisy internal updates
    const silentTypes = ['heartbeat', 'ping', 'pong', 'ack'];
    if (silentTypes.includes(type)) return null;
    pushMessage(transcript, {
      type: 'system',
      title: humanizeUpdateType(type),
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

  /* ── FlowToken-inspired animated markdown renderer (vanilla JS) ── */

  function renderRichText(text, prevLength) {
    var fragment = document.createDocumentFragment();
    var source = String(text || '');
    var cutoff = typeof prevLength === 'number' ? prevLength : source.length;
    var pattern = /```([\w-]*)\n?([\s\S]*?)```/g;
    var lastIndex = 0;
    var match;
    while ((match = pattern.exec(source))) {
      var before = source.slice(lastIndex, match.index);
      appendParagraphs(fragment, before, cutoff, lastIndex);
      var codeText = match[2].trim();
      var wrapper = document.createElement('div');
      wrapper.className = 'acp-code-wrapper';
      if (match.index >= cutoff) wrapper.classList.add('ft-token-block');
      var pre = document.createElement('pre');
      pre.className = 'acp-code-block';
      var lang = match[1] || '';
      if (lang) pre.dataset.language = lang;
      var code = document.createElement('code');
      code.textContent = codeText;
      pre.appendChild(code);
      wrapper.appendChild(pre);
      // Async shiki highlighting — replaces plain code after load
      if (typeof (window as any)._shikiHighlight === 'function') {
        (function(w, ct, ln) {
          (window as any)._shikiHighlight(ct, ln || 'text').then(function(html) {
            if (html && w.parentNode) {
              w.innerHTML = html;
            }
          });
        })(wrapper, codeText, lang);
      }
      fragment.appendChild(wrapper);
      lastIndex = pattern.lastIndex;
    }
    appendParagraphs(fragment, source.slice(lastIndex), cutoff, lastIndex);
    return fragment;
  }

  function escapeHtml(str) {
    return str.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;').replace(/"/g, '&quot;');
  }

  function renderInlineMarkdown(text) {
    var html = escapeHtml(text);
    html = html.replace(/`([^`]+)`/g, '<code class="acp-inline-code">$1</code>');
    html = html.replace(/\*\*([^*]+)\*\*/g, '<strong>$1</strong>');
    html = html.replace(/\*([^*]+)\*/g, '<em>$1</em>');
    html = html.replace(/~~([^~]+)~~/g, '<del>$1</del>');
    html = html.replace(/\[([^\]]+)\]\(([^)]+)\)/g, '<a href="$2" target="_blank" rel="noopener">$1</a>');
    return html;
  }

  /**
   * Render inline markdown into parent. No per-character animation —
   * block-level fade-in is applied by the caller via ft-token-block class.
   */
  function appendInlineContent(parent, text) {
    parent.innerHTML = renderInlineMarkdown(text);
  }

  function appendParagraphs(parent, text, cutoff, offset) {
    var source = String(text || '').trim();
    if (!source) return;
    var globalCutoff = typeof cutoff === 'number' ? cutoff : Infinity;
    var baseOffset = typeof offset === 'number' ? offset : 0;
    var lines = source.split('\n');
    var charPos = baseOffset;
    var i = 0;
    while (i < lines.length) {
      var line = lines[i].trim();
      if (!line) { charPos += lines[i].length + 1; i++; continue; }
      var lineStart = charPos;

      // headings
      var headingMatch = line.match(/^(#{1,4})\s+(.+)/);
      if (headingMatch) {
        var level = Math.min(headingMatch[1].length + 1, 6);
        var heading = document.createElement('h' + level);
        heading.className = 'acp-md-heading';
        if (lineStart >= globalCutoff) heading.classList.add('ft-token-block');
        appendInlineContent(heading, headingMatch[2]);
        parent.appendChild(heading);
        charPos += lines[i].length + 1;
        i++;
        continue;
      }

      // blockquote
      if (/^>\s?/.test(line)) {
        var bq = document.createElement('blockquote');
        bq.className = 'acp-blockquote';
        var bqStart = charPos;
        var bqLines = [];
        while (i < lines.length && /^>\s?/.test(lines[i].trim())) {
          bqLines.push(lines[i].trim().replace(/^>\s?/, ''));
          charPos += lines[i].length + 1;
          i++;
        }
        var bqText = bqLines.join('\n');
        var bqP = document.createElement('p');
        if (bqStart >= globalCutoff) bq.classList.add('ft-token-block');
        appendInlineContent(bqP, bqText);
        bq.appendChild(bqP);
        parent.appendChild(bq);
        continue;
      }

      // table (GFM) — use textContent for cells (no TreeWalker animation)
      if (/^\|.+\|/.test(line) && i + 1 < lines.length && /^\|[\s:|-]+\|/.test(lines[i + 1]?.trim())) {
        var tableStart = charPos;
        var table = document.createElement('table');
        table.className = 'acp-table';
        var headerCells = line.split('|').slice(1, -1).map(function (c) { return c.trim(); });
        var thead = document.createElement('thead');
        var headRow = document.createElement('tr');
        headerCells.forEach(function (cell) {
          var th = document.createElement('th');
          th.innerHTML = renderInlineMarkdown(cell);
          headRow.appendChild(th);
        });
        thead.appendChild(headRow);
        table.appendChild(thead);
        charPos += lines[i].length + 1;
        i++;
        var sepCells = lines[i].trim().split('|').slice(1, -1);
        var aligns = sepCells.map(function (c) {
          var t = c.trim();
          if (t.startsWith(':') && t.endsWith(':')) return 'center';
          if (t.endsWith(':')) return 'right';
          return 'left';
        });
        charPos += lines[i].length + 1;
        i++;
        var tbody = document.createElement('tbody');
        while (i < lines.length && /^\|.+\|/.test(lines[i].trim())) {
          var cells = lines[i].trim().split('|').slice(1, -1).map(function (c) { return c.trim(); });
          var tr = document.createElement('tr');
          cells.forEach(function (cell, ci) {
            var td = document.createElement('td');
            if (aligns[ci]) td.style.textAlign = aligns[ci];
            td.innerHTML = renderInlineMarkdown(cell);
            tr.appendChild(td);
          });
          tbody.appendChild(tr);
          charPos += lines[i].length + 1;
          i++;
        }
        if (tableStart >= globalCutoff) table.classList.add('ft-token-block');
        table.appendChild(tbody);
        parent.appendChild(table);
        continue;
      }

      // unordered list
      if (/^[-*]\s+/.test(line)) {
        var ul = document.createElement('ul');
        ul.className = 'acp-md-list';
        var ulStart = charPos;
        while (i < lines.length && /^[-*]\s+/.test(lines[i].trim())) {
          var li = document.createElement('li');
          var liText = lines[i].trim().replace(/^[-*]\s+/, '');
          if (charPos >= globalCutoff) li.classList.add('ft-token-block');
          appendInlineContent(li, liText);
          ul.appendChild(li);
          charPos += lines[i].length + 1;
          i++;
        }
        parent.appendChild(ul);
        continue;
      }

      // ordered list
      if (/^\d+[.)]\s+/.test(line)) {
        var ol = document.createElement('ol');
        ol.className = 'acp-md-list';
        while (i < lines.length && /^\d+[.)]\s+/.test(lines[i].trim())) {
          var oli = document.createElement('li');
          var olText = lines[i].trim().replace(/^\d+[.)]\s+/, '');
          if (charPos >= globalCutoff) oli.classList.add('ft-token-block');
          appendInlineContent(oli, olText);
          ol.appendChild(oli);
          charPos += lines[i].length + 1;
          i++;
        }
        parent.appendChild(ol);
        continue;
      }

      // horizontal rule
      if (/^[-*_]{3,}\s*$/.test(line)) {
        var hr = document.createElement('hr');
        if (lineStart >= globalCutoff) hr.classList.add('ft-token-block');
        parent.appendChild(hr);
        charPos += lines[i].length + 1;
        i++;
        continue;
      }

      // regular paragraph
      var paraLines = [];
      var paraStart = charPos;
      while (i < lines.length) {
        var l = lines[i].trim();
        if (!l || /^#{1,4}\s/.test(l) || /^[-*]\s+/.test(l) || /^\d+[.)]\s+/.test(l) || /^[-*_]{3,}\s*$/.test(l) || /^>\s?/.test(l) || /^\|.+\|/.test(l)) break;
        paraLines.push(l);
        charPos += lines[i].length + 1;
        i++;
      }
      if (paraLines.length) {
        var p = document.createElement('p');
        p.className = 'acp-rich-paragraph';
        if (paraStart >= globalCutoff) p.classList.add('ft-token-block');
        var paraText = paraLines.join('\n');
        appendInlineContent(p, paraText);
        parent.appendChild(p);
      }
    }
  }

  /**
   * Strip incomplete markdown syntax so the parser never renders partial raw syntax.
   * Handles: unclosed fences, tables, bold, italic, backticks, strikethrough, HTML tags.
   */
  function stripIncompleteMd(text) {
    // 1. Unclosed fenced code block
    var fenceMatches = text.match(/```/g);
    if (fenceMatches && fenceMatches.length % 2 !== 0) {
      var lastFence = text.lastIndexOf('```');
      text = text.substring(0, lastFence);
    }
    // 2. Incomplete table row
    var lines = text.split('\n');
    var lastLine = lines[lines.length - 1];
    if (lastLine && lastLine.indexOf('|') !== -1 && !lastLine.trim().endsWith('|')) {
      lines.pop();
      text = lines.join('\n');
    }
    // 3. Unclosed bold ** (check full text, not just tail)
    var boldCount = (text.match(/\*\*/g) || []).length;
    if (boldCount % 2 !== 0) {
      text = text.substring(0, text.lastIndexOf('**'));
    }
    // 4. Unclosed italic * (after removing paired **)
    var cleanText = text.replace(/\*\*/g, '');
    var italicCount = (cleanText.match(/\*/g) || []).length;
    if (italicCount % 2 !== 0) {
      // Find the last lone * in the original text
      for (var ii = text.length - 1; ii >= 0; ii--) {
        if (text[ii] === '*' && (ii === 0 || text[ii - 1] !== '*') && (ii === text.length - 1 || text[ii + 1] !== '*')) {
          text = text.substring(0, ii);
          break;
        }
      }
    }
    // 5. Unclosed backtick (check full text — exclude triple backticks)
    var btText = text.replace(/```/g, '');
    var btCount = (btText.match(/`/g) || []).length;
    if (btCount % 2 !== 0) {
      // Find last lone backtick (not part of ```)
      for (var bi = text.length - 1; bi >= 0; bi--) {
        if (text[bi] === '`' && !(text[bi - 1] === '`' && text[bi - 2] === '`') && !(text[bi + 1] === '`' && text[bi + 2] === '`')) {
          text = text.substring(0, bi);
          break;
        }
      }
    }
    // 6. Unclosed strikethrough ~~ (check full text)
    var strikeCount = (text.match(/~~/g) || []).length;
    if (strikeCount % 2 !== 0) {
      text = text.substring(0, text.lastIndexOf('~~'));
    }
    // 7. Incomplete HTML tag
    var lastOpenBracket = text.lastIndexOf('<');
    if (lastOpenBracket !== -1 && text.substring(lastOpenBracket).indexOf('>') === -1) {
      text = text.substring(0, lastOpenBracket);
    }
    // 8. Incomplete markdown link [text](url) — strip if unclosed
    // Match incomplete: [text](url  or  [text](  or  [text]( or [text
    var lastOpenBracket2 = text.lastIndexOf('[');
    if (lastOpenBracket2 !== -1) {
      var afterBracket = text.substring(lastOpenBracket2);
      // Full valid link: [text](url)
      if (!/\[[^\]]*\]\([^)]*\)/.test(afterBracket)) {
        text = text.substring(0, lastOpenBracket2);
      }
    }
    // 9. Incomplete heading at end (# without content on last line)
    var lastNewline = text.lastIndexOf('\n');
    var trailingLine = text.substring(lastNewline + 1).trim();
    if (/^#{1,6}\s*$/.test(trailingLine)) {
      text = text.substring(0, lastNewline === -1 ? 0 : lastNewline);
    }
    return text;
  }

  // WeakMap to track text lengths for delta animation
  var _textLengths = new WeakMap();

  /**
   * Incremental streaming with morphdom callbacks.
   * morphdom diffs old DOM vs new — only touches what changed.
   * onNodeAdded: animates brand new elements.
   * onElUpdated: wraps only NEW text delta in animated span.
   * Already-rendered text stays untouched at full opacity.
   */
  function appendStreamingChunk(message) {
    try {
      if (!message || !message._el || !message.streaming) return false;
      var textBlock = null;
      for (var bi = 0; bi < (message.blocks || []).length; bi++) {
        if (message.blocks[bi].type === 'text') { textBlock = message.blocks[bi]; break; }
      }
      if (!textBlock) return false;
      var raw = textBlock.text || '';
      var prevLen = typeof textBlock._renderedLength === 'number' ? textBlock._renderedLength : 0;
      if (raw.length === prevLen) return true;
      var container = message._el.querySelector('.acp-block--text');
      if (!container) return false;

      var safeRaw = stripIncompleteMd(raw);
      if (!safeRaw.trim()) return true;

      // Render markdown to a temp element
      var temp = document.createElement('div');
      temp.appendChild(renderRichText(safeRaw, safeRaw.length));

      if (typeof (window as any).morphdom === 'function') {
        // Snapshot text lengths before morphing
        container.querySelectorAll('*').forEach(function(el) {
          _textLengths.set(el, (el.textContent || '').length);
        });

        // Use morphdom to diff — only patches what changed
        (window as any).morphdom(container, temp, {
          childrenOnly: true,

          onBeforeElUpdated: function(fromEl, toEl) {
            if (fromEl.classList && fromEl.classList.contains('ft-animate')) {
              toEl.classList.add('ft-animate');
            }
            return true;
          },

          onElUpdated: function(el) {
            var pLen = _textLengths.get(el) || 0;
            var currText = el.textContent || '';
            if (currText.length > pLen) {
              if (el.children.length === 0) {
                var oldText = currText.substring(0, pLen);
                var newText = currText.substring(pLen);
                if (newText.trim()) {
                  el.innerHTML = '';
                  if (oldText) {
                    var oldSpan = document.createElement('span');
                    oldSpan.textContent = oldText;
                    el.appendChild(oldSpan);
                  }
                  var newSpan = document.createElement('span');
                  newSpan.className = 'ft-animate';
                  newSpan.textContent = newText;
                  el.appendChild(newSpan);
                }
              }
            }
            _textLengths.set(el, currText.length);
          },

          onNodeAdded: function(node) {
            if (node.nodeType === 1) {
              node.classList.add('ft-animate');
              node.querySelectorAll('*').forEach(function(child) {
                child.classList.add('ft-animate');
              });
            }
            return node;
          },
        });
      } else {
        // Fallback: no morphdom — just replace children directly
        // Still streams incrementally, just without fade animation
        while (container.firstChild) container.removeChild(container.firstChild);
        while (temp.firstChild) container.appendChild(temp.firstChild);
      }

      textBlock._renderedLength = raw.length;
      return true;
    } catch (err) {
      // If anything fails, return false so renderThread falls back to full re-render
      console.warn('[appendStreamingChunk] error, falling back:', err);
      return false;
    }
  }

  function renderBlock(block, streaming) {
    if (!block) return null;
    if (block.type === 'text') {
      var htmlError = detectHtmlErrorDocument(block.text);
      var raw = htmlError ? htmlError.text : block.text || '';
      var wrapper = document.createElement('div');
      wrapper.className = 'acp-block acp-block--text';

      if (streaming) {
        // Streaming: full markdown, no per-element animation — scroll handles reveal
        var safeRaw = stripIncompleteMd(raw);
        if (safeRaw.trim()) {
          wrapper.appendChild(renderRichText(safeRaw, safeRaw.length));
        }
        block._renderedLength = raw.length;
      } else {
        // Finalized: full rich markdown
        wrapper.appendChild(renderRichText(raw, raw.length));
        block._renderedLength = raw.length;
      }

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
      const htmlError = detectHtmlErrorDocument(block.text);
      const details = document.createElement('details');
      details.className = 'acp-details';
      details.open = htmlError ? false : Boolean(block.open);
      const summary = document.createElement('summary');
      summary.textContent = block.title || 'Details';
      const pre = document.createElement('pre');
      pre.className = 'acp-details-body';
      pre.textContent = htmlError ? htmlError.text : block.text || '';
      details.append(summary, pre);
      return details;
    }
    return null;
  }

  function renderMessage(message) {
    if (message.type === 'thinking') {
      const article = document.createElement('article');
      article.className = 'acp-message acp-message--thinking';
      article.dataset.type = 'thinking';
      const details = document.createElement('details');
      details.className = 'acp-thinking-block';
      const summary = document.createElement('summary');
      summary.className = 'acp-thinking-summary';
      summary.textContent = 'Thinking…';
      const content = document.createElement('div');
      content.className = 'acp-thinking-content';
      const textBlock = message.blocks.find((b) => b.type === 'text');
      if (textBlock) {
        content.appendChild(renderRichText(textBlock.text || '', 0));
      }
      details.append(summary, content);
      article.appendChild(details);
      return article;
    }

    if (message.type === 'thinking_done') {
      const container = document.createElement('div');
      container.className = 'acp-thinking-baked acp-thinking--done';

      const header = document.createElement('button');
      header.type = 'button';
      header.className = 'acp-thinking-header';
      header.setAttribute('aria-expanded', 'false');

      const labelWrap = document.createElement('span');
      labelWrap.className = 'acp-thinking-label-wrap';
      const label = document.createElement('span');
      label.className = 'acp-thinking-label';
      label.textContent = message.seconds > 0
        ? `Thought for ${message.seconds} second${message.seconds !== 1 ? 's' : ''}`
        : 'Thought';
      labelWrap.appendChild(label);

      const chevron = document.createElement('span');
      chevron.className = 'acp-thinking-chevron';
      chevron.innerHTML = CHEVRON_SVG;

      header.append(labelWrap, chevron);

      const body = document.createElement('div');
      body.className = 'acp-thinking-body';
      const timeline = document.createElement('div');
      timeline.className = 'acp-thinking-timeline';

      timeline.appendChild(buildGroupedTimeline(message.chunks || [], true));

      body.appendChild(timeline);
      container.append(header, body);

      header.addEventListener('click', () => {
        const expanded = container.classList.toggle('acp-thinking--expanded');
        header.setAttribute('aria-expanded', String(expanded));
      });

      return container;
    }

    const article = document.createElement('article');
    article.className = `acp-message acp-message--${message.type}`;
    article.dataset.type = message.type;

    const bubble = document.createElement('div');
    bubble.className = message.type === 'user' || message.type === 'assistant' ? 'acp-bubble' : 'acp-event-card';

    if (message.title || message.status || message.meta) {
      const header = document.createElement('div');
      header.className = 'acp-message-meta';
      const title = document.createElement('strong');
      title.textContent = message.title || (message.type === 'assistant' ? 'Assistant' : message.type === 'user' ? 'You' : 'Update');
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
      const node = renderBlock(block, message.streaming);
      if (node) bubble.appendChild(node);
    });

    article.appendChild(bubble);
    return article;
  }

  let _thinkingEl: HTMLElement | null = null;
  let _thinkingRenderedCount = 0;
  let _thinkingWordInterval: ReturnType<typeof setInterval> | null = null;
  let _thinkingWordIndex = 0;
  let _thinkingPrevToolCount = 0;
  let _thinkingPrevThoughtCount = 0;
  const THINKING_WORDS = ['Thinking', 'Planning', 'Refining'];

  const MORPH_SVG =
    '<svg class="acp-thinking-icon" width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round">' +
    '<path class="acp-thinking-morph" d="M 12 8 C 14.21 8 16 9.79 16 12 C 16 14.21 14.21 16 12 16 C 9.79 16 8 14.21 8 12 C 8 9.79 9.79 8 12 8 Z">' +
    '<animate attributeName="d" ' +
    'values="M 12 8 C 14.21 8 16 9.79 16 12 C 16 14.21 14.21 16 12 16 C 9.79 16 8 14.21 8 12 C 8 9.79 9.79 8 12 8 Z;' +
    'M 12 12 C 14 8.5 19 8.5 19 12 C 19 15.5 14 15.5 12 12 C 10 8.5 5 8.5 5 12 C 5 15.5 10 15.5 12 12 Z;' +
    'M 12 16 C 14.21 16 16 14.21 16 12 C 16 9.79 14.21 8 12 8 C 9.79 8 8 9.79 8 12 C 8 14.21 9.79 16 12 16 Z;' +
    'M 12 12 C 14 8.5 19 8.5 19 12 C 19 15.5 14 15.5 12 12 C 10 8.5 5 8.5 5 12 C 5 15.5 10 15.5 12 12 Z;' +
    'M 12 8 C 14.21 8 16 9.79 16 12 C 16 14.21 14.21 16 12 16 C 9.79 16 8 14.21 8 12 C 8 9.79 9.79 8 12 8 Z" ' +
    'dur="6s" repeatCount="indefinite" calcMode="spline" ' +
    'keySplines="0.4 0 0.2 1; 0.4 0 0.2 1; 0.4 0 0.2 1; 0.4 0 0.2 1"/>' +
    '</path></svg>';

  const CHEVRON_SVG =
    '<svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round">' +
    '<polyline points="9 18 15 12 9 6"/></svg>';

  const SEARCH_ICON =
    '<svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">' +
    '<circle cx="11" cy="11" r="8"/><line x1="21" y1="21" x2="16.65" y2="16.65"/></svg>';

  // Match web-related tools by name substring (after lowering + stripping separators)

  function extractToolUrl(update) {
    let input = update.rawInput;
    if (!input) return '';
    if (typeof input === 'string') {
      try { input = JSON.parse(input); }
      catch { return input.startsWith('http') ? input : ''; }
    }
    if (typeof input !== 'object' || input === null) return '';
    return input.url || input.query || input.uri || '';
  }

  const GLOBE_ICON =
    '<svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">' +
    '<circle cx="12" cy="12" r="10"/><line x1="2" y1="12" x2="22" y2="12"/>' +
    '<path d="M12 2a15.3 15.3 0 0 1 4 10 15.3 15.3 0 0 1-4 10 15.3 15.3 0 0 1-4-10 15.3 15.3 0 0 1 4-10z"/></svg>';

  function extractDomain(url) {
    try { return new URL(url).hostname.replace(/^www\./, ''); }
    catch { return url; }
  }

  const TOOL_ICON =
    '<svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">' +
    '<path d="M14.7 6.3a1 1 0 0 0 0 1.4l1.6 1.6a1 1 0 0 0 1.4 0l3.77-3.77a6 6 0 0 1-7.94 7.94l-6.91 6.91a2.12 2.12 0 0 1-3-3l6.91-6.91a6 6 0 0 1 7.94-7.94l-3.76 3.76z"/></svg>';

  /** Build grouped timeline DOM nodes from thinking chunks. */
  function buildGroupedTimeline(chunks, showFinished) {
    const fragment = document.createDocumentFragment();

    // Process chunks in order, grouping consecutive same-kind web tools
    let i = 0;
    while (i < chunks.length) {
      const chunk = chunks[i];

      // Thought step — collapsible when text exceeds 120 chars
      if (chunk.kind === 'thought') {
        if (!chunk.text) { i++; continue; }
        const step = document.createElement('div');
        step.className = 'acp-tl-step acp-tl-step--thought';
        const dot = document.createElement('div');
        dot.className = 'acp-tl-dot';
        const content = document.createElement('div');
        content.className = 'acp-tl-content';
        if (chunk.text.length > 120) {
          const details = document.createElement('details');
          details.className = 'acp-tl-thought-details';
          const summary = document.createElement('summary');
          summary.className = 'acp-tl-thought-text';
          summary.textContent = excerpt(chunk.text, 120);
          details.appendChild(summary);
          const full = document.createElement('div');
          full.className = 'acp-tl-thought-full';
          full.textContent = chunk.text;
          details.appendChild(full);
          content.appendChild(details);
        } else {
          const text = document.createElement('span');
          text.className = 'acp-tl-thought-text';
          text.textContent = chunk.text;
          content.appendChild(text);
        }
        step.append(dot, content);
        fragment.appendChild(step);
        i++; continue;
      }

      if (chunk.kind !== 'tool') { i++; continue; }

      // Group consecutive search chunks
      if (chunk.toolKind === 'search') {
        const searchPills: Array<{ text: string }> = [];
        while (i < chunks.length && chunks[i].kind === 'tool' && chunks[i].toolKind === 'search') {
          const c = chunks[i];
          const cleanText = (c.text || '').replace(/^"|"$/g, '').trim();
          const pillText = c.url || cleanText;
          if (pillText && pillText !== 'undefined') searchPills.push({ text: pillText });
          i++;
        }
        if (searchPills.length > 0) {
          const step = document.createElement('div');
          step.className = 'acp-tl-step acp-tl-step--tool';
          const dot = document.createElement('div');
          dot.className = 'acp-tl-dot';
          const content = document.createElement('div');
          content.className = 'acp-tl-content';
          const label = document.createElement('span');
          label.className = 'acp-tl-group-label';
          label.textContent = 'Searching';
          content.appendChild(label);
          const pillRow = document.createElement('div');
          pillRow.className = 'acp-tl-pill-row';
          for (const item of searchPills) {
            const pill = document.createElement('span');
            pill.className = 'acp-tl-search-pill';
            pill.innerHTML = SEARCH_ICON;
            const pillText = document.createElement('span');
            pillText.textContent = item.text;
            pill.appendChild(pillText);
            pillRow.appendChild(pill);
          }
          content.appendChild(pillRow);
          step.append(dot, content);
          fragment.appendChild(step);
        }
        continue;
      }

      // Group consecutive fetch chunks
      if (chunk.toolKind === 'fetch') {
        const fetchSources: Array<{ url: string; domain: string }> = [];
        while (i < chunks.length && chunks[i].kind === 'tool' && chunks[i].toolKind === 'fetch') {
          const c = chunks[i];
          if (c.url) {
            fetchSources.push({ url: c.url, domain: extractDomain(c.url) });
          }
          i++;
        }
        if (fetchSources.length > 0) {
          const step = document.createElement('div');
          step.className = 'acp-tl-step acp-tl-step--sources';
          const dot = document.createElement('div');
          dot.className = 'acp-tl-dot';
          const content = document.createElement('div');
          content.className = 'acp-tl-content';
          const label = document.createElement('span');
          label.className = 'acp-tl-group-label';
          label.textContent = 'Reviewing sources';
          content.appendChild(label);
          const sourceList = document.createElement('div');
          sourceList.className = 'acp-tl-source-list';
          for (const src of fetchSources) {
            const row = document.createElement('div');
            row.className = 'acp-tl-source-row';
            const favicon = document.createElement('img');
            favicon.className = 'acp-tl-source-favicon';
            favicon.src = `https://www.google.com/s2/favicons?domain=${src.domain}&sz=32`;
            favicon.alt = '';
            favicon.width = 16;
            favicon.height = 16;
            const link = document.createElement('a');
            link.className = 'acp-tl-source-link';
            link.href = src.url;
            link.target = '_blank';
            link.rel = 'noopener noreferrer';
            link.textContent = src.url;
            const domainEl = document.createElement('span');
            domainEl.className = 'acp-tl-source-domain';
            domainEl.textContent = src.domain;
            row.append(favicon, link, domainEl);
            sourceList.appendChild(row);
          }
          content.appendChild(sourceList);
          step.append(dot, content);
          fragment.appendChild(step);
        }
        continue;
      }

      // Generic tool — name, status, collapsible details
      const statusClass = chunk.status === 'completed' ? 'acp-tl-step--completed'
        : chunk.status === 'failed' ? 'acp-tl-step--failed'
        : 'acp-tl-step--pending';
      const step = document.createElement('div');
      step.className = `acp-tl-step acp-tl-step--generic ${statusClass}`;
      const dot = document.createElement('div');
      dot.className = 'acp-tl-dot';
      const content = document.createElement('div');
      content.className = 'acp-tl-content';

      const labelRow = document.createElement('div');
      labelRow.className = 'acp-tl-tool-label-row';
      const icon = document.createElement('span');
      icon.className = 'acp-tl-tool-icon';
      icon.innerHTML = TOOL_ICON;
      const label = document.createElement('span');
      label.className = 'acp-tl-group-label';
      label.textContent = chunk.toolName || 'Tool call';
      labelRow.append(icon, label);

      if (chunk.status && chunk.status !== 'pending') {
        const badge = document.createElement('span');
        badge.className = 'acp-tl-status';
        badge.textContent = chunk.status.replace(/_/g, ' ');
        labelRow.appendChild(badge);
      }
      content.appendChild(labelRow);

      // Collapsible input/result
      if (chunk.input || chunk.result) {
        const details = document.createElement('details');
        details.className = 'acp-tl-tool-details';
        const summary = document.createElement('summary');
        summary.textContent = 'Details';
        details.appendChild(summary);
        if (chunk.input) {
          const inputLabel = document.createElement('div');
          inputLabel.className = 'acp-tl-detail-label';
          inputLabel.textContent = 'Input';
          const inputPre = document.createElement('pre');
          inputPre.textContent = chunk.input;
          details.append(inputLabel, inputPre);
        }
        if (chunk.result) {
          const resultLabel = document.createElement('div');
          resultLabel.className = 'acp-tl-detail-label';
          resultLabel.textContent = 'Result';
          const resultPre = document.createElement('pre');
          resultPre.textContent = chunk.result;
          details.append(resultLabel, resultPre);
        }
        content.appendChild(details);
      }

      step.append(dot, content);
      fragment.appendChild(step);
      i++;
    }

    // "Finished" step
    if (showFinished) {
      const step = document.createElement('div');
      step.className = 'acp-tl-step acp-tl-step--finished';
      const dot = document.createElement('div');
      dot.className = 'acp-tl-dot';
      const content = document.createElement('div');
      content.className = 'acp-tl-content';
      const text = document.createElement('span');
      text.className = 'acp-tl-text acp-tl-text--finished';
      text.textContent = 'Finished';
      content.appendChild(text);
      step.append(dot, content);
      fragment.appendChild(step);
    }

    return fragment;
  }

  function renderThinkingBlock(transcript) {
    const chunks = transcript.thinkingChunks;
    const isActive = transcript.thinkingActive;
    const hasToolOrThought = chunks.length > 0;
    if (!hasToolOrThought) {
      if (_thinkingEl) {
        _thinkingEl = null;
        _thinkingRenderedCount = 0;
        _thinkingPrevToolCount = 0;
        _thinkingPrevThoughtCount = 0;
        if (_thinkingWordInterval) {
          clearInterval(_thinkingWordInterval);
          _thinkingWordInterval = null;
        }
      }
      return null;
    }

    // Create element if it doesn't exist
    if (!_thinkingEl) {
      _thinkingRenderedCount = 0;
      _thinkingWordIndex = 0;
      const container = document.createElement('div');
      container.className = 'acp-thinking acp-thinking--expanded'; // auto-open

      const header = document.createElement('button');
      header.className = 'acp-thinking-header';
      header.type = 'button';
      header.setAttribute('aria-expanded', 'true');

      const icon = document.createElement('span');
      icon.className = 'acp-thinking-icon-wrap';
      icon.innerHTML = MORPH_SVG;

      // Word label: inline-grid with invisible sizer + animated visible span
      const labelWrap = document.createElement('span');
      labelWrap.className = 'acp-thinking-label-wrap';

      const sizer = document.createElement('span');
      sizer.className = 'acp-thinking-label-sizer shimmer-text';
      sizer.setAttribute('aria-hidden', 'true');
      sizer.textContent = THINKING_WORDS.reduce((a, b) => a.length >= b.length ? a : b);

      const label = document.createElement('span');
      label.className = 'acp-thinking-label shimmer-text';
      label.textContent = 'Thinking';

      labelWrap.append(sizer, label);

      const chevron = document.createElement('span');
      chevron.className = 'acp-thinking-chevron';
      chevron.innerHTML = CHEVRON_SVG;

      header.append(icon, labelWrap, chevron);

      const body = document.createElement('div');
      body.className = 'acp-thinking-body';

      const timeline = document.createElement('div');
      timeline.className = 'acp-thinking-timeline';
      body.appendChild(timeline);

      container.append(header, body);

      // Toggle expand/collapse
      header.addEventListener('click', function () {
        const expanded = container.classList.toggle('acp-thinking--expanded');
        header.setAttribute('aria-expanded', String(expanded));
      });

      // Rotating words
      if (_thinkingWordInterval) clearInterval(_thinkingWordInterval);
      _thinkingWordInterval = setInterval(() => {
        if (!_thinkingEl) return;
        const el = _thinkingEl.querySelector('.acp-thinking-label') as HTMLElement | null;
        if (!el || _thinkingEl.classList.contains('acp-thinking--done')) return;
        _thinkingWordIndex = (_thinkingWordIndex + 1) % THINKING_WORDS.length;
        const nextWord = THINKING_WORDS[_thinkingWordIndex];
        el.classList.add('acp-thinking-label--exit');
        setTimeout(() => {
          el.classList.remove('acp-thinking-label--exit');
          el.textContent = nextWord;
          el.classList.add('acp-thinking-label--enter');
          setTimeout(() => { el.classList.remove('acp-thinking-label--enter'); }, 240);
        }, 160);
      }, 2500);

      _thinkingEl = container;
    }

    // Update label for done state
    const labelEl = _thinkingEl.querySelector('.acp-thinking-label') as HTMLElement | null;
    const sizerEl = _thinkingEl.querySelector('.acp-thinking-label-sizer') as HTMLElement | null;
    if (isActive) {
      _thinkingEl.classList.remove('acp-thinking--done');
    } else {
      const seconds = transcript.thinkingElapsedSeconds || Math.round((Date.now() - (transcript.thinkingStartTime || Date.now())) / 1000);
      const doneText = `Thought for ${seconds} second${seconds !== 1 ? 's' : ''}`;
      if (labelEl) {
        labelEl.textContent = doneText;
        labelEl.classList.remove('shimmer-text', 'acp-thinking-label--enter', 'acp-thinking-label--exit');
      }
      if (sizerEl) sizerEl.textContent = doneText;
      _thinkingEl.classList.add('acp-thinking--done');
      // Auto-close when done
      _thinkingEl.classList.remove('acp-thinking--expanded');
      _thinkingEl.querySelector('.acp-thinking-header')?.setAttribute('aria-expanded', 'false');
      if (_thinkingWordInterval) {
        clearInterval(_thinkingWordInterval);
        _thinkingWordInterval = null;
      }
    }

    // Rebuild grouped timeline
    const timeline = _thinkingEl.querySelector('.acp-thinking-timeline');
    if (timeline) {
      timeline.innerHTML = '';
      timeline.appendChild(buildGroupedTimeline(chunks, !isActive && chunks.length > 0));
    }

    return _thinkingEl;
  }

  function clearThinkingBlock() {
    _thinkingEl = null;
    _thinkingRenderedCount = 0;
    _thinkingPrevToolCount = 0;
    _thinkingPrevThoughtCount = 0;
    if (_thinkingWordInterval) {
      clearInterval(_thinkingWordInterval);
      _thinkingWordInterval = null;
    }
  }

  function getPreviewText(transcript) {
    for (let index = transcript.messages.length - 1; index >= 0; index -= 1) {
      const message = transcript.messages[index];
      if (!message) continue;
      if (message.type === 'assistant' || message.type === 'user') {
        const textBlock = message.blocks.find((block) => block.type === 'text' && block.text);
        if (textBlock) {
          const htmlError = detectHtmlErrorDocument(textBlock.text);
          if (!htmlError) {
            return excerpt(textBlock.text);
          }
        }
      }
      if (message.type === 'tool') {
        const resultBlock = message.blocks.find((block) => block.type === 'details' && block.title === 'Result' && block.text);
        const htmlError = detectHtmlErrorDocument(resultBlock?.text);
        if (htmlError) {
          return excerpt(htmlError.text);
        }
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
    const type = update?.sessionUpdate || '';
    return ![
      '',
      'available_commands_update',
      'current_mode_update',
      'usage_update',
      'session_info_update',
    ].includes(type);
  }

  global.SpritzACPRender = {
    appendStreamingChunk,
    buildCommandItems,
    clearThinkingBlock,
    createTranscript,
    detectHtmlErrorDocument,
    applySessionUpdate,
    finalizeHistoricalThinking,
    finalizeStreaming,
    getPreviewText,
    hydrateTranscript,
    isTranscriptBearingUpdate,
    renderMessage,
    renderThinkingBlock,
    serializeTranscript,
    transcriptContainsHtmlError,
  };
})(window);
