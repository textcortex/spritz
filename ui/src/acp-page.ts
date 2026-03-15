(function (global) {
  const { createACPClient } = global.SpritzACPClient;
  const ACPRender = global.SpritzACPRender;
  const ACP_TRANSCRIPT_CACHE_PREFIX = 'spritz:acp:thread:';
  const ACP_TRANSCRIPT_CACHE_INDEX_KEY = 'spritz:acp:thread:index';
  const ACP_TRANSCRIPT_CACHE_LIMIT = 25;
  const PRE_CUTOVER_ACP_TRANSCRIPT_CACHE_PREFIX = 'spritz:acp:transcript:';
  const PRE_CUTOVER_ACP_TRANSCRIPT_CACHE_INDEX_KEY = 'spritz:acp:transcript:index';

  type ACPPage = any;
  type ACPConversationBootstrapOptions = {
    forceBootstrap?: boolean;
    allowRepairRetry?: boolean;
    allowAutoRebind?: boolean;
  };

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

  function acpWsUrl(conversationId, deps) {
    const base = deps.apiBaseUrl || '';
    const resolved = base.startsWith('http') ? base : `${window.location.origin}${base}`;
    const wsBase = resolved.replace(/^http/, 'ws');
    const token = deps.getAuthToken();
    const query = token ? `?${encodeURIComponent(deps.authBearerTokenParam)}=${encodeURIComponent(token)}` : '';
    return `${wsBase}/acp/conversations/${encodeURIComponent(conversationId)}/connect${query}`;
  }

  async function fetchACPAgentsData(deps) {
    const data = await deps.request('/acp/agents');
    return data.items || [];
  }

  async function fetchSpritzData(deps, spritzName) {
    return deps.request(`/spritzes/${encodeURIComponent(spritzName)}`);
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

  async function bootstrapACPConversationData(deps, id) {
    return deps.request(`/acp/conversations/${encodeURIComponent(id)}/bootstrap`, {
      method: 'POST',
    });
  }

  function createACPPageState(name, conversationId, deps) {
    return {
      deps,
      selectedName: name || '',
      selectedConversationId: conversationId || '',
      selectedSpritz: null,
      workspaceState: 'empty',
      agents: [],
      selectedAgent: null,
      conversations: [],
      agentConversations: {},
      selectedConversation: null,
      transcript: ACPRender.createTranscript(),
      permissionQueue: [],
      previewByConversationId: new Map(),
      promptInFlight: false,
      client: null,
      reconnectTimer: null,
      workspaceRefreshTimer: null,
      destroyed: false,
      bootstrapComplete: false,
      cacheHydratedTranscript: false,
      cacheReplacedByReplay: false,
      _savedThinkingDone: null,
    };
  }

  function selectedWorkspace(page) {
    return page.selectedAgent?.spritz || page.selectedSpritz || null;
  }

  function isACPReadyWorkspace(spritz) {
    return String(spritz?.status?.acp?.state || '').trim().toLowerCase() === 'ready';
  }

  function workspacePhase(spritz) {
    return String(spritz?.status?.phase || '').trim() || 'Unknown';
  }

  function workspaceStatusSummary(spritz) {
    const name = String(spritz?.metadata?.name || '').trim() || 'Workspace';
    const phase = workspacePhase(spritz);
    const message = String(spritz?.status?.message || '').trim();
    if (!spritz) {
      return {
        title: 'Workspace not found',
        copy: 'The requested workspace does not exist or is no longer visible to your account.',
        status: 'Workspace not found.',
      };
    }
    if (String(phase).toLowerCase() === 'ready' && !isACPReadyWorkspace(spritz)) {
      return {
        title: name,
        copy: message || 'Workspace is ready, but chat services are still starting.',
        status: 'Preparing chat…',
      };
    }
    if (['failed', 'error'].includes(String(phase).toLowerCase())) {
      return {
        title: name,
        copy: message || 'Workspace failed to start correctly.',
        status: 'Workspace failed.',
      };
    }
    return {
      title: name,
      copy: message || 'Workspace is still provisioning. Chat will unlock automatically once it is ready.',
      status: 'Provisioning workspace…',
    };
  }

  function clearWorkspaceRefresh(page) {
    if (page.workspaceRefreshTimer) {
      clearTimeout(page.workspaceRefreshTimer);
      page.workspaceRefreshTimer = null;
    }
  }

  function scheduleWorkspaceRefresh(page) {
    clearWorkspaceRefresh(page);
    if (page.destroyed || page.workspaceState === 'ready' || !page.selectedName) return;
    page.workspaceRefreshTimer = setTimeout(() => {
      loadACPPage(page);
    }, 5000);
    if (typeof page.workspaceRefreshTimer?.unref === 'function') {
      page.workspaceRefreshTimer.unref();
    }
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

  function normalizeACPToastMessage(message) {
    const raw = String(message || '').trim();
    if (!raw) return '';
    const htmlError = ACPRender.detectHtmlErrorDocument?.(raw);
    return htmlError?.text || raw;
  }

  function showACPToast(page, message, type = 'error') {
    const normalized = normalizeACPToastMessage(message);
    if (!normalized) return;
    if (typeof page.deps.showToast === 'function') {
      page.deps.showToast(normalized, type);
      return;
    }
    if (typeof page.deps.showNotice === 'function') {
      page.deps.showNotice(normalized, type);
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
    return readTranscriptCacheIndexFromKey(storage, ACP_TRANSCRIPT_CACHE_INDEX_KEY);
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

  function purgePreCutoverTranscriptCache(storage) {
    if (!storage) return;
    try {
      const priorIndex = readTranscriptCacheIndexFromKey(storage, PRE_CUTOVER_ACP_TRANSCRIPT_CACHE_INDEX_KEY);
      priorIndex.forEach((conversationId) => {
        storage.removeItem(`${PRE_CUTOVER_ACP_TRANSCRIPT_CACHE_PREFIX}${conversationId}`);
      });
      storage.removeItem(PRE_CUTOVER_ACP_TRANSCRIPT_CACHE_INDEX_KEY);
    } catch {
      // ignore storage errors
    }
  }

  function readTranscriptCacheIndexFromKey(storage, key) {
    if (!storage) return [];
    try {
      const raw = storage.getItem(key);
      const parsed = raw ? JSON.parse(raw) : [];
      return Array.isArray(parsed) ? parsed.filter((item) => typeof item === 'string' && item) : [];
    } catch {
      return [];
    }
  }

  function clearCachedConversationRecord(conversationId) {
    const normalizedId = String(conversationId || '').trim();
    if (!normalizedId) return;
    const storage = safeSessionStorage();
    if (!storage) return;
    purgePreCutoverTranscriptCache(storage);
    try {
      storage.removeItem(`${PRE_CUTOVER_ACP_TRANSCRIPT_CACHE_PREFIX}${normalizedId}`);
      storage.removeItem(conversationTranscriptCacheKey(normalizedId));
      writeTranscriptCacheIndex(
        storage,
        readTranscriptCacheIndex(storage).filter((item) => item !== normalizedId),
      );
    } catch {
      // ignore storage errors
    }
  }

  function readCachedConversationRecord(conversationId) {
    const normalizedId = String(conversationId || '').trim();
    if (!normalizedId) return null;
    const storage = safeSessionStorage();
    if (!storage) return null;
    purgePreCutoverTranscriptCache(storage);
    try {
      storage.removeItem(`${PRE_CUTOVER_ACP_TRANSCRIPT_CACHE_PREFIX}${normalizedId}`);
      const raw = storage.getItem(conversationTranscriptCacheKey(normalizedId));
      if (!raw) return null;
      const parsed = JSON.parse(raw);
      if (!parsed || typeof parsed !== 'object') {
        clearCachedConversationRecord(normalizedId);
        return null;
      }
      if (
        ACPRender.transcriptContainsHtmlError?.(parsed.transcript) ||
        ACPRender.detectHtmlErrorDocument?.(parsed.preview)
      ) {
        clearCachedConversationRecord(normalizedId);
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
    purgePreCutoverTranscriptCache(storage);

    const preview = ACPRender.getPreviewText(page.transcript);
    const payload = {
      conversationId,
      spritzName: page.selectedName || '',
      sessionId: page.selectedConversation?.spec?.sessionId || '',
      updatedAt: new Date().toISOString(),
      preview,
      transcript: ACPRender.serializeTranscript(page.transcript),
    };
    try {
      storage.removeItem(`${PRE_CUTOVER_ACP_TRANSCRIPT_CACHE_PREFIX}${conversationId}`);
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

  function reportACPError(page, err, fallback, type = 'error') {
    if (isBenignACPError(err)) return;
    showACPToast(page, err?.message || fallback, type);
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
    const selectedName = page.selectedName || page.selectedSpritz?.metadata?.name || '';
    const selectedReady = page.agents.some((agent) => agent?.spritz?.metadata?.name === selectedName);
    if (!selectedReady && page.selectedSpritz?.metadata?.name) {
      const option = document.createElement('option');
      option.value = page.selectedSpritz.metadata.name;
      option.textContent = `${page.selectedSpritz.metadata.name} · ${workspacePhase(page.selectedSpritz).toLowerCase()}`;
      option.selected = true;
      page.agentSelectEl.appendChild(option);
    }
    if (!page.agents.length) {
      if (page.selectedSpritz?.metadata?.name) {
        page.agentSelectEl.disabled = false;
        return;
      }
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

    if (!page.agents || !page.agents.length) {
      return;
    }

    const agentConvos = page.agentConversations || {};

    page.agents.forEach((agent) => {
      const spritzName = agent?.spritz?.metadata?.name || '';
      if (!spritzName) return;

      const group = document.createElement('div');
      group.className = 'acp-agent-group';

      const header = document.createElement('button');
      header.type = 'button';
      header.className = 'acp-agent-header';
      if (spritzName === page.selectedName) {
        header.dataset.active = 'true';
      }

      const headerLabel = document.createElement('span');
      headerLabel.className = 'acp-agent-header-label';
      headerLabel.textContent = getAgentTitle(agent);

      const chevron = document.createElement('span');
      chevron.className = 'acp-agent-chevron';

      const addBtn = document.createElement('button');
      addBtn.type = 'button';
      addBtn.className = 'acp-agent-add-btn';
      addBtn.innerHTML = '<svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"><line x1="12" y1="5" x2="12" y2="19"/><line x1="5" y1="12" x2="19" y2="12"/></svg>';
      addBtn.addEventListener('click', async (e) => {
        e.stopPropagation();
        try {
          const conversation = await createACPConversationData(page.deps, spritzName);
          window.location.assign(chatPagePath(spritzName, conversation.metadata?.name || ''));
        } catch (err) {
          reportACPError(page, err, 'Failed to create conversation.');
        }
      });

      header.append(headerLabel, chevron, addBtn);

      const convos = agentConvos[spritzName] || [];
      const convoList = document.createElement('div');
      convoList.className = 'acp-agent-convos';

      // Expand this agent group if it's the selected one or has no selection yet
      const isExpanded = spritzName === page.selectedName || !page.selectedName;
      group.dataset.expanded = String(isExpanded);

      header.addEventListener('click', () => {
        const expanded = group.dataset.expanded === 'true';
        group.dataset.expanded = String(!expanded);
      });

      convos.forEach((conversation) => {
        const id = conversation.metadata?.name || '';
        const button = document.createElement('button');
        button.type = 'button';
        button.className = 'acp-thread-item';
        button.dataset.active = String(id === page.selectedConversationId && spritzName === page.selectedName);
        button.onclick = () => {
          window.location.assign(chatPagePath(spritzName, id));
        };

        const title = document.createElement('span');
        title.className = 'acp-thread-item-title';
        title.textContent = conversation.spec?.title || 'New conversation';
        button.append(title);
        convoList.appendChild(button);
      });

      group.append(header, convoList);
      page.threadListEl.appendChild(group);
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
      tag.style.cursor = 'pointer';
      tag.addEventListener('click', () => {
        if (page.composerEl) {
          page.composerEl.value = item.label + ' ';
          page.composerEl.focus();
          page.composerEl.dispatchEvent(new Event('input'));
        }
      });
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

  function renderPresetGallery(page) {
    if (!page.threadStreamEl) return;
    const presets = page.deps.presets || [];

    const gallery = document.createElement('div');
    gallery.className = 'acp-preset-gallery';

    const title = document.createElement('h3');
    title.className = 'acp-preset-gallery-title';
    title.textContent = 'Create a new Spritz';
    gallery.appendChild(title);

    const grid = document.createElement('div');
    grid.className = 'acp-preset-grid';

    presets.forEach((preset) => {
      const card = document.createElement('button');
      card.type = 'button';
      card.className = 'acp-preset-card';
      const name = document.createElement('strong');
      name.textContent = preset.name || preset.id || 'Preset';
      const desc = document.createElement('p');
      desc.textContent = preset.description || preset.image || '';
      card.append(name, desc);
      card.addEventListener('click', async () => {
        card.disabled = true;
        card.textContent = 'Creating…';
        try {
          const payload: any = {
            spec: { image: preset.image },
          };
          if (preset.id) payload.presetId = preset.id;
          if (preset.namePrefix) payload.namePrefix = preset.namePrefix;
          if (page.deps.ownerId) payload.spec.owner = { id: page.deps.ownerId };
          await page.deps.request('/spritzes', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify(payload),
          });
          page.deps.showToast('Spritz created. Loading…', 'info');
          setTimeout(() => loadACPPage(page), 1500);
        } catch (err) {
          reportACPError(page, err, 'Failed to create Spritz.');
          card.disabled = false;
          card.innerHTML = '';
          card.append(name, desc);
        }
      });
      grid.appendChild(card);
    });

    // Custom card
    const customCard = document.createElement('button');
    customCard.type = 'button';
    customCard.className = 'acp-preset-card acp-preset-card--custom';
    const customName = document.createElement('strong');
    customName.textContent = 'Custom';
    const customDesc = document.createElement('p');
    customDesc.textContent = 'Configure image, repo, and more';
    customCard.append(customName, customDesc);
    customCard.addEventListener('click', () => {
      openCreateModal(page);
    });
    grid.appendChild(customCard);

    gallery.appendChild(grid);
    page.threadStreamEl.appendChild(gallery);
  }

  function openCreateModal(page) {
    const existing = page.card?.querySelector('.acp-create-modal-backdrop');
    if (existing) existing.remove();

    const backdrop = document.createElement('div');
    backdrop.className = 'acp-create-modal-backdrop';

    const modal = document.createElement('div');
    modal.className = 'acp-create-modal';

    const header = document.createElement('div');
    header.className = 'acp-create-modal-header';
    const headerTitle = document.createElement('h3');
    headerTitle.textContent = 'Create Custom Spritz';
    const closeBtn = document.createElement('button');
    closeBtn.type = 'button';
    closeBtn.className = 'acp-nav-icon';
    closeBtn.innerHTML = '<svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"><line x1="18" y1="6" x2="6" y2="18"/><line x1="6" y1="6" x2="18" y2="18"/></svg>';
    closeBtn.addEventListener('click', () => backdrop.remove());
    header.append(headerTitle, closeBtn);

    const form = document.createElement('form');
    form.className = 'acp-create-modal-form';

    const fields = [
      { label: 'Image', name: 'image', placeholder: 'spritz-starter:latest', required: true },
      { label: 'Name', name: 'name', placeholder: 'Auto-generated' },
      { label: 'Repository URL', name: 'repo', placeholder: 'https://github.com/...' },
      { label: 'Branch', name: 'branch', placeholder: 'main' },
      { label: 'TTL', name: 'ttl', placeholder: '8h' },
      { label: 'Namespace', name: 'namespace', placeholder: 'default' },
    ];

    fields.forEach((field) => {
      const label = document.createElement('label');
      label.textContent = field.label;
      const input = document.createElement('input');
      input.name = field.name;
      input.placeholder = field.placeholder;
      if (field.required) input.required = true;
      label.appendChild(input);
      form.appendChild(label);
    });

    const submitBtn = document.createElement('button');
    submitBtn.type = 'submit';
    submitBtn.textContent = 'Create Spritz';
    form.appendChild(submitBtn);

    form.addEventListener('submit', async (e) => {
      e.preventDefault();
      const data = new FormData(form);
      const payload: any = {
        spec: { image: (data.get('image') || '').toString().trim() },
      };
      const rawName = (data.get('name') || '').toString().trim();
      if (rawName) payload.name = rawName;
      if (page.deps.ownerId) payload.spec.owner = { id: page.deps.ownerId };
      const repo = (data.get('repo') || '').toString().trim();
      if (repo) {
        payload.spec.repo = { url: repo };
        const branch = (data.get('branch') || '').toString().trim();
        if (branch) payload.spec.repo.branch = branch;
      }
      const ttl = (data.get('ttl') || '').toString().trim();
      if (ttl) payload.spec.ttl = ttl;
      const ns = (data.get('namespace') || '').toString().trim();
      if (ns) payload.namespace = ns;

      submitBtn.disabled = true;
      submitBtn.textContent = 'Creating…';
      try {
        await page.deps.request('/spritzes', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify(payload),
        });
        backdrop.remove();
        page.deps.showToast('Spritz created. Loading…', 'info');
        setTimeout(() => loadACPPage(page), 1500);
      } catch (err) {
        reportACPError(page, err, 'Failed to create Spritz.');
        submitBtn.disabled = false;
        submitBtn.textContent = 'Create Spritz';
      }
    });

    modal.append(header, form);
    backdrop.appendChild(modal);
    backdrop.addEventListener('click', (e) => {
      if (e.target === backdrop) backdrop.remove();
    });
    page.card.appendChild(backdrop);
  }

  function renderThread(page) {
    if (!page.threadTitleEl || !page.threadMetaEl || !page.threadStreamEl) return;
    const currentWorkspace = selectedWorkspace(page);
    const openUrl = page.deps.buildOpenUrl(currentWorkspace?.status?.url, currentWorkspace);
    page.openBtn.disabled = !openUrl;
    page.threadTitleEl.textContent =
      page.selectedConversation?.spec?.title ||
      currentWorkspace?.metadata?.name ||
      getAgentTitle(page.selectedAgent);
    page.threadMetaEl.textContent =
      page.workspaceState === 'ready' ? buildThreadMeta(page.selectedAgent, page.selectedConversation) : '';
    renderCommandBar(page);
    renderModeBar(page);

    if (page.workspaceState !== 'ready') {
      page.threadStreamEl.innerHTML = '';
      const summary = workspaceStatusSummary(currentWorkspace);
      const card = document.createElement('div');
      card.className = 'acp-welcome-card acp-pending-card';
      const heading = document.createElement('strong');
      heading.textContent = summary.title;
      const copy = document.createElement('p');
      copy.textContent = summary.copy;
      card.append(heading, copy);
      page.threadStreamEl.appendChild(card);
      renderPermissionPrompt(page);
      return;
    }

    if (!page.selectedAgent || !page.selectedConversation) {
      renderPresetGallery(page);
      renderPermissionPrompt(page);
      return;
    }

    if (!page.transcript.messages.length) {
      page.threadStreamEl.innerHTML = '';
      const intro = document.createElement('div');
      intro.className = 'acp-welcome-card';
      const heading = document.createElement('strong');
      heading.textContent = getAgentTitle(page.selectedAgent);
      const copy = document.createElement('p');
      copy.textContent = 'Conversation is ready. Send a message to begin.';
      intro.append(heading, copy);
      page.threadStreamEl.appendChild(intro);
    } else {
      // Incremental rendering: only re-render streaming/new messages.
      // The thinking block lives in the DOM alongside message nodes.
      // We skip it during index-based diffing to avoid detaching it
      // (which would restart CSS/SMIL animations every render cycle).
      const messages = page.transcript.messages;
      const thinkingInsertIdx = page.transcript.thinkingInsertIndex ?? messages.length;
      let domIdx = 0;

      for (let mi = 0; mi < messages.length; mi++) {
        // Skip over the thinking block if it occupies this DOM slot
        let domNode = page.threadStreamEl.children[domIdx] as HTMLElement | undefined;
        if (domNode && domNode.classList.contains('acp-thinking')) {
          domIdx++;
          domNode = page.threadStreamEl.children[domIdx] as HTMLElement | undefined;
        }

        const msg = messages[mi];
        if (domNode && msg._el === domNode && !msg.streaming) {
          domIdx++;
          continue;
        }
        if (msg.streaming && msg._el === domNode && ACPRender.appendStreamingChunk(msg)) {
          domIdx++;
          continue;
        }
        const rendered = ACPRender.renderMessage(msg);
        msg._el = rendered;
        if (domNode) {
          page.threadStreamEl.replaceChild(rendered, domNode);
        } else {
          page.threadStreamEl.appendChild(rendered);
        }
        domIdx++;
      }

      // Remove extra DOM nodes (except the thinking block)
      const children = page.threadStreamEl.children;
      for (let i = children.length - 1; i >= 0; i--) {
        const child = children[i] as HTMLElement;
        if (child.classList.contains('acp-thinking')) continue;
        // Count non-thinking children up to this point
        let msgCount = 0;
        for (let j = 0; j <= i; j++) {
          if (!(children[j] as HTMLElement).classList.contains('acp-thinking')) msgCount++;
        }
        if (msgCount > messages.length) {
          page.threadStreamEl.removeChild(child);
        }
      }

      // Update the thinking block (insert, move, or remove)
      const thinkingBlock = ACPRender.renderThinkingBlock(page.transcript);
      const existingThinking = page.threadStreamEl.querySelector('.acp-thinking');
      if (thinkingBlock) {
        // Calculate the correct DOM index accounting for the thinking block itself
        let targetDomIdx = thinkingInsertIdx;
        // Find the refNode: the message element at thinkingInsertIdx
        let refCount = 0;
        let refNode: HTMLElement | null = null;
        for (let i = 0; i < page.threadStreamEl.children.length; i++) {
          const child = page.threadStreamEl.children[i] as HTMLElement;
          if (child.classList.contains('acp-thinking')) continue;
          if (refCount === thinkingInsertIdx) {
            refNode = child;
            break;
          }
          refCount++;
        }

        if (existingThinking === thinkingBlock) {
          // Same element — check if it needs to move
          const currentNext = thinkingBlock.nextElementSibling as HTMLElement | null;
          if (refNode && currentNext !== refNode) {
            page.threadStreamEl.insertBefore(thinkingBlock, refNode);
          } else if (!refNode && page.threadStreamEl.lastChild !== thinkingBlock) {
            page.threadStreamEl.appendChild(thinkingBlock);
          }
          // Otherwise it's already in the right spot — do nothing
        } else {
          // New element (first render or after clearThinkingBlock)
          if (existingThinking) existingThinking.remove();
          if (refNode) {
            page.threadStreamEl.insertBefore(thinkingBlock, refNode);
          } else {
            page.threadStreamEl.appendChild(thinkingBlock);
          }
        }
      } else if (existingThinking) {
        existingThinking.remove();
      }
    }
    // Smart auto-scroll: only scroll if user hasn't scrolled away
    if (!_userScrolledAway) {
      _programmaticScroll = true;
      page.threadBodyEl.scrollTo({ top: page.threadBodyEl.scrollHeight, behavior: 'smooth' });
      // Reset flag after scroll events have fired
      requestAnimationFrame(() => { _programmaticScroll = false; });
    }
    renderPermissionPrompt(page);
  }

  // Track whether user has manually scrolled away from bottom
  let _userScrolledAway = false;
  let _scrollListenerAttached = false;
  let _programmaticScroll = false;

  function attachScrollListener(el: HTMLElement) {
    if (_scrollListenerAttached) return;
    _scrollListenerAttached = true;
    el.addEventListener('scroll', () => {
      // Ignore scroll events triggered by our own scrollTo calls
      if (_programmaticScroll) return;
      // User is "at bottom" if within 80px of the end
      var atBottom = el.scrollHeight - el.scrollTop - el.clientHeight < 80;
      _userScrolledAway = !atBottom;
    }, { passive: true });
  }

  function createGridLoader() {
    const loader = document.createElement('span');
    loader.className = 'grid-loader';
    for (let i = 0; i < 9; i++) {
      loader.appendChild(document.createElement('span'));
    }
    return loader;
  }

  const TERMINAL_STATUSES = ['connected', 'completed', 'disconnected', 'no acp-ready workspaces'];

  function setStatus(page, text) {
    if (!page.statusEl) return;
    page.statusEl.innerHTML = '';
    if (!text) return;
    const isTerminal = TERMINAL_STATUSES.some((s) => text.toLowerCase().startsWith(s));
    if (!isTerminal) {
      page.statusEl.appendChild(createGridLoader());
    }
    if (typeof document.createTextNode === 'function') {
      page.statusEl.appendChild(document.createTextNode(text));
    } else {
      page.statusEl.textContent = (page.statusEl.textContent || '') + text;
    }
  }

  function selectedConversationClientMatches(page) {
    return Boolean(
      page.client &&
        page.selectedConversation &&
        typeof page.client.matchesConversation === 'function' &&
        page.client.matchesConversation(page.selectedConversation),
    );
  }

  async function ensureSelectedConversationClient(page) {
    if (!page.selectedConversation) return false;
    if (selectedConversationClientMatches(page)) {
      return true;
    }
    if (!page.selectedConversationId) {
      return false;
    }
    setStatus(page, 'Reconnecting conversation…');
    await selectConversation(page, page.selectedConversationId);
    return selectedConversationClientMatches(page);
  }

  const SEND_ICON = '<svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><line x1="22" y1="2" x2="11" y2="13"/><polygon points="22 2 15 22 11 13 2 9 22 2"/></svg>';
  const STOP_ICON = '<svg width="16" height="16" viewBox="0 0 24 24" fill="currentColor" stroke="none"><rect x="6" y="6" width="12" height="12" rx="2"/></svg>';

  function syncComposer(page) {
    const disabled =
      !page.client ||
      !page.client.isReady() ||
      !page.selectedConversation ||
      !selectedConversationClientMatches(page);
    if (page.composerEl) page.composerEl.disabled = disabled || page.promptInFlight;
    if (page.sendBtn) {
      if (page.promptInFlight) {
        page.sendBtn.innerHTML = STOP_ICON;
        page.sendBtn.dataset.tooltip = 'Stop';
        page.sendBtn.disabled = false;
      } else {
        page.sendBtn.innerHTML = SEND_ICON;
        page.sendBtn.dataset.tooltip = 'Send';
        page.sendBtn.disabled = disabled;
      }
    }
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

  function applyBootstrappedConversation(page, response) {
    const updated = response?.conversation;
    if (!updated?.metadata?.name) {
      return null;
    }
    const conversationId = updated.metadata.name;
    const previousSessionId = page.selectedConversation?.spec?.sessionId || '';
    const nextSessionId = updated.spec?.sessionId || '';
    const replaced = Boolean(response?.replaced || (previousSessionId && nextSessionId && previousSessionId !== nextSessionId));

    page.selectedConversation = updated;
    page.selectedConversationId = conversationId;
    page.conversations = page.conversations.map((item) =>
      item.metadata?.name === conversationId ? updated : item,
    );

    if (replaced) {
      clearCachedConversationRecord(conversationId);
      page.previewByConversationId.delete(conversationId);
      page.transcript = ACPRender.createTranscript();
      page.cacheHydratedTranscript = false;
      page.cacheReplacedByReplay = false;
    }

    if (response?.agentInfo && page.selectedAgent?.spritz) {
      page.selectedAgent = {
        ...page.selectedAgent,
        spritz: {
          ...page.selectedAgent.spritz,
          status: {
            ...(page.selectedAgent.spritz.status || {}),
            acp: {
              ...(page.selectedAgent.spritz.status?.acp || {}),
              agentInfo: response.agentInfo,
            },
          },
        },
      };
    }

    renderConversationList(page);
    renderThread(page);
    return updated;
  }

  async function bootstrapSelectedConversation(page) {
    const conversationId = page.selectedConversation?.metadata?.name || '';
    if (!conversationId) {
      throw new Error('Conversation not selected.');
    }
    setStatus(page, 'Preparing conversation…');
    const response = await bootstrapACPConversationData(page.deps, conversationId);
    applyBootstrappedConversation(page, response);
    return response;
  }

  function conversationNeedsBootstrap(
    conversation,
    options: ACPConversationBootstrapOptions = {},
  ) {
    if (options.forceBootstrap) return true;
    const sessionId = String(conversation?.spec?.sessionId || '').trim();
    if (!sessionId) return true;
    return String(conversation?.status?.bindingState || '').trim().toLowerCase() !== 'active';
  }

  let _pendingRenderFrame: number | null = null;

  function scheduleRender(page) {
    if (_pendingRenderFrame) return;
    _pendingRenderFrame = requestAnimationFrame(() => {
      _pendingRenderFrame = null;
      renderThread(page);
    });
  }

  function applyACPUpdate(page, update) {
    const result = ACPRender.applySessionUpdate(page.transcript, update, {
      historical: !page.bootstrapComplete,
    });
    if (result?.toast?.message) {
      showACPToast(page, result.toast.message, result.toast.type || 'error');
    }
    if (result?.conversationTitle) {
      patchSelectedConversation(page, { title: result.conversationTitle }).catch(() => {});
    }
    updateConversationPreview(page);
    writeCachedConversationRecord(page);
    renderConversationList(page);
    scheduleRender(page);
  }

  function resetConversationRuntime(page) {
    page.transcript = ACPRender.createTranscript();
    ACPRender.clearThinkingBlock();
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

  async function connectSelectedConversation(
    page,
    options: ACPConversationBootstrapOptions = {},
  ) {
    if (!page.selectedAgent || !page.selectedConversation) {
      resetConversationRuntime(page);
      renderThread(page);
      return;
    }
    let bootstrap = null;
    if (conversationNeedsBootstrap(page.selectedConversation, options)) {
      bootstrap = await bootstrapSelectedConversation(page);
    }
    page.bootstrapComplete = false;
    page.cacheHydratedTranscript = page.transcript.messages.length > 0;
    page.cacheReplacedByReplay = false;
    renderThread(page);
    page.client = createACPClient({
      wsUrl: acpWsUrl(page.selectedConversation.metadata?.name || '', page.deps),
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
          // Preserve thinking_done messages from the cached transcript so
          // they survive reconnection even if replay doesn't include tool_call events.
          const savedThinkingDone = page.transcript.messages.filter(
            (m) => m?.type === 'thinking_done',
          );
          page.transcript = ACPRender.createTranscript();
          page._savedThinkingDone = savedThinkingDone;
          page.cacheReplacedByReplay = true;
        }
        applyACPUpdate(page, update);
      },
      onPermissionRequest(entry) {
        page.permissionQueue.push(entry);
        renderPermissionPrompt(page);
      },
      onPromptStateChange(value) {
        page.promptInFlight = value;
        if (!value) {
          ACPRender.finalizeStreaming(page.transcript);
          ACPRender.clearThinkingBlock();
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
    try {
      await page.client.start();
    } catch (err) {
      if (err?.code === 'ACP_SESSION_MISSING' && options.allowRepairRetry !== false) {
        resetConversationRuntime(page);
        await connectSelectedConversation(page, { ...options, allowRepairRetry: false, forceBootstrap: true });
        return;
      }
      throw err;
    }
    if (!selectedConversationClientMatches(page)) {
      if (options.allowAutoRebind === false) {
        throw new Error('ACP client bound to the wrong conversation.');
      }
      resetConversationRuntime(page);
      await connectSelectedConversation(page, { allowAutoRebind: false });
      return;
    }
    if (bootstrap?.replaced) {
      page.transcript = ACPRender.createTranscript();
      renderConversationList(page);
      renderThread(page);
    }
    if (page.cacheHydratedTranscript && !page.cacheReplacedByReplay) {
      page.transcript = ACPRender.createTranscript();
      renderConversationList(page);
      renderThread(page);
    }
    page.bootstrapComplete = true;
    // Bake any remaining thinking chunks from the last replayed turn
    ACPRender.finalizeHistoricalThinking(page.transcript);
    // Restore thinking_done messages that were preserved from cache but
    // not reconstructed during replay (e.g. if tool_call events were not replayed).
    if (Array.isArray(page._savedThinkingDone) && page._savedThinkingDone.length > 0) {
      const replayHasThinkingDone = page.transcript.messages.some(
        (m) => m?.type === 'thinking_done',
      );
      if (!replayHasThinkingDone) {
        // Re-inject saved thinking_done messages at appropriate positions.
        // Find each user message boundary and insert before the assistant response.
        const saved = page._savedThinkingDone;
        let savedIdx = 0;
        let userCount = 0;
        for (let i = 0; i < page.transcript.messages.length && savedIdx < saved.length; i++) {
          const msg = page.transcript.messages[i];
          if (msg?.type === 'user') userCount++;
          // Insert thinking_done after user message, before assistant response
          if (msg?.type === 'assistant' && userCount > savedIdx) {
            page.transcript.messages.splice(i, 0, saved[savedIdx]);
            savedIdx++;
            i++; // skip past the inserted message
          }
        }
        // Append any remaining at the end
        while (savedIdx < saved.length) {
          page.transcript.messages.push(saved[savedIdx]);
          savedIdx++;
        }
      }
      delete page._savedThinkingDone;
    }
    renderThread(page);
    writeCachedConversationRecord(page);
    clearACPNotice(page);
    if (page.composerEl?.focus) page.composerEl.focus();
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
    }
  }

  async function refreshConversations(page) {
    if (page.workspaceState !== 'ready') {
      page.conversations = [];
      page.selectedConversation = null;
      page.selectedConversationId = '';
      renderConversationList(page);
      renderThread(page);
      return;
    }
    if (!page.selectedName) {
      page.conversations = [];
      page.selectedConversation = null;
      page.selectedConversationId = '';
      renderConversationList(page);
      renderThread(page);
      return;
    }
    page.conversations = await listACPConversationsData(page.deps, page.selectedName);
    page.agentConversations[page.selectedName] = page.conversations;
    hydrateCachedConversationPreviews(page);
    const routeConversationId = conversationIdFromHash(window.location.hash || '');
    let resolvedConversationId =
      page.conversations.find((item) => item.metadata?.name === routeConversationId)?.metadata?.name ||
      page.selectedConversationId ||
      page.conversations[0]?.metadata?.name ||
      '';
    renderConversationList(page);
    if (routeConversationId && resolvedConversationId !== routeConversationId) {
      window.location.replace(chatPagePath(page.selectedName, resolvedConversationId));
      return;
    }
    if (resolvedConversationId && resolvedConversationId !== routeConversationId) {
      window.location.replace(chatPagePath(page.selectedName, resolvedConversationId));
      return;
    }
    await selectConversation(page, resolvedConversationId);
  }

  async function loadACPPage(page) {
    try {
      clearWorkspaceRefresh(page);
      clearACPNotice(page);
      setStatus(page, 'Loading workspaces…');
      page.agents = await fetchACPAgentsData(page.deps);
      page.selectedSpritz = null;
      page.workspaceState = 'empty';
      const selectedReadyAgent = page.selectedName
        ? page.agents.find((item) => item?.spritz?.metadata?.name === page.selectedName) || null
        : null;
      if (page.selectedName && !selectedReadyAgent) {
        try {
          page.selectedSpritz = await fetchSpritzData(page.deps, page.selectedName);
        } catch (err) {
          if (err?.status !== 404) {
            throw err;
          }
        }
      }
      // Fetch conversations for all agents in parallel
      const convoResults = await Promise.all(
        page.agents.map(async (agent) => {
          const name = agent?.spritz?.metadata?.name || '';
          if (!name) return { name, convos: [] };
          try {
            const convos = await listACPConversationsData(page.deps, name);
            return { name, convos };
          } catch {
            return { name, convos: [] };
          }
        }),
      );
      page.agentConversations = {};
      for (const { name, convos } of convoResults) {
        if (name) page.agentConversations[name] = convos;
      }

      renderAgentPicker(page);
      if (!page.agents.length && !page.selectedSpritz) {
        page.selectedAgent = null;
        page.conversations = [];
        page.selectedConversation = null;
        page.selectedConversationId = '';
        page.workspaceState = 'empty';
        resetConversationRuntime(page);
        renderConversationList(page);
        renderThread(page);
        setStatus(page, 'No ACP-ready workspaces.');
        return;
      }

      if (!page.selectedName) {
        const resolvedName = page.agents[0]?.spritz?.metadata?.name || '';
        window.location.replace(chatPagePath(resolvedName));
        return;
      }
      if (!selectedReadyAgent && !page.selectedSpritz) {
        page.selectedAgent = null;
        page.conversations = [];
        page.selectedConversation = null;
        page.selectedConversationId = '';
        page.workspaceState = 'missing';
        resetConversationRuntime(page);
        renderAgentPicker(page);
        renderConversationList(page);
        renderThread(page);
        setStatus(page, 'Workspace not found.');
        return;
      }

      page.selectedAgent = selectedReadyAgent || (isACPReadyWorkspace(page.selectedSpritz) ? { spritz: page.selectedSpritz } : null);
      page.workspaceState = page.selectedAgent ? 'ready' : 'pending';
      if (page.workspaceState !== 'ready') {
        page.conversations = [];
        page.selectedConversation = null;
        page.selectedConversationId = '';
        resetConversationRuntime(page);
        renderAgentPicker(page);
        renderConversationList(page);
        renderThread(page);
        setStatus(page, workspaceStatusSummary(page.selectedSpritz).status);
        scheduleWorkspaceRefresh(page);
        return;
      }
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

    const page: ACPPage = createACPPageState(name, conversationId, deps);

    const shell = document.createElement('section');
    shell.className = 'acp-shell';

    const sidebar = document.createElement('aside');
    sidebar.className = 'acp-sidebar';
    const sidebarTop = document.createElement('div');
    sidebarTop.className = 'acp-sidebar-top';

    const newConversationButton = document.createElement('button');
    newConversationButton.type = 'button';
    newConversationButton.className = 'acp-new-chat-item';
    newConversationButton.innerHTML = '<svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M12 20h9"/><path d="M16.5 3.5a2.121 2.121 0 0 1 3 3L7 19l-4 1 1-4L16.5 3.5z"/></svg><span>New chat</span>';

    const backLink = document.createElement('a');
    backLink.href = '#create';
    backLink.className = 'acp-new-chat-item acp-back-link';
    backLink.innerHTML = '<svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><rect x="3" y="3" width="7" height="7"/><rect x="14" y="3" width="7" height="7"/><rect x="3" y="14" width="7" height="7"/><rect x="14" y="14" width="7" height="7"/></svg><span>Spritzes</span>';

    const refreshButton = document.createElement('button');
    refreshButton.type = 'button';
    refreshButton.className = 'acp-nav-icon';
    refreshButton.dataset.tooltip = 'Refresh';
    refreshButton.dataset.tooltipPos = 'bottom-end';
    refreshButton.innerHTML = '<svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><polyline points="23 4 23 10 17 10"/><path d="M20.49 15a9 9 0 1 1-2.12-9.36L23 10"/></svg>';

    const collapseIcon = '<svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><rect x="3" y="3" width="18" height="18" rx="2"/><line x1="9" y1="3" x2="9" y2="21"/></svg>';
    const expandIcon = '<svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><rect x="3" y="3" width="18" height="18" rx="2"/><line x1="15" y1="3" x2="15" y2="21"/></svg>';

    const toggleBtn = document.createElement('button');
    toggleBtn.type = 'button';
    toggleBtn.className = 'acp-nav-icon acp-sidebar-toggle';
    toggleBtn.innerHTML = collapseIcon;

    const isCollapsed = typeof localStorage !== 'undefined' && localStorage.getItem('spritz:sidebar-collapsed') === 'true';
    if (isCollapsed) {
      shell.dataset.collapsed = 'true';
      toggleBtn.innerHTML = expandIcon;
    }

    toggleBtn.addEventListener('click', () => {
      const collapsed = shell.dataset.collapsed === 'true';
      if (collapsed) {
        delete shell.dataset.collapsed;
        toggleBtn.innerHTML = collapseIcon;
        try { localStorage.setItem('spritz:sidebar-collapsed', 'false'); } catch {}
      } else {
        shell.dataset.collapsed = 'true';
        toggleBtn.innerHTML = expandIcon;
          try { localStorage.setItem('spritz:sidebar-collapsed', 'true'); } catch {}
      }
    });

    const selectRow = document.createElement('div');
    selectRow.className = 'acp-sidebar-select-row';
    selectRow.append(toggleBtn);

    const threadList = document.createElement('div');
    threadList.className = 'acp-thread-list';

    const sidebarActions = document.createElement('div');
    sidebarActions.className = 'acp-sidebar-actions';
    sidebarActions.append(newConversationButton, backLink);

    sidebarTop.append(selectRow, sidebarActions);
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
    openButton.className = 'acp-nav-icon';
    openButton.dataset.tooltip = 'Open workspace';
    openButton.dataset.tooltipPos = 'bottom-end';
    openButton.innerHTML = '<svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M18 13v6a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V8a2 2 0 0 1 2-2h6"/><polyline points="15 3 21 3 21 9"/><line x1="10" y1="14" x2="21" y2="3"/></svg>';
    headerActions.append(refreshButton, openButton);

    const mobileMenuBtn = document.createElement('button');
    mobileMenuBtn.type = 'button';
    mobileMenuBtn.className = 'acp-nav-icon acp-mobile-menu';
    mobileMenuBtn.innerHTML = '<svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"><line x1="3" y1="6" x2="21" y2="6"/><line x1="3" y1="12" x2="21" y2="12"/><line x1="3" y1="18" x2="21" y2="18"/></svg>';

    const backdrop = document.createElement('div');
    backdrop.className = 'acp-sidebar-backdrop';

    mobileMenuBtn.addEventListener('click', () => {
      shell.dataset.mobileOpen = 'true';
    });
    backdrop.addEventListener('click', () => {
      delete shell.dataset.mobileOpen;
    });

    shell.appendChild(backdrop);

    threadList.addEventListener('click', () => {
      delete shell.dataset.mobileOpen;
    });

    const headerTop = document.createElement('div');
    headerTop.className = 'acp-main-header-top';
    headerTop.append(mobileMenuBtn, headerCopy, headerActions);

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
    composerInput.rows = 1;
    const sendButton = document.createElement('button');
    sendButton.type = 'button';
    sendButton.className = 'acp-composer-send';
    sendButton.dataset.tooltip = 'Send';
    sendButton.innerHTML = SEND_ICON;
    const cancelButton = sendButton;
    const composerActions = document.createElement('div');
    composerActions.className = 'acp-composer-actions';
    composerActions.append(sendButton);
    composer.append(composerInput, composerActions);

    footerInner.append(permissionBox, composer, statusRow);
    footer.appendChild(footerInner);

    main.append(header, body, footer);
    shell.append(sidebar, main);
    deps.shellEl.append(shell);

    page.card = shell;
    page.agentSelectEl = null;
    page.threadListEl = threadList;
    page.threadTitleEl = threadTitle;
    page.threadMetaEl = threadMeta;
    page.commandBarEl = commandBar;
    page.threadBodyEl = body;
    attachScrollListener(body);
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
      if (page.promptInFlight) {
        page.client?.cancelPrompt();
        return;
      }
      const text = composerInput.value.trim();
      if (!text || !page.client || !page.selectedConversation) return;
      const rebound = await ensureSelectedConversationClient(page);
      if (!rebound || !page.client) {
        reportACPError(page, new Error('Conversation is reconnecting. Try again.'), 'Conversation is reconnecting. Try again.', 'info');
        syncComposer(page);
        renderThread(page);
        return;
      }
      composerInput.value = '';
      if (composerInput.focus) composerInput.focus();
      _userScrolledAway = false; // resume auto-scroll on new message
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
        setStatus(page, 'Completed');
      } catch (err) {
        reportACPError(page, err, 'Failed to send ACP prompt.');
      } finally {
        syncComposer(page);
        renderThread(page);
      }
    });

    function autoResizeComposer() {
      composerInput.style.height = 'auto';
      composerInput.style.height = Math.min(composerInput.scrollHeight, 180) + 'px';
    }
    composerInput.addEventListener('input', autoResizeComposer);

    composerInput.addEventListener('keydown', (event) => {
      if (event.key === 'Enter' && !event.shiftKey) {
        event.preventDefault();
        sendButton.click();
      }
    });

    page.destroy = function destroy() {
      page.destroyed = true;
      clearWorkspaceRefresh(page);
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
