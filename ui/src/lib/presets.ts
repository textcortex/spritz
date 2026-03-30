import { useEffect, useMemo, useState } from 'react';
import { request } from './api';
import { useConfig, type Preset } from './config';

const PRESETS_PLACEHOLDER = '__SPRITZ_UI_PRESETS__';

interface PresetCatalogResult {
  presets: Preset[];
  loaded: boolean;
}

export function parsePresets(raw: Preset[] | string | undefined | null): Preset[] {
  if (Array.isArray(raw)) return raw;
  if (typeof raw === 'string') {
    const trimmed = raw.trim();
    if (!trimmed || trimmed === PRESETS_PLACEHOLDER) return [];
    try {
      const parsed = JSON.parse(trimmed);
      return Array.isArray(parsed) ? parsed : [];
    } catch {
      console.error('Failed to parse Spritz preset configuration.');
      return [];
    }
  }
  return [];
}

export function usePresetCatalog(): PresetCatalogResult {
  const config = useConfig();
  const fallbackPresets = useMemo(() => parsePresets(config.presets), [config.presets]);
  const [catalog, setCatalog] = useState<PresetCatalogResult>({ presets: [], loaded: false });

  useEffect(() => {
    let cancelled = false;

    void (async () => {
      try {
        const data = await request<{ items?: Preset[] }>('/presets');
        if (cancelled) return;
        setCatalog({
          presets: Array.isArray(data?.items) ? data.items : [],
          loaded: true,
        });
      } catch {
        if (cancelled) return;
        setCatalog({
          presets: fallbackPresets,
          loaded: true,
        });
      }
    })();

    return () => {
      cancelled = true;
    };
  }, [fallbackPresets]);

  return catalog;
}

export function usePresets(): Preset[] {
  return usePresetCatalog().presets;
}
