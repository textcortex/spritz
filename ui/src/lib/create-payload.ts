import type { Preset } from './config';

function normalizeNamePrefix(value: string): string {
  return String(value || '')
    .trim()
    .toLowerCase()
    .replace(/[^a-z0-9-]+/g, '-')
    .replace(/^-+|-+$/g, '')
    .replace(/--+/g, '-');
}

function resolveNamePrefix(activePreset: Preset | null, imageValue: string): string {
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

function resolveRepoSelection(options: {
  activePreset: Preset | null;
  repoValue: string;
  branchValue: string;
  defaultRepoUrl: string;
  defaultRepoBranch: string;
}): { repoUrl: string; repoBranch: string } {
  const { activePreset, repoValue, branchValue, defaultRepoUrl, defaultRepoBranch } = options;
  const presetOwnsRepo =
    !!activePreset &&
    (Object.prototype.hasOwnProperty.call(activePreset, 'repoUrl') ||
      Object.prototype.hasOwnProperty.call(activePreset, 'branch'));

  if (presetOwnsRepo) {
    return { repoUrl: repoValue, repoBranch: branchValue };
  }
  return {
    repoUrl: repoValue || defaultRepoUrl,
    repoBranch: branchValue || defaultRepoBranch,
  };
}

// --- Simple YAML parser (ported from legacy app.ts) ---

function parseYamlScalar(value: string): unknown {
  const trimmed = String(value).trim();
  if (!trimmed) return '';
  if (
    (trimmed.startsWith('"') && trimmed.endsWith('"')) ||
    (trimmed.startsWith("'") && trimmed.endsWith("'"))
  ) {
    return trimmed.slice(1, -1);
  }
  const lowered = trimmed.toLowerCase();
  if (lowered === 'true') return true;
  if (lowered === 'false') return false;
  if (lowered === 'null') return null;
  const numeric = Number(trimmed);
  if (!Number.isNaN(numeric) && trimmed !== '') return numeric;
  return trimmed;
}

function parseYamlKeyValue(line: string) {
  const idx = line.indexOf(':');
  if (idx === -1) return null;
  const key = line.slice(0, idx).trim();
  if (!key) return null;
  const value = line.slice(idx + 1).trim();
  return { key, value: parseYamlScalar(value) };
}

interface YamlLine {
  text: string;
  indent: number;
}

function prepareYamlLines(raw: string): YamlLine[] {
  return raw
    .split(/\r?\n/)
    .map((line) => {
      const hashIndex = line.indexOf('#');
      const sanitized = hashIndex >= 0 ? line.slice(0, hashIndex) : line;
      const withoutTabs = sanitized.replace(/\t/g, '  ').replace(/\s+$/, '');
      if (!withoutTabs.trim()) return null;
      const match = withoutTabs.match(/^\s*/);
      return { text: withoutTabs, indent: match ? match[0].length : 0 };
    })
    .filter(Boolean) as YamlLine[];
}

function collectYamlBlock(lines: YamlLine[], startIndex: number) {
  const baseIndent = lines[startIndex].indent;
  const block: YamlLine[] = [];
  let i = startIndex + 1;
  for (; i < lines.length; i += 1) {
    if (lines[i].indent <= baseIndent) break;
    block.push(lines[i]);
  }
  return { block, nextIndex: i };
}

function parseYamlListBlock(lines: YamlLine[]): Record<string, unknown>[] {
  const items: Record<string, unknown>[] = [];
  let current: Record<string, unknown> | null = null;
  for (const line of lines) {
    const trimmed = line.text.trim();
    if (trimmed.startsWith('-')) {
      if (current) items.push(current);
      current = {};
      const rest = trimmed.replace(/^-\s*/, '').trim();
      if (rest) {
        const kv = parseYamlKeyValue(rest);
        if (!kv) throw new Error(`Invalid user config list entry: ${rest}`);
        current[kv.key] = kv.value;
      }
      continue;
    }
    if (!current) throw new Error('User config YAML list must start each item with "-".');
    const kv = parseYamlKeyValue(trimmed);
    if (!kv) throw new Error(`Invalid user config YAML line: ${trimmed}`);
    current[kv.key] = kv.value;
  }
  if (current) items.push(current);
  if (items.length === 0) throw new Error('User config YAML list must contain at least one item.');
  return items;
}

function parseYamlObjectBlock(
  lines: YamlLine[],
  baseIndent: number,
): Record<string, unknown> {
  const obj: Record<string, unknown> = {};
  for (let i = 0; i < lines.length; ) {
    const line = lines[i];
    if (line.indent <= baseIndent) {
      i += 1;
      continue;
    }
    const trimmed = line.text.trim();
    if (trimmed.startsWith('-')) throw new Error('User config YAML object entries cannot start with "-".');
    const kv = parseYamlKeyValue(trimmed);
    if (!kv) throw new Error(`Invalid user config YAML line: ${trimmed}`);
    if (kv.value !== '') {
      obj[kv.key] = kv.value;
      i += 1;
      continue;
    }
    const { block, nextIndex } = collectYamlBlock(lines, i);
    if (block.length === 0) {
      obj[kv.key] = null;
      i = nextIndex;
      continue;
    }
    const firstBlockLine = block[0].text.trim();
    if (firstBlockLine.startsWith('-')) {
      obj[kv.key] = parseYamlListBlock(block);
    } else {
      obj[kv.key] = parseYamlObjectBlock(block, line.indent);
    }
    i = nextIndex;
  }
  return obj;
}

function parseUserConfigYaml(raw: string): Record<string, unknown> | null {
  const lines = prepareYamlLines(raw);
  if (lines.length === 0) return null;
  const firstLine = lines[0].text.trim();
  if (firstLine.startsWith('-')) {
    return { sharedMounts: parseYamlListBlock(lines) };
  }
  const config: Record<string, unknown> = {};
  for (let i = 0; i < lines.length; ) {
    const line = lines[i];
    if (line.indent !== 0) throw new Error('User config YAML must start at column 1.');
    const trimmed = line.text.trim();
    const kv = parseYamlKeyValue(trimmed);
    if (!kv) throw new Error(`Invalid user config YAML line: ${trimmed}`);
    if (kv.value !== '') {
      config[kv.key] = kv.value;
      i += 1;
      continue;
    }
    const { block, nextIndex } = collectYamlBlock(lines, i);
    if (block.length === 0) {
      config[kv.key] = null;
      i = nextIndex;
      continue;
    }
    const firstBlockLine = block[0].text.trim();
    if (firstBlockLine.startsWith('-')) {
      config[kv.key] = parseYamlListBlock(block);
    } else {
      config[kv.key] = parseYamlObjectBlock(block, line.indent);
    }
    i = nextIndex;
  }
  return config;
}

function normalizeUserConfigPayload(payload: unknown): Record<string, unknown> | null {
  if (payload === null || payload === undefined) return null;
  if (Array.isArray(payload)) return { sharedMounts: payload };
  if (payload && typeof payload === 'object') return payload as Record<string, unknown>;
  throw new Error('User config must be a YAML mapping or JSON object (or a shared mounts list).');
}

export function parseUserConfigInput(value: string): Record<string, unknown> | null {
  const raw = String(value || '').trim();
  if (!raw) return null;
  if (raw.startsWith('{') || raw.startsWith('[')) {
    const parsed = JSON.parse(raw);
    return normalizeUserConfigPayload(parsed);
  }
  const parsed = parseUserConfigYaml(raw);
  return normalizeUserConfigPayload(parsed);
}

export interface BuildCreatePayloadOptions {
  name: string;
  imageValue: string;
  namespace: string;
  ownerId: string;
  activePreset: Preset | null;
  repoValue: string;
  branchValue: string;
  defaultRepoUrl: string;
  defaultRepoBranch: string;
  defaultRepoDir: string;
  ttlValue: string;
}

export function buildCreatePayload(options: BuildCreatePayloadOptions) {
  const name = String(options.name || '').trim();
  const imageValue = String(options.imageValue || '').trim();
  const namespace = String(options.namespace || '').trim();
  const ttlValue = String(options.ttlValue || '').trim();
  const ownerId = String(options.ownerId || '').trim();
  const activePreset = options.activePreset || null;
  const presetMatchesImage =
    !!activePreset && imageValue !== '' && String(activePreset.image || '').trim() === imageValue;

  const payload: Record<string, unknown> = {
    namespace: namespace || undefined,
    spec: {} as Record<string, unknown>,
  };
  const spec = payload.spec as Record<string, unknown>;

  if (name) {
    payload.name = name;
  } else {
    const namePrefix = resolveNamePrefix(presetMatchesImage ? activePreset : null, imageValue);
    if (namePrefix) payload.namePrefix = namePrefix;
  }

  if (presetMatchesImage && String((activePreset as unknown as Record<string, unknown>)?.id || '').trim()) {
    payload.presetId = String((activePreset as unknown as Record<string, unknown>).id).trim();
  } else if (imageValue) {
    spec.image = imageValue;
  }

  if (ownerId) spec.owner = { id: ownerId };

  const { repoUrl, repoBranch } = resolveRepoSelection({
    activePreset: presetMatchesImage ? activePreset : null,
    repoValue: options.repoValue,
    branchValue: options.branchValue,
    defaultRepoUrl: options.defaultRepoUrl,
    defaultRepoBranch: options.defaultRepoBranch,
  });

  if (repoUrl) {
    const repo: Record<string, string> = { url: repoUrl };
    if (repoBranch) repo.branch = repoBranch;
    if (String(options.defaultRepoDir || '').trim()) {
      repo.dir = String(options.defaultRepoDir).trim();
    }
    spec.repo = repo;
  }

  if (ttlValue) spec.ttl = ttlValue;

  return payload;
}
