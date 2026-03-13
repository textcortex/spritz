(function (root, factory) {
  const exports = factory();
  if (typeof module === 'object' && module.exports) {
    module.exports = exports;
  }
  root.SpritzCreateFormState = exports;
})(typeof globalThis !== 'undefined' ? globalThis : this, function () {
  const CREATE_FORM_STORAGE_KEY = 'spritz:create-form';

  function normalizeString(value) {
    if (value === undefined || value === null) return '';
    return String(value);
  }

  function trimString(value) {
    return normalizeString(value).trim();
  }

  function imagesMatch(left, right) {
    return trimString(left) === trimString(right);
  }

  function deriveCreateFormSelection(activePreset, imageValue) {
    if (!activePreset || !imagesMatch(imageValue, activePreset.image)) {
      return { mode: 'custom' };
    }
    return {
      mode: 'preset',
      presetName: trimString(activePreset.name),
      presetImage: trimString(activePreset.image),
    };
  }

  function hasMeaningfulState(state) {
    if (!state || typeof state !== 'object') return false;
    if (state.selection?.mode === 'preset') return true;
    const fields = state.fields;
    if (!fields || typeof fields !== 'object') return false;
    return ['image', 'repo', 'branch', 'ttl', 'namespace', 'userConfig']
      .some((key) => trimString(fields[key]) !== '');
  }

  function sanitizeSelection(raw) {
    if (!raw || typeof raw !== 'object') {
      return { mode: 'custom' };
    }
    if (raw.mode !== 'preset') {
      return { mode: 'custom' };
    }
    return {
      mode: 'preset',
      presetName: trimString(raw.presetName),
      presetImage: trimString(raw.presetImage),
    };
  }

  function sanitizeFields(raw) {
    const input = raw && typeof raw === 'object' ? raw : {};
    return {
      image: normalizeString(input.image),
      repo: normalizeString(input.repo),
      branch: normalizeString(input.branch),
      ttl: normalizeString(input.ttl),
      namespace: normalizeString(input.namespace),
      userConfig: normalizeString(input.userConfig),
    };
  }

  function sanitizeCreateFormState(raw) {
    if (!raw || typeof raw !== 'object') return null;
    const state = {
      selection: sanitizeSelection(raw.selection),
      fields: sanitizeFields(raw.fields),
    };
    return hasMeaningfulState(state) ? state : null;
  }

  function buildCreateFormState(values) {
    const input = values && typeof values === 'object' ? values : {};
    return sanitizeCreateFormState({
      selection: deriveCreateFormSelection(input.activePreset, input.image),
      fields: {
        image: input.image,
        repo: input.repo,
        branch: input.branch,
        ttl: input.ttl,
        namespace: input.namespace,
        userConfig: input.userConfig,
      },
    });
  }

  function readCreateFormState(storage) {
    if (!storage || typeof storage.getItem !== 'function') return null;
    const raw = storage.getItem(CREATE_FORM_STORAGE_KEY);
    if (!raw) return null;
    try {
      return sanitizeCreateFormState(JSON.parse(raw));
    } catch {
      if (typeof storage.removeItem === 'function') {
        storage.removeItem(CREATE_FORM_STORAGE_KEY);
      }
      return null;
    }
  }

  function writeCreateFormState(storage, state) {
    if (!storage || typeof storage.setItem !== 'function') return null;
    const sanitized = sanitizeCreateFormState(state);
    if (!sanitized) {
      if (typeof storage.removeItem === 'function') {
        storage.removeItem(CREATE_FORM_STORAGE_KEY);
      }
      return null;
    }
    storage.setItem(CREATE_FORM_STORAGE_KEY, JSON.stringify(sanitized));
    return sanitized;
  }

  function clearCreateFormState(storage) {
    if (!storage || typeof storage.removeItem !== 'function') return;
    storage.removeItem(CREATE_FORM_STORAGE_KEY);
  }

  return {
    CREATE_FORM_STORAGE_KEY,
    buildCreateFormState,
    clearCreateFormState,
    deriveCreateFormSelection,
    readCreateFormState,
    writeCreateFormState,
  };
});
