import { Outlet } from 'react-router-dom';
import { Toaster } from '@/components/ui/sonner';
import { NoticeBanner } from '@/components/notice-banner';

export function Layout() {
  return (
    <main className="min-h-screen">
      <NoticeBanner />
      <Outlet />
      <Toaster position="bottom-right" />
    </main>
  );
}
