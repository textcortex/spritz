import { describe, it, expect } from 'vite-plus/test';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { NoticeProvider, NoticeBanner, useNotice } from './notice-banner';

function TestHarness() {
  const { showNotice } = useNotice();
  return (
    <>
      <button onClick={() => showNotice('Error occurred', 'error')}>Show Error</button>
      <button onClick={() => showNotice('Info message', 'info')}>Show Info</button>
      <NoticeBanner />
    </>
  );
}

function renderWithNotice() {
  return render(
    <NoticeProvider>
      <TestHarness />
    </NoticeProvider>,
  );
}

describe('NoticeBanner', () => {
  it('renders nothing when no notice is set', () => {
    render(
      <NoticeProvider>
        <NoticeBanner />
      </NoticeProvider>,
    );
    expect(screen.queryByRole('alert')).toBeNull();
  });

  it('shows error notice with correct message', async () => {
    renderWithNotice();
    await userEvent.click(screen.getByText('Show Error'));
    expect(screen.getByRole('alert')).toBeDefined();
    expect(screen.getByText('Error occurred')).toBeDefined();
  });

  it('shows info notice with correct message', async () => {
    renderWithNotice();
    await userEvent.click(screen.getByText('Show Info'));
    expect(screen.getByRole('alert')).toBeDefined();
    expect(screen.getByText('Info message')).toBeDefined();
  });

  it('dismiss button clears the notice', async () => {
    renderWithNotice();
    await userEvent.click(screen.getByText('Show Error'));
    expect(screen.getByRole('alert')).toBeDefined();

    const alert = screen.getByRole('alert');
    const closeButton = alert.querySelector('button');
    expect(closeButton).not.toBeNull();
    await userEvent.click(closeButton!);

    expect(screen.queryByRole('alert')).toBeNull();
  });
});
