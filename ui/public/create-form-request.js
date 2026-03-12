(function (root, factory) {
  const exports = factory();
  if (typeof module === 'object' && module.exports) {
    module.exports = exports;
  }
  root.SpritzCreateFormRequest = exports;
})(typeof globalThis !== 'undefined' ? globalThis : this, function () {
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

  return { resolveRepoSelection };
});
