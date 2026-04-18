import { BrowserRouter, Routes, Route, useLocation } from 'react-router-dom';
import { ConfigProvider, config } from '@/lib/config';
import { BrandingEffects } from '@/components/branding-effects';
import { NoticeProvider } from '@/components/notice-banner';
import { Layout } from '@/components/layout';
import { ChatPage } from '@/pages/chat';
import { CreatePage } from '@/pages/create';
import { TerminalPage } from '@/pages/terminal';
import { chatCatchAllRoutePath } from '@/lib/urls';

/**
 * Maps legacy SPA-mounted Slack gateway paths to the real server-rendered
 * gateway surface while preserving query and hash fragments.
 */
export function buildLegacySlackGatewayRedirectURL(
  pathname: string,
  search: string,
  hash: string,
): string {
  const nextPath = pathname.startsWith('/spritz/') ? pathname.slice('/spritz'.length) : pathname;
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
    <BrowserRouter>
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
              <Route path="terminal/:name" element={<TerminalPage />} />
              <Route path={chatCatchAllRoutePath()} element={<ChatPage />} />
            </Route>
          </Routes>
        </NoticeProvider>
      </ConfigProvider>
    </BrowserRouter>
  );
}
