import { describe, expect, it } from 'vite-plus/test';
import { buildTerminalTheme, getLogoUrl, getProductName } from '@/lib/branding';

describe('branding helpers', () => {
  it('falls back to default product name', () => {
    expect(getProductName()).toBe('Spritz');
    expect(getProductName({ productName: '', logoUrl: '', faviconUrl: '', theme: {
      background: '',
      foreground: '',
      muted: '',
      mutedForeground: '',
      primary: '',
      primaryForeground: '',
      border: '',
      destructive: '',
      radius: '',
    }, terminal: { background: '', foreground: '', cursor: '' } })).toBe('Spritz');
  });

  it('falls back to the bundled default logo', () => {
    expect(getLogoUrl()).toBe('/agentol.svg');
    expect(getLogoUrl({
      productName: '',
      logoUrl: '',
      faviconUrl: '',
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
      },
      terminal: { background: '', foreground: '', cursor: '' },
    })).toBe('/agentol.svg');
  });

  it('builds terminal theme with defaults and overrides', () => {
    expect(buildTerminalTheme()).toEqual({
      background: '#000000',
      foreground: '#f0f0f0',
      cursor: '#f0f0f0',
    });

    expect(buildTerminalTheme({
      background: '#101820',
      foreground: '#fefefe',
      cursor: '#ff8800',
    })).toEqual({
      background: '#101820',
      foreground: '#fefefe',
      cursor: '#ff8800',
    });
  });
});
