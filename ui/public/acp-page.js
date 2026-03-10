(function (global) {
  const { createACPClient } = global.SpritzACPClient;
  const ACPRender = global.SpritzACPRender;
  const ACP_TRANSCRIPT_CACHE_VERSION = 1;
  const ACP_TRANSCRIPT_CACHE_PREFIX = 'spritz:acp:transcript:';
  const ACP_TRANSCRIPT_CACHE_INDEX_KEY = 'spritz:acp:transcript:index';
  const ACP_TRANSCRIPT_CACHE_LIMIT = 25;

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

  function createACPPageState(name, conversationId, deps) {
    return {
      deps,
      selectedName: name || '',
      selectedConversationId: conversationId || '',
      agents: [],
      selectedAgent: null,
      conversations: [],
      selectedConversation: null,
      transcript: ACPRender.createTranscript(),
      permissionQueue: [],
      previewByConversationId: new Map(),
      promptInFlight: false,
      client: null,
      reconnectTimer: null,
      destroyed: false,
      bootstrapComplete: false,
      cacheHydratedTranscript: false,
      cacheReplacedByReplay: false,
    };
  }

  function isBenignACPError(err) {
    const code = String(err?.code || '');
    const message = String(err?.message || '');
    return (
      code === 'ACP_CLIENT_DISPOSED' ||
      code === 'ACP_CONNECTION_CLOSED' ||
      message === 'ACP client disposed.' ||
      message === 'ACP connection closed.'
    );
  }

  function clearACPNotice(page) {
    if (typeof page.deps.clearNotice === 'function') {
      page.deps.clearNotice();
      return;
    }
    if (typeof page.deps.showNotice === 'function') {
      page.deps.showNotice('');
    }
  }

  function showACPToast(page, message, kind = 'error') {
    if (!message) return;
    if (typeof page.deps.showToast === 'function') {
      page.deps.showToast(message, kind);
      return;
    }
    if (typeof page.deps.showNotice === 'function') {
      page.deps.showNotice(message, kind);
    }
  }

  function safeSessionStorage() {
    try {
      return window.sessionStorage || window.localStorage || null;
    } catch {
      return null;
    }
  }

  function readTranscriptCacheIndex(storage) {
    if (!storage) return [];
    try {
      const raw = storage.getItem(ACP_TRANSCRIPT_CACHE_INDEX_KEY);
      const parsed = raw ? JSON.parse(raw) : [];
      return Array.isArray(parsed) ? parsed.filter((item) => typeof item === 'string' && item) : [];
    } catch {
      return [];
    }
  }

  function writeTranscriptCacheIndex(storage, ids) {
    if (!storage) return;
    try {
      storage.setItem(ACP_TRANSCRIPT_CACHE_INDEX_KEY, JSON.stringify(ids));
    } catch {
      // ignore storage errors
    }
  }

  function conversationTranscriptCacheKey(conversationId) {
    return `${ACP_TRANSCRIPT_CACHE_PREFIX}${conversationId}`;
  }

  function readCachedConversationRecord(conversationId) {
    const normalizedId = String(conversationId || '').trim();
    if (!normalizedId) return null;
    const storage = safeSessionStorage();
    if (!storage) return null;
    try {
      const raw = storage.getItem(conversationTranscriptCacheKey(normalizedId));
      if (!raw) return null;
      const parsed = JSON.parse(raw);
      if (parsed?.version !== ACP_TRANSCRIPT_CACHE_VERSION || typeof parsed !== 'object') {
        return null;
      }
      return parsed;
    } catch {
      return null;
    }
  }

  function writeCachedConversationRecord(page) {
    const conversationId = page.selectedConversation?.metadata?.name || '';
    if (!conversationId) return;
    const storage = safeSessionStorage();
    if (!storage) return;

    const preview = ACPRender.getPreviewText(page.transcript);
    const payload = {
      version: ACP_TRANSCRIPT_CACHE_VERSION,
      conversationId,
      spritzName: page.selectedName || '',
      sessionId: page.selectedConversation?.spec?.sessionId || '',
      updatedAt: new Date().toISOString(),
      preview,
      transcript: ACPRender.serializeTranscript(page.transcript),
    };
    try {
      storage.setItem(conversationTranscriptCacheKey(conversationId), JSON.stringify(payload));
      const nextIndex = [conversationId, ...readTranscriptCacheIndex(storage).filter((item) => item !== conversationId)];
      const prunedIndex = nextIndex.slice(0, ACP_TRANSCRIPT_CACHE_LIMIT);
      writeTranscriptCacheIndex(storage, prunedIndex);
      nextIndex.slice(ACP_TRANSCRIPT_CACHE_LIMIT).forEach((staleId) => {
        storage.removeItem(conversationTranscriptCacheKey(staleId));
      });
    } catch {
      // ignore storage errors
    }
    if (preview) {
      page.previewByConversationId.set(conversationId, preview);
    }
  }

  function hydrateCachedConversationPreviews(page) {
    page.conversations.forEach((conversation) => {
      const id = conversation?.metadata?.name || '';
      if (!id) return;
      const cached = readCachedConversationRecord(id);
      if (cached?.preview) {
        page.previewByConversationId.set(id, cached.preview);
      }
    });
  }

  function restoreCachedConversationTranscript(page) {
    const conversationId = page.selectedConversation?.metadata?.name || '';
    if (!conversationId) return false;
    const cached = readCachedConversationRecord(conversationId);
    if (!cached?.transcript) return false;
    page.transcript = ACPRender.hydrateTranscript(cached.transcript);
    if (cached.preview) {
      page.previewByConversationId.set(conversationId, cached.preview);
    }
    return page.transcript.messages.length > 0;
  }

  function reportACPError(page, err, fallback, kind = 'error') {
    if (isBenignACPError(err)) return;
    showACPToast(page, err?.message || fallback, kind);
  }

  function getAgentTitle(agent) {
    return (
      agent?.spritz?.status?.acp?.agentInfo?.title ||
      agent?.spritz?.status?.acp?.agentInfo?.name ||
      agent?.spritz?.metadata?.name ||
      'Agent'
    );
  }

  function getAgentVersion(agent) {
    return agent?.spritz?.status?.acp?.agentInfo?.version || '';
  }

  function getAgentAvatarLabel(agent) {
    const title = getAgentTitle(agent);
    const words = title.split(/\s+/).filter(Boolean);
    return (words[0]?.[0] || 'A') + (words[1]?.[0] || words[0]?.[1] || '');
  }

  function formatRelativeTime(value) {
    if (!value) return '';
    const date = new Date(value);
    if (Number.isNaN(date.getTime())) return '';
    const now = new Date();
    const today = new Date(now.getFullYear(), now.getMonth(), now.getDate());
    const target = new Date(date.getFullYear(), date.getMonth(), date.getDate());
    const diffDays = Math.round((today.getTime() - target.getTime()) / 86400000);
    if (diffDays === 0) {
      return date.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' });
    }
    if (diffDays === 1) {
      return 'Yesterday';
    }
    if (diffDays > 1 && diffDays < 7) {
      return date.toLocaleDateString([], { weekday: 'short' });
    }
    return date.toLocaleDateString([], { day: '2-digit', month: '2-digit', year: 'numeric' });
  }

  function buildThreadMeta(agent, conversation) {
    const parts = [];
    if (agent?.spritz?.metadata?.name) {
      parts.push(agent.spritz.metadata.name);
    }
    if (getAgentVersion(agent)) {
      parts.push(`v${getAgentVersion(agent)}`);
    }
    if (conversation?.spec?.sessionId) {
      parts.push(`session ${conversation.spec.sessionId}`);
    }
    return parts.join(' · ');
  }

  function getConversationUpdatedAt(conversation) {
    return (
      conversation?.status?.updatedAt ||
      conversation?.metadata?.creationTimestamp ||
      conversation?.spec?.updatedAt ||
      ''
    );
  }

  function updateConversationPreview(page) {
    if (!page.selectedConversation?.metadata?.name) return;
    const preview = ACPRender.getPreviewText(page.transcript);
    if (preview) {
      page.previewByConversationId.set(page.selectedConversation.metadata.name, preview);
    }
  }

  function renderAgentPicker(page) {
    if (!page.agentSelectEl) return;
    page.agentSelectEl.innerHTML = '';
    if (!page.agents.length) {
      const option = document.createElement('option');
      option.value = '';
      option.textContent = 'No ACP-ready workspaces';
      page.agentSelectEl.appendChild(option);
      page.agentSelectEl.disabled = true;
      return;
    }
    page.agentSelectEl.disabled = false;
    page.agents.forEach((agent) => {
      const name = agent?.spritz?.metadata?.name || '';
      const option = document.createElement('option');
      option.value = name;
      option.textContent = `${getAgentTitle(agent)} · ${name}`;
      option.selected = name === page.selectedName;
      page.agentSelectEl.appendChild(option);
    });
  }

  function renderConversationList(page) {
    if (!page.threadListEl) return;
    page.threadListEl.innerHTML = '';
    if (!page.selectedAgent) {
      const empty = document.createElement('p');
      empty.className = 'acp-empty acp-empty--sidebar';
      empty.textContent = 'Choose an ACP-ready workspace to load conversations.';
      page.threadListEl.appendChild(empty);
      page.newConversationBtn.disabled = true;
      return;
    }
    page.newConversationBtn.disabled = false;
    if (!page.conversations.length) {
      const empty = document.createElement('p');
      empty.className = 'acp-empty acp-empty--sidebar';
      empty.textContent = 'No conversations yet. Start one from the button above.';
      page.threadListEl.appendChild(empty);
      return;
    }

    page.conversations.forEach((conversation) => {
      const id = conversation.metadata?.name || '';
      const button = document.createElement('button');
      button.type = 'button';
      button.className = 'acp-thread-item';
      button.dataset.active = String(id === page.selectedConversationId);
      button.onclick = () => {
        window.location.assign(chatPagePath(page.selectedName, id));
      };

      const avatar = document.createElement('div');
      avatar.className = 'acp-thread-avatar';
      avatar.textContent = getAgentAvatarLabel(page.selectedAgent).slice(0, 2).toUpperCase();

      const body = document.createElement('div');
      body.className = 'acp-thread-item-body';
      const top = document.createElement('div');
      top.className = 'acp-thread-item-top';
      const title = document.createElement('strong');
      title.className = 'acp-thread-item-title';
      title.textContent = conversation.spec?.title || 'New conversation';
      const time = document.createElement('span');
      time.className = 'acp-thread-item-time';
      time.textContent = formatRelativeTime(getConversationUpdatedAt(conversation));
      top.append(title, time);

      const preview = document.createElement('p');
      preview.className = 'acp-thread-item-preview';
      preview.textContent =
        page.previewByConversationId.get(id) ||
        buildThreadMeta(page.selectedAgent, conversation) ||
        'Ready to chat';

      const meta = document.createElement('p');
      meta.className = 'acp-thread-item-meta';
      meta.textContent = buildThreadMeta(page.selectedAgent, conversation);

      body.append(top, preview, meta);
      button.append(avatar, body);
      page.threadListEl.appendChild(button);
    });
  }

  function renderPermissionPrompt(page) {
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
          reportACPError(page, err, 'Failed to send permission response.');
        } finally {
          page.permissionQueue.shift();
          renderPermissionPrompt(page);
        }
      };
      page.permissionOptionsEl.appendChild(button);
    });
  }

  function renderCommandBar(page) {
    if (!page.commandBarEl) return;
    page.commandBarEl.innerHTML = '';
    const items = ACPRender.buildCommandItems(page.transcript.availableCommands);
    if (!items.length) {
      page.commandBarEl.hidden = true;
      return;
    }
    page.commandBarEl.hidden = false;
    items.forEach((item) => {
      const tag = document.createElement('span');
      tag.className = 'acp-command-pill';
      tag.textContent = item.label;
      if (item.title) tag.title = item.title;
      page.commandBarEl.appendChild(tag);
    });
  }

  function renderModeBar(page) {
    if (!page.statusMetaEl) return;
    page.statusMetaEl.innerHTML = '';
    const badges = [];
    if (page.transcript.currentMode) {
      badges.push({ label: `Mode · ${page.transcript.currentMode}` });
    }
    if (page.transcript.usage && page.transcript.usage.used !== null && page.transcript.usage.size !== null) {
      badges.push({ label: `${page.transcript.usage.used}/${page.transcript.usage.size} used` });
    }
    badges.forEach((badgeInfo) => {
      const badge = document.createElement('span');
      badge.className = 'acp-inline-badge';
      badge.textContent = badgeInfo.label;
      page.statusMetaEl.appendChild(badge);
    });
  }

  function renderThread(page) {
    if (!page.threadTitleEl || !page.threadMetaEl || !page.threadStreamEl) return;
    const openUrl = page.deps.buildOpenUrl(page.selectedAgent?.spritz?.status?.url, page.selectedAgent?.spritz);
    page.openBtn.disabled = !openUrl;
    page.threadTitleEl.textContent = page.selectedConversation?.spec?.title || getAgentTitle(page.selectedAgent);
    page.threadMetaEl.textContent = buildThreadMeta(page.selectedAgent, page.selectedConversation);
    page.threadStreamEl.innerHTML = '';
    renderCommandBar(page);
    renderModeBar(page);

    if (!page.selectedAgent) {
      const empty = document.createElement('div');
      empty.className = 'acp-empty';
      empty.textContent = 'Choose an ACP-ready workspace to start chatting.';
      page.threadStreamEl.appendChild(empty);
      renderPermissionPrompt(page);
      return;
    }

    if (!page.selectedConversation) {
      const empty = document.createElement('div');
      empty.className = 'acp-empty';
      empty.textContent = 'Choose a conversation or start a new one.';
      page.threadStreamEl.appendChild(empty);
      renderPermissionPrompt(page);
      return;
    }

    if (!page.transcript.messages.length) {
      const intro = document.createElement('div');
      intro.className = 'acp-welcome-card';
      const heading = document.createElement('strong');
      heading.textContent = getAgentTitle(page.selectedAgent);
      const copy = document.createElement('p');
      copy.textContent = 'Conversation is ready. Send a message to begin.';
      intro.append(heading, copy);
      page.threadStreamEl.appendChild(intro);
    } else {
      page.transcript.messages.forEach((message) => {
        page.threadStreamEl.appendChild(ACPRender.renderMessage(message));
      });
    }
    page.threadBodyEl.scrollTop = page.threadBodyEl.scrollHeight;
    renderPermissionPrompt(page);
  }

  function setStatus(page, text) {
    if (!page.statusEl) return;
    page.statusEl.textContent = text || '';
  }

  function syncComposer(page) {
    const disabled = !page.client || !page.client.isReady() || !page.selectedConversation;
    if (page.composerEl) page.composerEl.disabled = disabled || page.promptInFlight;
    if (page.sendBtn) page.sendBtn.disabled = disabled || page.promptInFlight;
    if (page.cancelBtn) page.cancelBtn.disabled = !page.promptInFlight;
  }

  async function patchSelectedConversation(page, payload) {
    if (!page.selectedConversation?.metadata?.name) return;
    const updated = await patchACPConversationData(page.deps, page.selectedConversation.metadata.name, payload);
    page.selectedConversation = updated;
    page.selectedConversationId = updated.metadata?.name || page.selectedConversationId;
    page.conversations = page.conversations.map((item) =>
      item.metadata?.name === updated.metadata?.name ? updated : item,
    );
    renderConversationList(page);
    renderThread(page);
  }

  function applyACPUpdate(page, update) {
    const result = ACPRender.applySessionUpdate(page.transcript, update, {
      historical: !page.bootstrapComplete,
    });
    if (result?.conversationTitle) {
      patchSelectedConversation(page, { title: result.conversationTitle }).catch(() => {});
    }
    updateConversationPreview(page);
    writeCachedConversationRecord(page);
    renderConversationList(page);
    renderThread(page);
  }

  function resetConversationRuntime(page) {
    page.transcript = ACPRender.createTranscript();
    page.permissionQueue = [];
    page.promptInFlight = false;
    if (page.client) {
      page.client.dispose();
      page.client = null;
    }
    if (page.reconnectTimer) {
      clearTimeout(page.reconnectTimer);
      page.reconnectTimer = null;
    }
    syncComposer(page);
  }

  function scheduleReconnect(page) {
    if (page.destroyed || !page.selectedConversation || !page.selectedName) return;
    if (page.reconnectTimer) {
      clearTimeout(page.reconnectTimer);
    }
    page.reconnectTimer = setTimeout(() => {
      connectSelectedConversation(page).catch((err) => {
        setStatus(page, err.message || 'Disconnected');
      });
    }, 2000);
    setStatus(page, 'Disconnected. Reconnecting…');
  }

  async function connectSelectedConversation(page) {
    if (!page.selectedAgent || !page.selectedConversation) {
      resetConversationRuntime(page);
      renderThread(page);
      return;
    }
    page.bootstrapComplete = false;
    page.cacheHydratedTranscript = page.transcript.messages.length > 0;
    page.cacheReplacedByReplay = false;
    renderThread(page);
    page.client = createACPClient({
      wsUrl: acpWsUrl(page.selectedName, page.deps),
      conversation: page.selectedConversation,
      onStatus(text) {
        setStatus(page, text);
      },
      onReadyChange() {
        syncComposer(page);
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
        renderAgentPicker(page);
        renderConversationList(page);
        renderThread(page);
      },
      onUpdate(update) {
        if (
          !page.bootstrapComplete &&
          page.cacheHydratedTranscript &&
          !page.cacheReplacedByReplay &&
          ACPRender.isTranscriptBearingUpdate(update)
        ) {
          page.transcript = ACPRender.createTranscript();
          page.cacheReplacedByReplay = true;
        }
        applyACPUpdate(page, update);
      },
      onPermissionRequest(entry) {
        page.permissionQueue.push(entry);
        renderPermissionPrompt(page);
      },
      async onSessionId(sessionId) {
        await patchSelectedConversation(page, { sessionId });
      },
      onPromptStateChange(value) {
        page.promptInFlight = value;
        if (!value) {
          ACPRender.finalizeStreaming(page.transcript);
          updateConversationPreview(page);
          writeCachedConversationRecord(page);
          renderConversationList(page);
        }
        syncComposer(page);
        renderThread(page);
      },
      onClose() {
        scheduleReconnect(page);
      },
      onProtocolError(err) {
        reportACPError(page, err, 'Invalid ACP message.', 'info');
      },
    });
    syncComposer(page);
    await page.client.start();
    if (page.cacheHydratedTranscript && !page.cacheReplacedByReplay) {
      page.transcript = ACPRender.createTranscript();
      renderConversationList(page);
      renderThread(page);
    }
    page.bootstrapComplete = true;
    writeCachedConversationRecord(page);
    clearACPNotice(page);
  }

  async function selectConversation(page, conversationId) {
    page.selectedConversationId = conversationId || '';
    page.selectedConversation = page.conversations.find((item) => item.metadata?.name === conversationId) || null;
    resetConversationRuntime(page);
    if (page.selectedConversation) {
      restoreCachedConversationTranscript(page);
    }
    renderConversationList(page);
    renderThread(page);
    if (page.selectedConversation) {
      await connectSelectedConversation(page);
    } else {
      setStatus(page, 'Choose or create a conversation.');
    }
  }

  async function refreshConversations(page) {
    if (!page.selectedName) {
      page.conversations = [];
      page.selectedConversation = null;
      page.selectedConversationId = '';
      renderConversationList(page);
      renderThread(page);
      return;
    }
    page.conversations = await listACPConversationsData(page.deps, page.selectedName);
    hydrateCachedConversationPreviews(page);
    const routeConversationId = conversationIdFromHash(window.location.hash || '');
    const resolvedConversationId =
      page.conversations.find((item) => item.metadata?.name === routeConversationId)?.metadata?.name ||
      page.selectedConversationId ||
      page.conversations[0]?.metadata?.name ||
      '';
    renderConversationList(page);
    if (routeConversationId && resolvedConversationId !== routeConversationId) {
      window.location.replace(chatPagePath(page.selectedName, resolvedConversationId));
      return;
    }
    await selectConversation(page, resolvedConversationId);
  }

  async function loadACPPage(page) {
    try {
      clearACPNotice(page);
      setStatus(page, 'Loading workspaces…');
      page.agents = await fetchACPAgentsData(page.deps);
      renderAgentPicker(page);
      if (!page.agents.length) {
        page.selectedAgent = null;
        page.conversations = [];
        page.selectedConversation = null;
        page.selectedConversationId = '';
        resetConversationRuntime(page);
        renderConversationList(page);
        renderThread(page);
        setStatus(page, 'No ACP-ready workspaces.');
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
      renderConversationList(page);
      renderThread(page);
      await refreshConversations(page);
      if (!page.selectedConversation) {
        setStatus(page, 'Choose or create a conversation.');
      }
    } catch (err) {
      if (isBenignACPError(err)) {
        return;
      }
      setStatus(page, err.message || 'Failed to load ACP page.');
      reportACPError(page, err, 'Failed to load ACP page.');
    }
  }

  function renderACPPage(name, conversationId, deps) {
    deps.cleanupTerminal();
    if (deps.activePage) {
      deps.activePage.destroy();
    }
    if (deps.createSection) deps.createSection.hidden = true;
    if (deps.listSection) deps.listSection.hidden = true;
    if (deps.shellEl?.dataset) deps.shellEl.dataset.view = 'chat';
    deps.setHeaderCopy('Spritz', 'Agent chat');

    const page = createACPPageState(name, conversationId, deps);

    const shell = document.createElement('section');
    shell.className = 'card acp-shell';

    const sidebar = document.createElement('aside');
    sidebar.className = 'acp-sidebar';
    const sidebarTop = document.createElement('div');
    sidebarTop.className = 'acp-sidebar-top';

    const nav = document.createElement('div');
    nav.className = 'acp-sidebar-nav';
    const backLink = document.createElement('a');
    backLink.href = '/';
    backLink.className = 'header-link';
    backLink.textContent = 'Back';
    const refreshButton = document.createElement('button');
    refreshButton.type = 'button';
    refreshButton.className = 'ghost';
    refreshButton.textContent = 'Refresh';
    nav.append(backLink, refreshButton);

    const titleGroup = document.createElement('div');
    titleGroup.className = 'acp-sidebar-title';
    const title = document.createElement('h2');
    title.textContent = 'Agent chat';
    const subtitle = document.createElement('p');
    subtitle.textContent = 'Talk to ACP-ready workspaces through Spritz.';
    titleGroup.append(title, subtitle);

    const agentSelect = document.createElement('select');
    agentSelect.className = 'acp-agent-select';

    const newConversationButton = document.createElement('button');
    newConversationButton.type = 'button';
    newConversationButton.textContent = 'New conversation';

    const threadList = document.createElement('div');
    threadList.className = 'acp-thread-list';

    sidebarTop.append(nav, titleGroup, agentSelect, newConversationButton);
    sidebar.append(sidebarTop, threadList);

    const main = document.createElement('section');
    main.className = 'acp-main';

    const header = document.createElement('div');
    header.className = 'acp-main-header';
    const headerCopy = document.createElement('div');
    headerCopy.className = 'acp-main-copy';
    const threadTitle = document.createElement('h2');
    threadTitle.textContent = 'Agent';
    const threadMeta = document.createElement('p');
    headerCopy.append(threadTitle, threadMeta);

    const headerActions = document.createElement('div');
    headerActions.className = 'acp-main-actions';
    const openButton = document.createElement('button');
    openButton.type = 'button';
    openButton.textContent = 'Open workspace';
    headerActions.append(openButton);

    const headerTop = document.createElement('div');
    headerTop.className = 'acp-main-header-top';
    headerTop.append(headerCopy, headerActions);

    const commandBar = document.createElement('div');
    commandBar.className = 'acp-command-bar';
    commandBar.hidden = true;

    header.append(headerTop, commandBar);

    const body = document.createElement('div');
    body.className = 'acp-main-body';
    const stream = document.createElement('div');
    stream.className = 'acp-stream';
    body.appendChild(stream);

    const footer = document.createElement('div');
    footer.className = 'acp-main-footer';
    const footerInner = document.createElement('div');
    footerInner.className = 'acp-footer-inner';

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
    const statusMeta = document.createElement('div');
    statusMeta.className = 'acp-status-meta';
    const hint = document.createElement('span');
    hint.className = 'acp-hint';
    hint.textContent = 'Enter sends. Shift+Enter adds a new line.';
    statusRow.append(statusEl, statusMeta, hint);

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

    footerInner.append(permissionBox, statusRow, composer);
    footer.appendChild(footerInner);

    main.append(header, body, footer);
    shell.append(sidebar, main);
    deps.shellEl.append(shell);

    page.card = shell;
    page.agentSelectEl = agentSelect;
    page.threadListEl = threadList;
    page.threadTitleEl = threadTitle;
    page.threadMetaEl = threadMeta;
    page.commandBarEl = commandBar;
    page.threadBodyEl = body;
    page.threadStreamEl = stream;
    page.permissionEl = permissionBox;
    page.permissionTextEl = permissionText;
    page.permissionOptionsEl = permissionOptions;
    page.statusEl = statusEl;
    page.statusMetaEl = statusMeta;
    page.composerEl = composerInput;
    page.sendBtn = sendButton;
    page.cancelBtn = cancelButton;
    page.refreshBtn = refreshButton;
    page.newConversationBtn = newConversationButton;
    page.openBtn = openButton;

    refreshButton.addEventListener('click', () => {
      loadACPPage(page);
    });

    agentSelect.addEventListener('change', () => {
      if (!agentSelect.value) return;
      window.location.assign(chatPagePath(agentSelect.value));
    });

    newConversationButton.addEventListener('click', async () => {
      if (!page.selectedName) return;
      try {
        const conversation = await createACPConversationData(page.deps, page.selectedName);
        page.conversations = [conversation, ...page.conversations];
        renderConversationList(page);
        window.location.assign(chatPagePath(page.selectedName, conversation.metadata?.name || ''));
      } catch (err) {
        reportACPError(page, err, 'Failed to create conversation.');
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
      ACPRender.applySessionUpdate(page.transcript, {
        sessionUpdate: 'user_message_chunk',
        content: { type: 'text', text },
      });
      ACPRender.finalizeStreaming(page.transcript);
      updateConversationPreview(page);
      renderConversationList(page);
      renderThread(page);
      if (!page.selectedConversation.spec?.title || page.selectedConversation.spec.title === 'New conversation') {
        const nextTitle = text.slice(0, 80);
        patchSelectedConversation(page, { title: nextTitle }).catch(() => {});
      }
      setStatus(page, 'Waiting for agent…');
      try {
        const result = await page.client.sendPrompt(text);
        setStatus(page, result?.stopReason ? `Completed · ${result.stopReason}` : 'Completed');
      } catch (err) {
        reportACPError(page, err, 'Failed to send ACP prompt.');
      } finally {
        syncComposer(page);
        renderThread(page);
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
      resetConversationRuntime(page);
      if (page.card) {
        page.card.remove();
      }
      if (deps.createSection) deps.createSection.hidden = false;
      if (deps.listSection) deps.listSection.hidden = false;
      if (deps.shellEl?.dataset) delete deps.shellEl.dataset.view;
    };

    syncComposer(page);
    renderAgentPicker(page);
    renderConversationList(page);
    renderThread(page);
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
