import { Outlet } from 'react-router-dom';
import { Toaster } from '@/components/ui/sonner';
import { NoticeBanner } from '@/components/notice-banner';
import { TooltipProvider } from '@/components/ui/tooltip';

export function Layout() {
  return (
    <TooltipProvider delay={400}>
      <main className="min-h-screen">
        <NoticeBanner />
        <Outlet />
        <Toaster position="bottom-right" />
      </main>
    </TooltipProvider>
  );
}
