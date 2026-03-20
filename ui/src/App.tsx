import { useEffect } from 'react';
import { BrowserRouter, Routes, Route, useNavigate, useLocation } from 'react-router-dom';
import { ConfigProvider, config } from '@/lib/config';
import { BrandingEffects } from '@/components/branding-effects';
import { NoticeProvider } from '@/components/notice-banner';
import { Layout } from '@/components/layout';
import { chatConversationPath, chatPath } from '@/lib/urls';
import { ChatPage } from '@/pages/chat';
import { CreatePage } from '@/pages/create';
import { TerminalPage } from '@/pages/terminal';

function HashRedirect() {
  const navigate = useNavigate();
  const location = useLocation();

  useEffect(() => {
    const hash = window.location.hash;
    if (!hash || hash === '#') return;

    // Convert legacy hash routes to path routes.
    // #chat/name → /c/name
    // #create → /create
    // #terminal/name → /terminal/name
    const match = hash.match(/^#(chat|terminal|create)(?:\/(.+?))?(?:\/([^/]+))?$/);
    if (match) {
      const route = match[1];
      const param = match[2] || '';
      const subParam = match[3] || '';
      const path = route === 'chat'
        ? subParam
          ? chatConversationPath(param, subParam)
          : chatPath(param)
        : subParam
          ? `/${route}/${param}/${subParam}`
          : param
            ? `/${route}/${param}`
            : `/${route}`;
      window.history.replaceState(null, '', window.location.pathname + window.location.search);
      navigate(path, { replace: true });
    }
  }, [location, navigate]);

  return null;
}

export function App() {
  return (
    <BrowserRouter>
      <ConfigProvider value={config}>
        <BrandingEffects />
        <NoticeProvider>
          <HashRedirect />
          <Routes>
            <Route element={<Layout />}>
              <Route index element={<ChatPage />} />
              <Route path="create" element={<CreatePage />} />
              <Route path="terminal/:name" element={<TerminalPage />} />
              <Route path="c/:name?" element={<ChatPage />} />
              <Route path="c/:name/:conversationId" element={<ChatPage />} />
              <Route path="chat/:name?" element={<ChatPage />} />
              <Route path="chat/:name/:conversationId" element={<ChatPage />} />
            </Route>
          </Routes>
        </NoticeProvider>
      </ConfigProvider>
    </BrowserRouter>
  );
}
