import { describe, it, expect, beforeEach } from 'vite-plus/test';
import { createMockStorage } from '@/test/helpers';
import {
  deriveFormSelection,
  buildCreateFormState,
  readCreateFormState,
  writeCreateFormState,
  clearCreateFormState,
} from './form-state';
import type { Preset } from './config';

const PRESET: Preset = {
  id: 'starter',
  name: 'Starter',
  image: 'spritz-starter:latest',
  description: '',
  repoUrl: '',
  branch: '',
  ttl: '',
};

describe('deriveFormSelection', () => {
  it('returns preset mode when images match', () => {
    const result = deriveFormSelection(PRESET, 'spritz-starter:latest');
    expect(result.mode).toBe('preset');
    expect(result.presetId).toBe('starter');
    expect(result.presetName).toBe('Starter');
    expect(result.presetImage).toBe('spritz-starter:latest');
  });

  it('returns custom mode when images differ', () => {
    const result = deriveFormSelection(PRESET, 'other-image:v1');
    expect(result.mode).toBe('custom');
  });

  it('returns custom mode when no active preset', () => {
    const result = deriveFormSelection(null, 'spritz-starter:latest');
    expect(result.mode).toBe('custom');
  });
});

describe('buildCreateFormState', () => {
  it('returns null when all fields are empty', () => {
    const result = buildCreateFormState({
      activePreset: null,
      image: '',
      repo: '',
      branch: '',
      ttl: '',
      namespace: '',
      userConfig: '',
    });
    expect(result).toBeNull();
  });

  it('returns state with preset selection', () => {
    const result = buildCreateFormState({
      activePreset: PRESET,
      image: 'spritz-starter:latest',
      repo: '',
      branch: '',
      ttl: '',
      namespace: '',
      userConfig: '',
    });
    expect(result).not.toBeNull();
    expect(result!.selection.mode).toBe('preset');
  });

  it('returns state with custom selection when image is filled', () => {
    const result = buildCreateFormState({
      activePreset: null,
      image: 'my-image:v1',
      repo: '',
      branch: '',
      ttl: '',
      namespace: '',
      userConfig: '',
    });
    expect(result).not.toBeNull();
    expect(result!.selection.mode).toBe('custom');
    expect(result!.fields.image).toBe('my-image:v1');
  });
});

describe('localStorage round-trip', () => {
  let mockStorage: Storage;

  beforeEach(() => {
    mockStorage = createMockStorage();
    Object.defineProperty(globalThis, 'localStorage', { value: mockStorage, writable: true });
  });

  it('reads null when nothing stored', () => {
    expect(readCreateFormState()).toBeNull();
  });

  it('writes and reads state', () => {
    const state = buildCreateFormState({
      activePreset: PRESET,
      image: 'spritz-starter:latest',
      repo: 'https://github.com/example/repo',
      branch: 'main',
      ttl: '1h',
      namespace: 'default',
      userConfig: '',
    });
    writeCreateFormState(state);
    const restored = readCreateFormState();
    expect(restored).not.toBeNull();
    expect(restored!.selection.mode).toBe('preset');
    expect(restored!.selection.presetId).toBe('starter');
    expect(restored!.fields.repo).toBe('https://github.com/example/repo');
  });

  it('reads legacy preset selections without presetId', () => {
    mockStorage.setItem('spritz:create-form', JSON.stringify({
      selection: {
        mode: 'preset',
        presetName: 'Starter',
        presetImage: 'spritz-starter:latest',
      },
      fields: {
        image: 'spritz-starter:latest',
        repo: '',
        branch: '',
        ttl: '',
        namespace: '',
        userConfig: '',
      },
    }));

    const restored = readCreateFormState();
    expect(restored).not.toBeNull();
    expect(restored!.selection.mode).toBe('preset');
    expect(restored!.selection.presetId).toBe('');
    expect(restored!.selection.presetName).toBe('Starter');
  });

  it('clearCreateFormState removes stored state', () => {
    const state = buildCreateFormState({
      activePreset: PRESET,
      image: 'spritz-starter:latest',
      repo: '',
      branch: '',
      ttl: '',
      namespace: '',
      userConfig: '',
    });
    writeCreateFormState(state);
    expect(readCreateFormState()).not.toBeNull();
    clearCreateFormState();
    expect(readCreateFormState()).toBeNull();
  });

  it('writeCreateFormState with null clears storage', () => {
    const state = buildCreateFormState({
      activePreset: PRESET,
      image: 'spritz-starter:latest',
      repo: '',
      branch: '',
      ttl: '',
      namespace: '',
      userConfig: '',
    });
    writeCreateFormState(state);
    writeCreateFormState(null);
    expect(readCreateFormState()).toBeNull();
  });
});
