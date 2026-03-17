import { createContext, useContext } from 'react';

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

export interface Preset {
  name: string;
  image: string;
  description: string;
  repoUrl: string;
  branch: string;
  ttl: string;
  namePrefix?: string;
}

export interface SpritzConfig {
  apiBaseUrl: string;
  ownerId: string;
  presets: Preset[] | string;
  repoDefaults: RepoDefaults;
  launch: LaunchConfig;
  auth: AuthConfig;
}

declare global {
  interface Window {
    SPRITZ_CONFIG?: Partial<SpritzConfig>;
  }
}

function loadConfig(): SpritzConfig {
  const raw = window.SPRITZ_CONFIG || {};
  return {
    apiBaseUrl: raw.apiBaseUrl || '',
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

const config = loadConfig();

const ConfigContext = createContext<SpritzConfig>(config);

export const ConfigProvider = ConfigContext.Provider;

export function useConfig(): SpritzConfig {
  return useContext(ConfigContext);
}

export { config };
