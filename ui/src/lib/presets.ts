import { useMemo } from 'react';
import { useConfig, type Preset } from './config';

const PRESETS_PLACEHOLDER = '__SPRITZ_UI_PRESETS__';

const DEFAULT_PRESETS: Preset[] = [
  {
    name: 'Starter (minimal)',
    image: 'spritz-starter:latest',
    description: 'Minimal starter image built from images/examples/base.',
    repoUrl: '',
    branch: '',
    ttl: '',
  },
  {
    name: 'Devbox (agents)',
    image: 'spritz-devbox:latest',
    description: 'Devbox image with coding agents preinstalled.',
    repoUrl: '',
    branch: '',
    ttl: '',
  },
  {
    name: 'OpenClaw',
    image: 'spritz-openclaw:latest',
    description: 'OpenClaw example image.',
    repoUrl: '',
    branch: '',
    ttl: '',
  },
  {
    name: 'Claude Code',
    image: 'spritz-claude-code:latest',
    description: 'Claude Code example image.',
    repoUrl: '',
    branch: '',
    ttl: '',
  },
  {
    name: 'Codex',
    image: 'spritz-codex:latest',
    description: 'Codex example image.',
    repoUrl: '',
    branch: '',
    ttl: '',
  },
];

export function parsePresets(raw: Preset[] | string | undefined | null): Preset[] {
  if (Array.isArray(raw)) return raw;
  if (typeof raw === 'string') {
    const trimmed = raw.trim();
    if (!trimmed || trimmed === PRESETS_PLACEHOLDER) return DEFAULT_PRESETS;
    try {
      const parsed = JSON.parse(trimmed);
      return Array.isArray(parsed) ? parsed : [];
    } catch {
      console.error('Failed to parse Spritz preset configuration.');
      return [];
    }
  }
  return DEFAULT_PRESETS;
}

export function usePresets(): Preset[] {
  const config = useConfig();
  return useMemo(() => parsePresets(config.presets), [config.presets]);
}
