(function (global) {
  const { createACPClient, extractACPText } = global.SpritzACPClient;

  function chatPagePath(name = '', conversationId = '') {
    if (!name) return '#chat';
    if (!conversationId) return `#chat/${encodeURIComponent(name)}`;
    return `#chat/${encodeURIComponent(name)}/${encodeURIComponent(conversationId)}`;
  }

  function chatRouteState(hash) {
    const value = hash || '';
    const prefix = '#chat/';
    if (value === '#chat') {
      return { spritzName: '', conversationId: '' };
    }
    if (!value.startsWith(prefix)) {
      return { spritzName: '', conversationId: '' };
    }
    const remainder = value.slice(prefix.length).split('/');
    return {
      spritzName: decodeURIComponent(remainder[0] || ''),
      conversationId: decodeURIComponent(remainder[1] || ''),
    };
  }

  function chatNameFromHash(hash) {
    return chatRouteState(hash).spritzName;
  }

  function conversationIdFromHash(hash) {
    return chatRouteState(hash).conversationId;
  }

  function acpWsUrl(name, deps) {
    const base = deps.apiBaseUrl || '';
    const resolved = base.startsWith('http') ? base : `${window.location.origin}${base}`;
    const wsBase = resolved.replace(/^http/, 'ws');
    const token = deps.getAuthToken();
    const query = token ? `?${encodeURIComponent(deps.authBearerTokenParam)}=${encodeURIComponent(token)}` : '';
    return `${wsBase}/acp/connect/${encodeURIComponent(name)}${query}`;
  }

  async function fetchACPAgentsData(deps) {
    const data = await deps.request('/acp/agents');
    return data.items || [];
  }

  async function listACPConversationsData(deps, spritzName) {
    const query = new URLSearchParams();
    query.set('spritz', spritzName);
    const data = await deps.request(`/acp/conversations?${query.toString()}`);
    return data.items || [];
  }

  async function createACPConversationData(deps, spritzName) {
    return deps.request('/acp/conversations', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ spritzName }),
    });
  }

  async function patchACPConversationData(deps, id, payload) {
    return deps.request(`/acp/conversations/${encodeURIComponent(id)}`, {
      method: 'PATCH',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(payload),
    });
  }

  function defaultACPThreadTitle(agent, conversation) {
    return (
      conversation?.spec?.title ||
      agent?.spritz?.status?.acp?.agentInfo?.title ||
      agent?.spritz?.status?.acp?.agentInfo?.name ||
      agent?.spritz?.metadata?.name ||
      'Agent'
    );
  }

  function buildACPThreadMeta(agent, conversation) {
    const version = agent?.spritz?.status?.acp?.agentInfo?.version;
    const sessionId = conversation?.spec?.sessionId;
    const parts = [
      agent?.spritz?.metadata?.name || '',
      version ? `v${version}` : '',
      sessionId ? `session ${sessionId}` : 'new conversation',
    ].filter(Boolean);
    return parts.join(' · ');
  }

  function createACPPageState(name, conversationId, deps) {
    return {
      deps,
      selectedName: name || '',
      selectedConversationId: conversationId || '',
      agents: [],
      selectedAgent: null,
      conversations: [],
      selectedConversation: null,
      messages: [],
      permissionQueue: [],
      toolCallIndex: new Map(),
      promptInFlight: false,
      client: null,
      reconnectTimer: null,
      destroyed: false,
    };
  }

  function renderACPAgentList(page) {
    if (!page.agentListEl) return;
    page.agentListEl.innerHTML = '';
    if (!page.agents.length) {
      const empty = document.createElement('p');
      empty.className = 'acp-empty';
      empty.textContent = 'No ACP-ready spritzes yet.';
      page.agentListEl.appendChild(empty);
      return;
    }
    page.agents.forEach((agent) => {
      const name = agent?.spritz?.metadata?.name;
      const button = document.createElement('button');
      button.type = 'button';
      button.className = 'acp-agent-item';
      button.dataset.active = String(name === page.selectedName);
      button.onclick = () => {
        if (!name) return;
        window.location.assign(chatPagePath(name));
      };
      const title = document.createElement('strong');
      title.textContent =
        agent?.spritz?.status?.acp?.agentInfo?.title ||
        agent?.spritz?.status?.acp?.agentInfo?.name ||
        name ||
        'Agent';
      const meta = document.createElement('small');
      meta.textContent = name || '';
      button.append(title, meta);
      page.agentListEl.appendChild(button);
    });
  }

  function renderACPConversationList(page) {
    if (!page.conversationListEl) return;
    page.conversationListEl.innerHTML = '';
    if (!page.selectedAgent) {
      const empty = document.createElement('p');
      empty.className = 'acp-empty';
      empty.textContent = 'Choose an ACP-ready spritz first.';
      page.conversationListEl.appendChild(empty);
      page.newConversationBtn.disabled = true;
      return;
    }
    page.newConversationBtn.disabled = false;
    if (!page.conversations.length) {
      const empty = document.createElement('p');
      empty.className = 'acp-empty';
      empty.textContent = 'No conversations yet. Create one to start chatting.';
      page.conversationListEl.appendChild(empty);
      return;
    }
    page.conversations.forEach((conversation) => {
      const button = document.createElement('button');
      button.type = 'button';
      button.className = 'acp-conversation-item';
      button.dataset.active = String(conversation.metadata?.name === page.selectedConversationId);
      button.onclick = () => {
        window.location.assign(chatPagePath(page.selectedName, conversation.metadata?.name || ''));
      };
      const title = document.createElement('strong');
      title.textContent = conversation.spec?.title || 'New conversation';
      const meta = document.createElement('small');
      meta.textContent = buildACPThreadMeta(page.selectedAgent, conversation);
      button.append(title, meta);
      page.conversationListEl.appendChild(button);
    });
  }

  function renderACPMessageElement(message) {
    const item = document.createElement('div');
    item.className = 'acp-message';
    item.dataset.kind = message.kind;

    if (message.kind === 'tool' || message.kind === 'plan' || message.kind === 'system') {
      const header = document.createElement('div');
      header.className = 'acp-message-header';
      const label = document.createElement('strong');
      label.textContent = message.label || 'Update';
      const meta = document.createElement('span');
      meta.textContent = message.meta || '';
      header.append(label, meta);
      item.appendChild(header);
    }

    if (message.kind === 'plan' && Array.isArray(message.entries)) {
      const list = document.createElement('ol');
      list.className = 'acp-message-plan';
      message.entries.forEach((entry) => {
        const li = document.createElement('li');
        const parts = [entry.content || '', entry.status || '', entry.priority || ''].filter(Boolean);
        li.textContent = parts.join(' · ');
        list.appendChild(li);
      });
      item.appendChild(list);
      return item;
    }

    const text = document.createElement('div');
    text.textContent = message.text || '';
    item.appendChild(text);

    if (message.kind === 'tool' && message.extra) {
      const extra = document.createElement('div');
      extra.className = 'acp-tool-content';
      extra.textContent = message.extra;
      item.appendChild(extra);
    }

    return item;
  }

  function renderACPPermissionPrompt(page) {
    if (!page.permissionEl || !page.permissionTextEl || !page.permissionOptionsEl) return;
    const current = page.permissionQueue[0];
    if (!current) {
      page.permissionEl.hidden = true;
      page.permissionTextEl.textContent = '';
      page.permissionOptionsEl.innerHTML = '';
      return;
    }

    page.permissionEl.hidden = false;
    const toolTitle = current.params?.toolCall?.title || current.params?.toolCall?.toolCallId || 'Tool call';
    page.permissionTextEl.textContent = `${toolTitle} is requesting permission.`;
    page.permissionOptionsEl.innerHTML = '';

    (current.params?.options || []).forEach((option) => {
      const button = document.createElement('button');
      button.type = 'button';
      button.textContent = option.name || option.optionId || 'Select';
      button.onclick = async () => {
        try {
          await current.respond({
            outcome: {
              outcome: 'selected',
              optionId: option.optionId,
            },
          });
        } catch (err) {
          page.deps.showNotice(err.message || 'Failed to send permission response.');
        } finally {
          page.permissionQueue.shift();
          renderACPPermissionPrompt(page);
        }
      };
      page.permissionOptionsEl.appendChild(button);
    });
  }

  function renderACPThread(page) {
    if (!page.threadTitleEl || !page.threadMetaEl || !page.threadBodyEl) return;
    const openUrl = page.deps.buildOpenUrl(page.selectedAgent?.spritz?.status?.url, page.selectedAgent?.spritz);
    page.openBtn.disabled = !openUrl;
    page.threadTitleEl.textContent = defaultACPThreadTitle(page.selectedAgent, page.selectedConversation);
    page.threadMetaEl.textContent = buildACPThreadMeta(page.selectedAgent, page.selectedConversation);
    page.threadBodyEl.innerHTML = '';

    if (!page.selectedAgent) {
      const empty = document.createElement('div');
      empty.className = 'acp-empty';
      empty.textContent = 'Choose an ACP-ready spritz to start chatting.';
      page.threadBodyEl.appendChild(empty);
      renderACPPermissionPrompt(page);
      return;
    }

    if (!page.selectedConversation) {
      const empty = document.createElement('div');
      empty.className = 'acp-empty';
      empty.textContent = 'Choose or create a conversation to start chatting.';
      page.threadBodyEl.appendChild(empty);
      renderACPPermissionPrompt(page);
      return;
    }

    if (!page.messages.length) {
      const empty = document.createElement('div');
      empty.className = 'acp-empty';
      empty.textContent = 'Conversation is ready. Send a prompt to start.';
      page.threadBodyEl.appendChild(empty);
    } else {
      page.messages.forEach((message) => {
        page.threadBodyEl.appendChild(renderACPMessageElement(message));
      });
    }
    page.threadBodyEl.scrollTop = page.threadBodyEl.scrollHeight;
    renderACPPermissionPrompt(page);
  }

  function setACPStatus(page, text) {
    if (!page.statusEl) return;
    page.statusEl.textContent = text || '';
  }

  function syncACPComposer(page) {
    const disabled = !page.client || !page.client.isReady() || !page.selectedConversation;
    if (page.composerEl) page.composerEl.disabled = disabled || page.promptInFlight;
    if (page.sendBtn) page.sendBtn.disabled = disabled || page.promptInFlight;
    if (page.cancelBtn) page.cancelBtn.disabled = !page.promptInFlight;
  }

  function pushACPMessage(page, message) {
    page.messages.push({
      id: message.id || `${message.kind}-${Date.now()}-${Math.random().toString(36).slice(2, 7)}`,
      kind: message.kind,
      label: message.label || '',
      meta: message.meta || '',
      text: message.text || '',
      extra: message.extra || '',
      entries: message.entries || null,
      toolCallId: message.toolCallId || '',
      streaming: Boolean(message.streaming),
    });
    renderACPThread(page);
  }

  function appendACPChunk(page, kind, text) {
    const chunk = String(text || '');
    if (!chunk) return;
    const last = page.messages[page.messages.length - 1];
    if (last && last.kind === kind && last.streaming) {
      last.text += chunk;
    } else {
      pushACPMessage(page, { kind, text: chunk, streaming: true });
    }
    renderACPThread(page);
  }

  function finalizeACPStreams(page) {
    page.messages.forEach((message) => {
      if (message.kind === 'assistant' || message.kind === 'user') {
        message.streaming = false;
      }
    });
  }

  function upsertACPToolCall(page, update) {
    const toolCallId = update.toolCallId || `tool-${Date.now()}`;
    const text = extractACPText(update.content);
    const existingIndex = page.toolCallIndex.get(toolCallId);
    const label = update.title || 'Tool call';
    const meta = update.status || update.kind || '';
    if (existingIndex !== undefined) {
      const existing = page.messages[existingIndex];
      existing.label = label;
      existing.meta = meta;
      if (text) existing.extra = text;
    } else {
      page.toolCallIndex.set(toolCallId, page.messages.length);
      pushACPMessage(page, {
        kind: 'tool',
        label,
        meta,
        extra: text,
        toolCallId,
      });
    }
    renderACPThread(page);
  }

  async function patchSelectedConversation(page, payload) {
    if (!page.selectedConversation?.metadata?.name) return;
    const updated = await patchACPConversationData(page.deps, page.selectedConversation.metadata.name, payload);
    page.selectedConversation = updated;
    page.selectedConversationId = updated.metadata?.name || page.selectedConversationId;
    page.conversations = page.conversations.map((item) =>
      item.metadata?.name === updated.metadata?.name ? updated : item,
    );
    renderACPConversationList(page);
    renderACPThread(page);
  }

  function applyACPUpdate(page, update) {
    const kind = update?.sessionUpdate || 'unknown';
    if (kind === 'user_message_chunk') {
      appendACPChunk(page, 'user', extractACPText(update.content));
      return;
    }
    if (kind === 'agent_message_chunk') {
      appendACPChunk(page, 'assistant', extractACPText(update.content));
      return;
    }
    if (kind === 'tool_call' || kind === 'tool_call_update') {
      upsertACPToolCall(page, update);
      return;
    }
    if (kind === 'plan') {
      pushACPMessage(page, {
        kind: 'plan',
        label: 'Plan',
        entries: Array.isArray(update.entries) ? update.entries : [],
      });
      return;
    }
    if (kind === 'session_info_update') {
      const title = update?.title || update?.sessionInfo?.title;
      if (title) {
        patchSelectedConversation(page, { title }).catch(() => {});
      }
      return;
    }
    pushACPMessage(page, {
      kind: 'system',
      label: kind,
      text: JSON.stringify(update, null, 2),
    });
  }

  function clearConversationRuntime(page) {
    page.messages = [];
    page.permissionQueue = [];
    page.toolCallIndex = new Map();
    page.promptInFlight = false;
    if (page.client) {
      page.client.dispose();
      page.client = null;
    }
    if (page.reconnectTimer) {
      clearTimeout(page.reconnectTimer);
      page.reconnectTimer = null;
    }
    syncACPComposer(page);
  }

  function scheduleReconnect(page) {
    if (page.destroyed || !page.selectedConversation || !page.selectedName) return;
    if (page.reconnectTimer) {
      clearTimeout(page.reconnectTimer);
    }
    page.reconnectTimer = setTimeout(() => {
      connectSelectedConversation(page).catch((err) => {
        setACPStatus(page, err.message || 'Disconnected');
      });
    }, 2000);
    setACPStatus(page, 'Disconnected. Reconnecting…');
  }

  async function connectSelectedConversation(page) {
    clearConversationRuntime(page);
    if (!page.selectedAgent || !page.selectedConversation) {
      renderACPThread(page);
      return;
    }
    renderACPThread(page);
    page.client = createACPClient({
      wsUrl: acpWsUrl(page.selectedName, page.deps),
      conversation: page.selectedConversation,
      onStatus(text) {
        setACPStatus(page, text);
      },
      onReadyChange() {
        syncACPComposer(page);
      },
      onAgentInfo(agentInfo) {
        if (!page.selectedAgent) return;
        page.selectedAgent = {
          ...page.selectedAgent,
          spritz: {
            ...page.selectedAgent.spritz,
            status: {
              ...(page.selectedAgent.spritz.status || {}),
              acp: {
                ...(page.selectedAgent.spritz.status?.acp || {}),
                agentInfo: agentInfo || page.selectedAgent.spritz.status?.acp?.agentInfo,
              },
            },
          },
        };
        renderACPAgentList(page);
        renderACPThread(page);
      },
      onUpdate(update) {
        applyACPUpdate(page, update);
      },
      onPermissionRequest(entry) {
        page.permissionQueue.push(entry);
        renderACPPermissionPrompt(page);
      },
      async onSessionId(sessionId) {
        await patchSelectedConversation(page, { sessionId });
      },
      onPromptStateChange(value) {
        page.promptInFlight = value;
        if (!value) {
          finalizeACPStreams(page);
        }
        syncACPComposer(page);
      },
      onClose() {
        scheduleReconnect(page);
      },
      onProtocolError(err) {
        pushACPMessage(page, { kind: 'system', label: 'protocol', text: err.message || 'Invalid ACP message.' });
      },
    });
    syncACPComposer(page);
    await page.client.start();
  }

  async function selectConversation(page, conversationId) {
    page.selectedConversationId = conversationId || '';
    page.selectedConversation = page.conversations.find((item) => item.metadata?.name === conversationId) || null;
    clearConversationRuntime(page);
    renderACPConversationList(page);
    renderACPThread(page);
    if (page.selectedConversation) {
      await connectSelectedConversation(page);
    } else {
      setACPStatus(page, 'Choose or create a conversation.');
    }
  }

  async function refreshConversations(page) {
    if (!page.selectedName) {
      page.conversations = [];
      page.selectedConversation = null;
      page.selectedConversationId = '';
      renderACPConversationList(page);
      renderACPThread(page);
      return;
    }
    page.conversations = await listACPConversationsData(page.deps, page.selectedName);
    const routeConversationId = conversationIdFromHash(window.location.hash || '');
    const resolvedConversationId =
      page.conversations.some((item) => item.metadata?.name === routeConversationId)
        ? routeConversationId
        : page.conversations[0]?.metadata?.name || '';
    renderACPConversationList(page);
    if (routeConversationId && resolvedConversationId !== routeConversationId) {
      window.location.replace(chatPagePath(page.selectedName, resolvedConversationId));
      return;
    }
    await selectConversation(page, resolvedConversationId);
  }

  async function loadACPPage(page) {
    try {
      setACPStatus(page, 'Loading agents…');
      page.agents = await fetchACPAgentsData(page.deps);
      renderACPAgentList(page);
      if (!page.agents.length) {
        page.selectedAgent = null;
        page.conversations = [];
        page.selectedConversation = null;
        page.selectedConversationId = '';
        clearConversationRuntime(page);
        renderACPConversationList(page);
        renderACPThread(page);
        setACPStatus(page, 'No ACP-ready spritzes.');
        return;
      }

      const resolvedName = page.agents.some((item) => item?.spritz?.metadata?.name === page.selectedName)
        ? page.selectedName
        : page.agents[0]?.spritz?.metadata?.name;
      if (!page.selectedName || resolvedName !== page.selectedName) {
        window.location.replace(chatPagePath(resolvedName));
        return;
      }

      page.selectedName = resolvedName;
      page.selectedAgent = page.agents.find((item) => item?.spritz?.metadata?.name === resolvedName) || null;
      renderACPConversationList(page);
      renderACPThread(page);
      await refreshConversations(page);
      if (!page.selectedConversation) {
        setACPStatus(page, 'Choose or create a conversation.');
      }
    } catch (err) {
      setACPStatus(page, err.message || 'Failed to load ACP page.');
      page.deps.showNotice(err.message || 'Failed to load ACP page.');
    }
  }

  function renderACPPage(name, conversationId, deps) {
    deps.cleanupTerminal();
    if (deps.activePage) {
      deps.activePage.destroy();
    }
    if (deps.createSection) deps.createSection.hidden = true;
    if (deps.listSection) deps.listSection.hidden = true;
    deps.setHeaderCopy('Spritz · Agent chat', 'ACP-ready workspaces via the Spritz gateway.');

    const page = createACPPageState(name, conversationId, deps);

    const card = document.createElement('section');
    card.className = 'card acp-card';

    const agentSidebar = document.createElement('aside');
    agentSidebar.className = 'acp-sidebar';
    const agentHeader = document.createElement('div');
    agentHeader.className = 'acp-sidebar-header';
    const agentTitle = document.createElement('h2');
    agentTitle.textContent = 'Agents';
    const agentMeta = document.createElement('p');
    agentMeta.textContent = 'ACP-ready spritzes appear here automatically.';
    const agentActions = document.createElement('div');
    agentActions.className = 'acp-sidebar-actions';
    const backLink = document.createElement('a');
    backLink.href = '/';
    backLink.className = 'header-link';
    backLink.textContent = 'Back';
    const refreshButton = document.createElement('button');
    refreshButton.type = 'button';
    refreshButton.textContent = 'Refresh';
    agentActions.append(backLink, refreshButton);
    agentHeader.append(agentTitle, agentMeta, agentActions);
    const agentList = document.createElement('div');
    agentList.className = 'acp-agent-list';
    agentSidebar.append(agentHeader, agentList);

    const conversationSidebar = document.createElement('aside');
    conversationSidebar.className = 'acp-conversation-sidebar';
    const conversationHeader = document.createElement('div');
    conversationHeader.className = 'acp-sidebar-header';
    const conversationTitle = document.createElement('h2');
    conversationTitle.textContent = 'Conversations';
    const conversationMeta = document.createElement('p');
    conversationMeta.textContent = 'Each thread keeps its own ACP session.';
    const conversationActions = document.createElement('div');
    conversationActions.className = 'acp-sidebar-actions';
    const newConversationButton = document.createElement('button');
    newConversationButton.type = 'button';
    newConversationButton.textContent = 'New conversation';
    conversationActions.append(newConversationButton);
    conversationHeader.append(conversationTitle, conversationMeta, conversationActions);
    const conversationList = document.createElement('div');
    conversationList.className = 'acp-conversation-list';
    conversationSidebar.append(conversationHeader, conversationList);

    const thread = document.createElement('div');
    thread.className = 'acp-thread';
    const threadHeader = document.createElement('div');
    threadHeader.className = 'acp-thread-header';
    const threadHeaderCopy = document.createElement('div');
    const threadTitle = document.createElement('h2');
    threadTitle.textContent = 'Agent';
    const threadMeta = document.createElement('p');
    threadMeta.textContent = '';
    threadHeaderCopy.append(threadTitle, threadMeta);
    const threadActions = document.createElement('div');
    threadActions.className = 'acp-thread-actions';
    const openButton = document.createElement('button');
    openButton.type = 'button';
    openButton.textContent = 'Open workspace';
    threadActions.append(openButton);
    threadHeader.append(threadHeaderCopy, threadActions);

    const threadBody = document.createElement('div');
    threadBody.className = 'acp-thread-body';
    const footer = document.createElement('div');
    footer.className = 'acp-thread-footer';
    const permissionBox = document.createElement('div');
    permissionBox.className = 'acp-permission';
    permissionBox.hidden = true;
    const permissionText = document.createElement('div');
    const permissionOptions = document.createElement('div');
    permissionOptions.className = 'acp-permission-options';
    permissionBox.append(permissionText, permissionOptions);

    const statusRow = document.createElement('div');
    statusRow.className = 'acp-status-row';
    const statusEl = document.createElement('span');
    statusEl.className = 'acp-status';
    const hint = document.createElement('span');
    hint.className = 'acp-hint';
    hint.textContent = 'Enter sends. Shift+Enter adds a new line.';
    statusRow.append(statusEl, hint);

    const composer = document.createElement('div');
    composer.className = 'acp-composer';
    const composerInput = document.createElement('textarea');
    composerInput.placeholder = 'Message the agent…';
    const composerActions = document.createElement('div');
    composerActions.className = 'acp-composer-actions';
    const sendButton = document.createElement('button');
    sendButton.type = 'button';
    sendButton.textContent = 'Send';
    const cancelButton = document.createElement('button');
    cancelButton.type = 'button';
    cancelButton.className = 'ghost';
    cancelButton.textContent = 'Cancel turn';
    composerActions.append(sendButton, cancelButton);
    composer.append(composerInput, composerActions);
    footer.append(permissionBox, statusRow, composer);

    thread.append(threadHeader, threadBody, footer);
    card.append(agentSidebar, conversationSidebar, thread);
    deps.shellEl.append(card);

    page.card = card;
    page.agentListEl = agentList;
    page.conversationListEl = conversationList;
    page.threadTitleEl = threadTitle;
    page.threadMetaEl = threadMeta;
    page.threadBodyEl = threadBody;
    page.permissionEl = permissionBox;
    page.permissionTextEl = permissionText;
    page.permissionOptionsEl = permissionOptions;
    page.statusEl = statusEl;
    page.composerEl = composerInput;
    page.sendBtn = sendButton;
    page.cancelBtn = cancelButton;
    page.refreshBtn = refreshButton;
    page.newConversationBtn = newConversationButton;
    page.openBtn = openButton;

    refreshButton.addEventListener('click', () => {
      loadACPPage(page);
    });

    newConversationButton.addEventListener('click', async () => {
      if (!page.selectedName) return;
      try {
        const conversation = await createACPConversationData(page.deps, page.selectedName);
        page.conversations = [conversation, ...page.conversations];
        renderACPConversationList(page);
        window.location.assign(chatPagePath(page.selectedName, conversation.metadata?.name || ''));
      } catch (err) {
        page.deps.showNotice(err.message || 'Failed to create conversation.');
      }
    });

    openButton.addEventListener('click', () => {
      const openUrl = page.deps.buildOpenUrl(page.selectedAgent?.spritz?.status?.url, page.selectedAgent?.spritz);
      if (openUrl) {
        window.open(openUrl, '_blank');
      }
    });

    sendButton.addEventListener('click', async () => {
      const text = composerInput.value.trim();
      if (!text || !page.client || !page.selectedConversation) return;
      composerInput.value = '';
      pushACPMessage(page, { kind: 'user', text });
      if (!page.selectedConversation.spec?.title || page.selectedConversation.spec.title === 'New conversation') {
        const nextTitle = text.slice(0, 80);
        patchSelectedConversation(page, { title: nextTitle }).catch(() => {});
      }
      setACPStatus(page, 'Waiting for agent…');
      try {
        const result = await page.client.sendPrompt(text);
        setACPStatus(page, result?.stopReason ? `Completed · ${result.stopReason}` : 'Completed');
      } catch (err) {
        page.deps.showNotice(err.message || 'Failed to send ACP prompt.');
      } finally {
        syncACPComposer(page);
        renderACPThread(page);
      }
    });

    cancelButton.addEventListener('click', () => {
      page.client?.cancelPrompt();
    });

    composerInput.addEventListener('keydown', (event) => {
      if (event.key === 'Enter' && !event.shiftKey) {
        event.preventDefault();
        sendButton.click();
      }
    });

    page.destroy = function destroy() {
      page.destroyed = true;
      clearConversationRuntime(page);
      if (page.card) {
        page.card.remove();
      }
      if (deps.createSection) deps.createSection.hidden = false;
      if (deps.listSection) deps.listSection.hidden = false;
    };

    syncACPComposer(page);
    renderACPAgentList(page);
    renderACPConversationList(page);
    renderACPThread(page);
    loadACPPage(page);
    return page;
  }

  global.SpritzACPPage = {
    chatPagePath,
    chatNameFromHash,
    conversationIdFromHash,
    renderACPPage,
  };
})(window);
