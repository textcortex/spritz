import { createContext, useCallback, useContext, useState, type ReactNode } from 'react';
import { cn } from '@/lib/utils';
import { XIcon } from 'lucide-react';

interface NoticeState {
  message: string;
  type: 'error' | 'info';
}

interface NoticeContextValue {
  notice: NoticeState | null;
  showNotice: (message: string, type?: 'error' | 'info') => void;
  clearNotice: () => void;
}

const NoticeContext = createContext<NoticeContextValue>({
  notice: null,
  showNotice: () => {},
  clearNotice: () => {},
});

export function useNotice() {
  return useContext(NoticeContext);
}

export function NoticeProvider({ children }: { children: ReactNode }) {
  const [notice, setNotice] = useState<NoticeState | null>(null);

  const showNotice = useCallback((message: string, type: 'error' | 'info' = 'error') => {
    if (!message) {
      setNotice(null);
      return;
    }
    setNotice({ message, type });
  }, []);

  const clearNotice = useCallback(() => {
    setNotice(null);
  }, []);

  return (
    <NoticeContext.Provider value={{ notice, showNotice, clearNotice }}>
      {children}
    </NoticeContext.Provider>
  );
}

export function NoticeBanner() {
  const { notice, clearNotice } = useNotice();

  if (!notice) return null;

  return (
    <div
      className={cn(
        'flex items-center justify-between gap-2 px-4 py-2 text-sm',
        notice.type === 'error' && 'bg-destructive/10 text-destructive',
        notice.type === 'info' && 'bg-primary/10 text-primary',
      )}
      role="alert"
    >
      <span>{notice.message}</span>
      <button
        type="button"
        aria-label="Dismiss notice"
        onClick={clearNotice}
        className="shrink-0 rounded p-0.5 hover:bg-black/10"
      >
        <XIcon aria-hidden="true" className="size-3.5" />
      </button>
    </div>
  );
}
