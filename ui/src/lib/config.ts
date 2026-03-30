import { createContext, useContext } from 'react';

type DeepPartial<T> = {
  [K in keyof T]?: T[K] extends Array<infer U>
    ? Array<DeepPartial<U>>
    : T[K] extends object
      ? DeepPartial<T[K]>
      : T[K];
};

export interface AuthRefreshConfig {
  enabled: string;
  url: string;
  method: string;
  credentials: string;
  tokenStorageKeys: string;
  timeoutMs: string;
  cooldownMs: string;
  headers: string;
}

export interface AuthConfig {
  mode: string;
  tokenStorage: string;
  tokenStorageKeys: string;
  bearerTokenParam: string;
  loginUrl: string;
  returnToMode: string;
  returnToParam: string;
  redirectOnUnauthorized: string;
  refresh: AuthRefreshConfig;
}

export interface RepoDefaults {
  url: string;
  dir: string;
  branch: string;
  hideInputs: string;
}

export interface LaunchConfig {
  queryParams: string;
}

export interface BrandingThemeConfig {
  background: string;
  foreground: string;
  muted: string;
  mutedForeground: string;
  primary: string;
  primaryForeground: string;
  border: string;
  destructive: string;
  radius: string;
}

export interface BrandingTerminalConfig {
  background: string;
  foreground: string;
  cursor: string;
}

export interface BrandingConfig {
  productName: string;
  logoUrl: string;
  faviconUrl: string;
  theme: BrandingThemeConfig;
  terminal: BrandingTerminalConfig;
}

export interface Preset {
  id?: string;
  name: string;
  image: string;
  description: string;
  repoUrl: string;
  branch: string;
  ttl: string;
  namePrefix?: string;
  hidden?: boolean;
}

export interface SpritzConfig {
  apiBaseUrl: string;
  websocketBaseUrl: string;
  chatPathPrefix: string;
  ownerId: string;
  presets: Preset[] | string;
  repoDefaults: RepoDefaults;
  launch: LaunchConfig;
  branding: BrandingConfig;
  auth: AuthConfig;
}

export type RawSpritzConfig = DeepPartial<SpritzConfig>;

declare global {
  interface Window {
    SPRITZ_CONFIG?: RawSpritzConfig;
  }
}

export function resolveConfig(raw: RawSpritzConfig = {}): SpritzConfig {
  return {
    apiBaseUrl: raw.apiBaseUrl || '',
    websocketBaseUrl: raw.websocketBaseUrl || '',
    chatPathPrefix: raw.chatPathPrefix || '/c',
    ownerId: raw.ownerId || '',
    presets: raw.presets || [],
    repoDefaults: {
      url: '',
      dir: '',
      branch: '',
      hideInputs: '',
      ...(raw.repoDefaults || {}),
    },
    launch: {
      queryParams: '',
      ...(raw.launch || {}),
    },
    branding: {
      productName: '',
      logoUrl: '',
      faviconUrl: '',
      ...(raw.branding || {}),
      theme: {
        background: '',
        foreground: '',
        muted: '',
        mutedForeground: '',
        primary: '',
        primaryForeground: '',
        border: '',
        destructive: '',
        radius: '',
        ...(raw.branding?.theme || {}),
      },
      terminal: {
        background: '',
        foreground: '',
        cursor: '',
        ...(raw.branding?.terminal || {}),
      },
    },
    auth: {
      mode: '',
      tokenStorage: 'localStorage',
      tokenStorageKeys: '',
      bearerTokenParam: 'token',
      loginUrl: '',
      returnToMode: 'auto',
      returnToParam: '',
      redirectOnUnauthorized: 'true',
      ...(raw.auth || {}),
      refresh: {
        enabled: 'false',
        url: '',
        method: 'POST',
        credentials: 'include',
        tokenStorageKeys: '',
        timeoutMs: '5000',
        cooldownMs: '30000',
        headers: '',
        ...(raw.auth?.refresh || {}),
      },
    },
  };
}

function loadConfig(): SpritzConfig {
  return resolveConfig(window.SPRITZ_CONFIG || {});
}

const config = loadConfig();

const ConfigContext = createContext<SpritzConfig>(config);

export const ConfigProvider = ConfigContext.Provider;

export function useConfig(): SpritzConfig {
  return useContext(ConfigContext);
}

export { config };
