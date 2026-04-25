import { BrowserRouter, Routes, Route, useLocation } from 'react-router-dom';
import { ConfigProvider, config } from '@/lib/config';
import { BrandingEffects } from '@/components/branding-effects';
import { NoticeProvider } from '@/components/notice-banner';
import { Layout } from '@/components/layout';
import { ChatPage } from '@/pages/chat';
import { CreatePage } from '@/pages/create';
import { SettingsPage } from '@/pages/settings';
import { TerminalPage } from '@/pages/terminal';
import { chatCatchAllRoutePath, normalizeChatPathPrefix } from '@/lib/urls';
import { slackGatewayBasePath } from '@/lib/slack-management';

/**
 * Maps legacy SPA-mounted Slack gateway paths to the real server-rendered
 * gateway surface while preserving query and hash fragments.
 */
export function buildLegacySlackGatewayRedirectURL(
  pathname: string,
  search: string,
  hash: string,
): string {
  const legacyPrefix = '/spritz/slack-gateway';
  const nextPath = pathname.startsWith(legacyPrefix)
    ? `${slackGatewayBasePath()}${pathname.slice(legacyPrefix.length)}`
    : pathname.startsWith('/spritz/')
      ? pathname.slice('/spritz'.length)
      : pathname;
  return `${nextPath}${search}${hash}`;
}

/**
 * Performs a full-page navigation to a non-SPA route.
 */
export const browserLocation = {
  replace(url: string): void {
    window.location.replace(url);
  },
};

function routeMarkerIndex(pathname: string, marker: string): number {
  const index = pathname.indexOf(marker);
  if (index < 0) return -1;
  const markerEnd = index + marker.length;
  if (pathname.length === markerEnd || pathname[markerEnd] === '/') return index;
  return -1;
}

/**
 * Infers the path prefix used by the host page before the SPA route segment.
 *
 * The Slack gateway can redirect to a React app hosted under a non-root
 * SpritzBaseURL such as /spritz/settings/slack/workspaces. BrowserRouter needs
 * that /spritz basename to make the normal settings route tree match.
 */
export function inferBrowserRouterBasename(
  pathname = typeof window !== 'undefined' ? window.location.pathname : '/',
  chatPathPrefix = config.chatPathPrefix,
): string | undefined {
  const normalizedPath = `/${String(pathname || '/').replace(/^\/+/, '')}`;
  const markers = Array.from(new Set([
    '/settings',
    normalizeChatPathPrefix(chatPathPrefix),
    '/create',
    '/terminal',
    '/spritz/slack-gateway',
  ].filter((marker) => marker && marker !== '/')));

  for (const marker of markers) {
    const index = routeMarkerIndex(normalizedPath, marker);
    if (index === 0) return undefined;
    if (index > 0) return normalizedPath.slice(0, index);
  }
  return undefined;
}

function LegacySlackGatewayRedirectPage() {
  const location = useLocation();

  if (typeof window !== 'undefined') {
    browserLocation.replace(
      buildLegacySlackGatewayRedirectURL(location.pathname, location.search, location.hash),
    );
  }

  return null;
}

export function App() {
  return (
    <BrowserRouter basename={inferBrowserRouterBasename()}>
      <ConfigProvider value={config}>
        <BrandingEffects />
        <NoticeProvider>
          <Routes>
            <Route
              path="spritz/slack-gateway/*"
              element={<LegacySlackGatewayRedirectPage />}
            />
            <Route element={<Layout />}>
              <Route index element={<ChatPage />} />
              <Route path="create" element={<CreatePage />} />
              <Route path="settings/*" element={<SettingsPage />} />
              <Route path="terminal/:name" element={<TerminalPage />} />
              <Route path={chatCatchAllRoutePath()} element={<ChatPage />} />
            </Route>
          </Routes>
        </NoticeProvider>
      </ConfigProvider>
    </BrowserRouter>
  );
}
