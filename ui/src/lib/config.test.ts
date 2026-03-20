import { describe, expect, it } from 'vite-plus/test';
import { resolveConfig } from '@/lib/config';

describe('resolveConfig', () => {
  it('preserves branding defaults when branding is omitted', () => {
    const config = resolveConfig();

    expect(config.branding.productName).toBe('');
    expect(config.branding.logoUrl).toBe('');
    expect(config.branding.faviconUrl).toBe('');
    expect(config.branding.theme.primary).toBe('');
    expect(config.branding.terminal.cursor).toBe('');
  });

  it('merges partial branding values without dropping defaults', () => {
    const config = resolveConfig({
      branding: {
        productName: 'Example Console',
        theme: {
          primary: '#123456',
          radius: '1rem',
        },
      },
    });

    expect(config.branding.productName).toBe('Example Console');
    expect(config.branding.theme.primary).toBe('#123456');
    expect(config.branding.theme.radius).toBe('1rem');
    expect(config.branding.theme.background).toBe('');
    expect(config.branding.terminal.background).toBe('');
  });
});
