import { useEffect, useMemo, useState } from 'react';
import { request } from './api';
import { useConfig, type Preset } from './config';

const PRESETS_PLACEHOLDER = '__SPRITZ_UI_PRESETS__';

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

export function usePresets(): Preset[] {
  const config = useConfig();
  const fallbackPresets = useMemo(() => parsePresets(config.presets), [config.presets]);
  const [presets, setPresets] = useState<Preset[]>([]);

  useEffect(() => {
    let cancelled = false;

    void (async () => {
      try {
        const data = await request<{ items?: Preset[] }>('/presets');
        if (cancelled) return;
        setPresets(Array.isArray(data?.items) ? data.items : []);
      } catch {
        if (cancelled) return;
        setPresets(fallbackPresets);
      }
    })();

    return () => {
      cancelled = true;
    };
  }, [fallbackPresets]);

  return presets;
}
