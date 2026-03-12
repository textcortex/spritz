(function (root, factory) {
  const exports = factory();
  if (typeof module === 'object' && module.exports) {
    module.exports = exports;
  }
  root.SpritzPresetConfig = exports;
})(typeof globalThis !== 'undefined' ? globalThis : this, function () {
  /**
   * Parse the runtime preset configuration from config.js.
   * Missing placeholders fall back to built-in defaults; malformed values fail closed.
   */
  function parsePresets(raw, options) {
    const { placeholder = '', logger = console } = options || {};
    if (Array.isArray(raw)) return raw;
    if (typeof raw === 'string') {
      const trimmed = raw.trim();
      if (!trimmed || trimmed === placeholder) return null;
      try {
        const parsed = JSON.parse(trimmed);
        return Array.isArray(parsed) ? parsed : [];
      } catch (error) {
        if (logger && typeof logger.error === 'function') {
          logger.error('Failed to parse Spritz preset configuration.', error);
        }
        return [];
      }
    }
    return null;
  }

  return { parsePresets };
});
