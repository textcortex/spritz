import { useEffect } from 'react';
import { useConfig } from '@/lib/config';
import { applyBrandingTheme, buildTerminalTheme, syncDocumentBranding } from '@/lib/branding';

export function BrandingEffects() {
  const { branding } = useConfig();

  useEffect(() => {
    const root = document.documentElement;
    const restoreTheme = applyBrandingTheme(branding.theme, root);
    const terminalTheme = buildTerminalTheme(branding.terminal);

    root.style.setProperty('--terminal-shell-background', terminalTheme.background);
    root.style.setProperty('--terminal-shell-foreground', terminalTheme.foreground);
    root.style.setProperty('--terminal-shell-cursor', terminalTheme.cursor);
    root.style.setProperty(
      '--terminal-shell-border',
      `color-mix(in srgb, ${terminalTheme.foreground} 16%, transparent)`,
    );

    const restoreDocument = syncDocumentBranding(branding);

    return () => {
      restoreTheme();
      restoreDocument();
      root.style.removeProperty('--terminal-shell-background');
      root.style.removeProperty('--terminal-shell-foreground');
      root.style.removeProperty('--terminal-shell-cursor');
      root.style.removeProperty('--terminal-shell-border');
    };
  }, [branding]);

  return null;
}
