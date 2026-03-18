import { describe, it, expect } from 'vite-plus/test';
import { parsePresets } from './presets';

describe('parsePresets', () => {
  it('returns raw arrays directly', () => {
    const input = [{ name: 'Test', image: 'test:latest', description: '', repoUrl: '', branch: '', ttl: '' }];
    expect(parsePresets(input)).toBe(input);
  });

  it('returns DEFAULT_PRESETS for placeholder string', () => {
    const result = parsePresets('__SPRITZ_UI_PRESETS__');
    expect(result).toHaveLength(4);
    expect(result[0].name).toBe('Starter (minimal)');
  });

  it('returns DEFAULT_PRESETS for empty string', () => {
    const result = parsePresets('');
    expect(result).toHaveLength(4);
  });

  it('returns DEFAULT_PRESETS for whitespace-only string', () => {
    const result = parsePresets('   ');
    expect(result).toHaveLength(4);
  });

  it('returns DEFAULT_PRESETS for undefined', () => {
    const result = parsePresets(undefined);
    expect(result).toHaveLength(4);
  });

  it('returns DEFAULT_PRESETS for null', () => {
    const result = parsePresets(null);
    expect(result).toHaveLength(4);
  });

  it('parses valid JSON string into preset array', () => {
    const presets = [{ name: 'Custom', image: 'custom:v1', description: 'desc', repoUrl: '', branch: '', ttl: '' }];
    const result = parsePresets(JSON.stringify(presets));
    expect(result).toEqual(presets);
  });

  it('returns empty array for malformed JSON (fails closed)', () => {
    const result = parsePresets('not valid json {{{');
    expect(result).toEqual([]);
  });

  it('returns empty array for non-array JSON', () => {
    const result = parsePresets('{"key": "value"}');
    expect(result).toEqual([]);
  });
});
