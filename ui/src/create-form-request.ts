(function (root, factory) {
  const exports = factory();
  if (typeof module === 'object' && module.exports) {
    module.exports = exports;
  }
  root.SpritzCreateFormRequest = exports;
})(typeof globalThis !== 'undefined' ? globalThis : this, function () {
  function normalizeNamePrefix(value) {
    return String(value || '')
      .trim()
      .toLowerCase()
      .replace(/[^a-z0-9-]+/g, '-')
      .replace(/^-+|-+$/g, '')
      .replace(/--+/g, '-');
  }

  function resolveNamePrefix(activePreset, imageValue) {
    const presetPrefix = normalizeNamePrefix(activePreset?.namePrefix || '');
    if (presetPrefix) return presetPrefix;
    const image = String(imageValue || '').trim();
    if (!image) return '';
    const lastSegment = image.split('/').pop() || '';
    const withoutDigest = lastSegment.split('@')[0];
    const withoutTag = withoutDigest.split(':')[0];
    const withoutPrefix = withoutTag.replace(/^spritz-/, '');
    return normalizeNamePrefix(withoutPrefix);
  }

  function resolveRepoSelection(options) {
    const activePreset = options?.activePreset || null;
    const repoValue = String(options?.repoValue || '').trim();
    const branchValue = String(options?.branchValue || '').trim();
    const defaultRepoUrl = String(options?.defaultRepoUrl || '').trim();
    const defaultRepoBranch = String(options?.defaultRepoBranch || '').trim();
    const presetOwnsRepo =
      !!activePreset &&
      (Object.prototype.hasOwnProperty.call(activePreset, 'repoUrl') ||
        Object.prototype.hasOwnProperty.call(activePreset, 'branch'));

    if (presetOwnsRepo) {
      return {
        repoUrl: repoValue,
        repoBranch: branchValue,
      };
    }

    return {
      repoUrl: repoValue || defaultRepoUrl,
      repoBranch: branchValue || defaultRepoBranch,
    };
  }

  function buildCreatePayload(options) {
    const name = String(options?.name || '').trim();
    const imageValue = String(options?.imageValue || '').trim();
    const namespace = String(options?.namespace || '').trim();
    const ttlValue = String(options?.ttlValue || '').trim();
    const ownerId = String(options?.ownerId || '').trim();
    const activePreset = options?.activePreset || null;
    const presetMatchesImage =
      !!activePreset && imageValue !== '' && String(activePreset.image || '').trim() === imageValue;

    const payload: any = {
      namespace: namespace || undefined,
      spec: {},
    };

    if (name) {
      payload.name = name;
    } else {
      const namePrefix = resolveNamePrefix(presetMatchesImage ? activePreset : null, imageValue);
      if (namePrefix) {
        payload.namePrefix = namePrefix;
      }
    }

    if (presetMatchesImage && String(activePreset.id || '').trim()) {
      payload.presetId = String(activePreset.id).trim();
    } else if (imageValue) {
      payload.spec.image = imageValue;
    }

    if (ownerId) {
      payload.spec.owner = { id: ownerId };
    }

    const { repoUrl, repoBranch } = resolveRepoSelection({
      activePreset: presetMatchesImage ? activePreset : null,
      repoValue: options?.repoValue,
      branchValue: options?.branchValue,
      defaultRepoUrl: options?.defaultRepoUrl,
      defaultRepoBranch: options?.defaultRepoBranch,
    });

    if (repoUrl) {
      payload.spec.repo = { url: repoUrl };
      if (repoBranch) payload.spec.repo.branch = repoBranch;
      if (String(options?.defaultRepoDir || '').trim()) {
        payload.spec.repo.dir = String(options.defaultRepoDir).trim();
      }
    }

    if (ttlValue) {
      payload.spec.ttl = ttlValue;
    }

    return payload;
  }

  return { resolveRepoSelection, buildCreatePayload };
});
