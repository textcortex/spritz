(function (root, factory) {
  const exports = factory();
  if (typeof module === 'object' && module.exports) {
    module.exports = exports;
  }
  root.SpritzPresetPanel = exports;
})(typeof globalThis !== 'undefined' ? globalThis : this, function () {
  function setupPresetPanel(options) {
    const {
      document,
      form,
      presets,
      hideRepoInputs,
      applyRepoDefaults,
      normalizePresetEnv,
      setActivePresetEnv,
      setActivePreset,
    } = options || {};

    if (!document || !form || !Array.isArray(presets) || presets.length === 0) {
      return null;
    }

    const existingSelect = document.getElementById('preset-select');
    if (existingSelect) {
      return {
        reset() {
          existingSelect.value = '';
          const help = form.querySelector('.preset-help');
          if (help) help.textContent = '';
          setActivePresetEnv(null);
          if (typeof setActivePreset === 'function') setActivePreset(null);
        },
      };
    }

    const imageInput = form.querySelector('input[name="image"]');
    const repoInput = form.querySelector('input[name="repo"]');
    const branchInput = form.querySelector('input[name="branch"]');
    const ttlInput = form.querySelector('input[name="ttl"]');
    if (!imageInput) {
      return null;
    }

    if (typeof applyRepoDefaults === 'function') {
      applyRepoDefaults();
    }

    const panel = document.createElement('div');
    panel.className = 'preset-panel';

    const label = document.createElement('label');
    label.textContent = 'Preset';

    const select = document.createElement('select');
    select.id = 'preset-select';

    const customOption = document.createElement('option');
    customOption.value = '';
    customOption.textContent = 'Custom';
    select.append(customOption);

    presets.forEach((preset, index) => {
      const option = document.createElement('option');
      option.value = String(index);
      option.textContent = `${preset.name} (${preset.image})`;
      select.append(option);
    });

    const help = document.createElement('small');
    help.className = 'preset-help';

    label.append(select);
    panel.append(label, help);
    form.prepend(panel);

    const applyPreset = (preset) => {
      if (!preset) return;
      imageInput.value = preset.image || '';
      if (!hideRepoInputs) {
        if (repoInput && preset.repoUrl !== undefined) repoInput.value = preset.repoUrl || '';
        if (branchInput && preset.branch !== undefined) branchInput.value = preset.branch || '';
      }
      if (ttlInput && preset.ttl !== undefined) ttlInput.value = preset.ttl || '';
      help.textContent = preset.description || '';
      setActivePresetEnv(typeof normalizePresetEnv === 'function' ? normalizePresetEnv(preset.env) : null);
      if (typeof setActivePreset === 'function') setActivePreset(preset);
    };

    const reset = () => {
      select.value = '';
      help.textContent = '';
      setActivePresetEnv(null);
      if (typeof setActivePreset === 'function') setActivePreset(null);
    };

    select.addEventListener('change', () => {
      if (!select.value) {
        reset();
        return;
      }
      applyPreset(presets[Number(select.value)]);
    });

    if (!imageInput.value && presets[0]) {
      select.value = '0';
      applyPreset(presets[0]);
    }

    return { reset };
  }

  return { setupPresetPanel };
});
