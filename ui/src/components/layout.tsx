import { Outlet } from 'react-router-dom';
import { Toaster } from '@/components/ui/sonner';
import { NoticeBanner } from '@/components/notice-banner';
import { TooltipProvider } from '@/components/ui/tooltip';

export function Layout() {
  return (
    <TooltipProvider delay={400}>
      <a
        href="#main-content"
        className="sr-only focus:not-sr-only focus:fixed focus:left-4 focus:top-4 focus:z-[100] focus:rounded-lg focus:bg-background focus:px-4 focus:py-2 focus:text-sm focus:font-medium focus:shadow-lg focus:ring-2 focus:ring-ring"
      >
        Skip to main content
      </a>
      <main id="main-content" className="min-h-screen">
        <NoticeBanner />
        <Outlet />
        <Toaster position="bottom-right" />
      </main>
    </TooltipProvider>
  );
}
