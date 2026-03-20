import { BrowserRouter, Routes, Route } from 'react-router-dom';
import { ConfigProvider, config } from '@/lib/config';
import { BrandingEffects } from '@/components/branding-effects';
import { NoticeProvider } from '@/components/notice-banner';
import { Layout } from '@/components/layout';
import { ChatPage } from '@/pages/chat';
import { CreatePage } from '@/pages/create';
import { TerminalPage } from '@/pages/terminal';

export function App() {
  return (
    <BrowserRouter>
      <ConfigProvider value={config}>
        <BrandingEffects />
        <NoticeProvider>
          <Routes>
            <Route element={<Layout />}>
              <Route index element={<ChatPage />} />
              <Route path="create" element={<CreatePage />} />
              <Route path="terminal/:name" element={<TerminalPage />} />
              <Route path="c/:name?" element={<ChatPage />} />
              <Route path="c/:name/:conversationId" element={<ChatPage />} />
            </Route>
          </Routes>
        </NoticeProvider>
      </ConfigProvider>
    </BrowserRouter>
  );
}
