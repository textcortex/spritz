import { describe, it, expect } from 'vite-plus/test';
import { buildCreatePayload, parseUserConfigInput, type BuildCreatePayloadOptions } from './create-payload';
import type { Preset } from './config';

const BASE_PRESET: Preset = {
  name: 'Starter',
  image: 'spritz-starter:latest',
  description: '',
  repoUrl: '',
  branch: '',
  ttl: '',
};

function baseOptions(overrides: Partial<BuildCreatePayloadOptions> = {}): BuildCreatePayloadOptions {
  return {
    name: '',
    imageValue: 'spritz-starter:latest',
    namespace: 'default',
    ownerId: 'user-1',
    activePreset: BASE_PRESET,
    repoValue: '',
    branchValue: '',
    defaultRepoUrl: 'https://github.com/example/repo',
    defaultRepoBranch: 'main',
    defaultRepoDir: '',
    ttlValue: '',
    ...overrides,
  };
}

describe('buildCreatePayload', () => {
  it('uses presetId when preset has id and image matches', () => {
    const preset = { ...BASE_PRESET, id: 'preset-1' } as Preset & { id: string };
    const result = buildCreatePayload(baseOptions({ activePreset: preset }));
    expect(result.presetId).toBe('preset-1');
    expect((result.spec as Record<string, unknown>).image).toBeUndefined();
  });

  it('falls back to image in spec when no preset match', () => {
    const result = buildCreatePayload(baseOptions({ activePreset: null }));
    expect(result.presetId).toBeUndefined();
    expect((result.spec as Record<string, unknown>).image).toBe('spritz-starter:latest');
  });

  it('resolves namePrefix from image tag when no name given', () => {
    const result = buildCreatePayload(baseOptions({ name: '', activePreset: null }));
    expect(result.namePrefix).toBe('starter');
  });

  it('uses explicit name when provided', () => {
    const result = buildCreatePayload(baseOptions({ name: 'my-spritz' }));
    expect(result.name).toBe('my-spritz');
    expect(result.namePrefix).toBeUndefined();
  });

  it('resolves repo selection with fallback to defaults when preset does not own repo', () => {
    const preset: Preset = { name: 'NoRepo', image: 'spritz-starter:latest', description: '' } as Preset;
    const result = buildCreatePayload(baseOptions({
      activePreset: preset,
      repoValue: '',
      branchValue: '',
      defaultRepoUrl: 'https://github.com/default/repo',
      defaultRepoBranch: 'main',
    }));
    const repo = (result.spec as Record<string, unknown>).repo as Record<string, string>;
    expect(repo.url).toBe('https://github.com/default/repo');
    expect(repo.branch).toBe('main');
  });

  it('preserves explicit blank repo when preset owns repo keys', () => {
    const result = buildCreatePayload(baseOptions({
      repoValue: '',
      branchValue: '',
      defaultRepoUrl: 'https://github.com/default/repo',
      defaultRepoBranch: 'main',
    }));
    // BASE_PRESET has repoUrl key → preset owns repo → blank values are preserved
    const repo = (result.spec as Record<string, unknown>).repo;
    expect(repo).toBeUndefined();
  });

  it('sets ttl when provided', () => {
    const result = buildCreatePayload(baseOptions({ ttlValue: '1h' }));
    expect((result.spec as Record<string, unknown>).ttl).toBe('1h');
  });

  it('sets owner when ownerId provided', () => {
    const result = buildCreatePayload(baseOptions({ ownerId: 'user-42' }));
    expect((result.spec as Record<string, unknown>).owner).toEqual({ id: 'user-42' });
  });
});

describe('parseUserConfigInput', () => {
  it('returns null for empty input', () => {
    expect(parseUserConfigInput('')).toBeNull();
  });

  it('returns null for whitespace-only input', () => {
    expect(parseUserConfigInput('   ')).toBeNull();
  });

  it('parses JSON object', () => {
    const result = parseUserConfigInput('{"foo": "bar"}');
    expect(result).toEqual({ foo: 'bar' });
  });

  it('parses JSON array as sharedMounts', () => {
    const result = parseUserConfigInput('[{"path": "/data"}]');
    expect(result).toEqual({ sharedMounts: [{ path: '/data' }] });
  });

  it('parses YAML mapping', () => {
    const result = parseUserConfigInput('key: value\nanother: 42');
    expect(result).toEqual({ key: 'value', another: 42 });
  });

  it('parses YAML list as sharedMounts', () => {
    const result = parseUserConfigInput('- path: /data\n  readOnly: true');
    expect(result).toEqual({ sharedMounts: [{ path: '/data', readOnly: true }] });
  });

  it('throws on malformed JSON', () => {
    expect(() => parseUserConfigInput('{bad json')).toThrow();
  });

  it('parses YAML booleans and nulls', () => {
    const result = parseUserConfigInput('enabled: true\ndisabled: false\nempty: null');
    expect(result).toEqual({ enabled: true, disabled: false, empty: null });
  });
});
