import { describe, it, expect, vi } from 'vite-plus/test';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { PresetPanel, findPresetIndex } from './preset-panel';
import type { Preset } from '@/lib/config';

const PRESETS: Preset[] = [
  { name: 'Starter', image: 'spritz-starter:latest', description: 'Minimal starter', repoUrl: '', branch: '', ttl: '' },
  { name: 'Devbox', image: 'spritz-devbox:latest', description: '', repoUrl: '', branch: '', ttl: '' },
];

describe('PresetPanel', () => {
  it('renders nothing when presets array is empty', () => {
    const { container } = render(
      <PresetPanel presets={[]} selectedIndex="" onSelect={() => {}} />,
    );
    expect(container.firstChild).toBeNull();
  });

  it('renders select with Custom default and all presets', () => {
    render(<PresetPanel presets={PRESETS} selectedIndex="" onSelect={() => {}} />);
    const select = screen.getByLabelText('Preset') as HTMLSelectElement;
    expect(select.value).toBe('custom');
    expect(screen.getByText('Starter (spritz-starter:latest)')).toBeDefined();
    expect(screen.getByText('Devbox (spritz-devbox:latest)')).toBeDefined();
  });

  it('calls onSelect with correct preset when option is selected', async () => {
    const onSelect = vi.fn();
    render(<PresetPanel presets={PRESETS} selectedIndex="" onSelect={onSelect} />);
    const select = screen.getByLabelText('Preset');
    await userEvent.selectOptions(select, '0');
    expect(onSelect).toHaveBeenCalledWith(PRESETS[0], '0');
  });

  it('calls onSelect with null when Custom is selected', async () => {
    const onSelect = vi.fn();
    render(<PresetPanel presets={PRESETS} selectedIndex="0" onSelect={onSelect} />);
    const select = screen.getByLabelText('Preset');
    await userEvent.selectOptions(select, 'custom');
    expect(onSelect).toHaveBeenCalledWith(null, '');
  });

  it('shows description when selected preset has one', () => {
    render(<PresetPanel presets={PRESETS} selectedIndex="0" onSelect={() => {}} />);
    expect(screen.getByText('Minimal starter')).toBeDefined();
  });
});

describe('findPresetIndex', () => {
  it('returns empty string when no selection', () => {
    expect(findPresetIndex(PRESETS, undefined)).toBe('');
  });

  it('returns empty string when mode is custom', () => {
    expect(findPresetIndex(PRESETS, { mode: 'custom' })).toBe('');
  });

  it('returns correct index by image match', () => {
    expect(findPresetIndex(PRESETS, {
      mode: 'preset',
      presetImage: 'spritz-devbox:latest',
    })).toBe('1');
  });

  it('returns correct index by name match', () => {
    expect(findPresetIndex(PRESETS, {
      mode: 'preset',
      presetName: 'Starter',
    })).toBe('0');
  });

  it('returns empty string when no match', () => {
    expect(findPresetIndex(PRESETS, {
      mode: 'preset',
      presetImage: 'nonexistent:latest',
      presetName: 'Nonexistent',
    })).toBe('');
  });
});
