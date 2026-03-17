import type { Preset } from './config';

const CREATE_FORM_STORAGE_KEY = 'spritz:create-form';

export interface FormSelection {
  mode: 'preset' | 'custom';
  presetName?: string;
  presetImage?: string;
}

export interface FormFields {
  image: string;
  repo: string;
  branch: string;
  ttl: string;
  namespace: string;
  userConfig: string;
}

export interface CreateFormState {
  selection: FormSelection;
  fields: FormFields;
}

function normalizeString(value: unknown): string {
  if (value === undefined || value === null) return '';
  return String(value);
}

function trimString(value: unknown): string {
  return normalizeString(value).trim();
}

function imagesMatch(left: unknown, right: unknown): boolean {
  return trimString(left) === trimString(right);
}

export function deriveFormSelection(
  activePreset: Preset | null,
  imageValue: string,
): FormSelection {
  if (!activePreset || !imagesMatch(imageValue, activePreset.image)) {
    return { mode: 'custom' };
  }
  return {
    mode: 'preset',
    presetName: trimString(activePreset.name),
    presetImage: trimString(activePreset.image),
  };
}

function hasMeaningfulState(state: CreateFormState): boolean {
  if (state.selection?.mode === 'preset') return true;
  const fields = state.fields;
  if (!fields) return false;
  return (['image', 'repo', 'branch', 'ttl', 'namespace', 'userConfig'] as const).some(
    (key) => trimString(fields[key]) !== '',
  );
}

function sanitizeSelection(raw: unknown): FormSelection {
  if (!raw || typeof raw !== 'object') return { mode: 'custom' };
  const r = raw as Record<string, unknown>;
  if (r.mode !== 'preset') return { mode: 'custom' };
  return {
    mode: 'preset',
    presetName: trimString(r.presetName),
    presetImage: trimString(r.presetImage),
  };
}

function sanitizeFields(raw: unknown): FormFields {
  const input = raw && typeof raw === 'object' ? (raw as Record<string, unknown>) : {};
  return {
    image: normalizeString(input.image),
    repo: normalizeString(input.repo),
    branch: normalizeString(input.branch),
    ttl: normalizeString(input.ttl),
    namespace: normalizeString(input.namespace),
    userConfig: normalizeString(input.userConfig),
  };
}

function sanitize(raw: unknown): CreateFormState | null {
  if (!raw || typeof raw !== 'object') return null;
  const r = raw as Record<string, unknown>;
  const state: CreateFormState = {
    selection: sanitizeSelection(r.selection),
    fields: sanitizeFields(r.fields),
  };
  return hasMeaningfulState(state) ? state : null;
}

export function buildCreateFormState(values: {
  activePreset: Preset | null;
  image: string;
  repo: string;
  branch: string;
  ttl: string;
  namespace: string;
  userConfig: string;
}): CreateFormState | null {
  return sanitize({
    selection: deriveFormSelection(values.activePreset, values.image),
    fields: {
      image: values.image,
      repo: values.repo,
      branch: values.branch,
      ttl: values.ttl,
      namespace: values.namespace,
      userConfig: values.userConfig,
    },
  });
}

export function readCreateFormState(): CreateFormState | null {
  try {
    const raw = localStorage.getItem(CREATE_FORM_STORAGE_KEY);
    if (!raw) return null;
    return sanitize(JSON.parse(raw));
  } catch {
    try {
      localStorage.removeItem(CREATE_FORM_STORAGE_KEY);
    } catch {
      // ignore
    }
    return null;
  }
}

export function writeCreateFormState(state: CreateFormState | null): void {
  const sanitized = state ? sanitize(state) : null;
  if (!sanitized) {
    try {
      localStorage.removeItem(CREATE_FORM_STORAGE_KEY);
    } catch {
      // ignore
    }
    return;
  }
  try {
    localStorage.setItem(CREATE_FORM_STORAGE_KEY, JSON.stringify(sanitized));
  } catch {
    // ignore
  }
}

export function clearCreateFormState(): void {
  try {
    localStorage.removeItem(CREATE_FORM_STORAGE_KEY);
  } catch {
    // ignore
  }
}
