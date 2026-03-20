import type { Preset } from '@/lib/config';
import type { FormSelection } from '@/lib/form-state';
import { Label } from '@/components/ui/label';

interface PresetPanelProps {
  presets: Preset[];
  selectedIndex: string;
  onSelect: (preset: Preset | null, index: string) => void;
}

export function PresetPanel({ presets, selectedIndex, onSelect }: PresetPanelProps) {
  if (!presets.length) return null;

  const selectedPreset = selectedIndex ? presets[Number(selectedIndex)] : null;

  return (
    <div className="flex flex-col gap-2">
      <Label htmlFor="preset-select">Preset</Label>
      <select
        id="preset-select"
        value={selectedIndex || 'custom'}
        onChange={(e) => {
          const value = e.target.value;
          if (!value || value === 'custom') {
            onSelect(null, '');
            return;
          }
          const preset = presets[Number(value)];
          if (preset) onSelect(preset, value);
        }}
        className="flex h-11 w-full rounded-[var(--radius-lg)] border border-input bg-transparent px-3 py-1 text-sm shadow-none transition-colors focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring"
      >
        <option value="custom">Custom</option>
        {presets.map((preset, index) => (
          <option key={index} value={String(index)}>
            {preset.name} ({preset.image})
          </option>
        ))}
      </select>
      {selectedPreset?.description && (
        <p className="text-xs text-muted-foreground">{selectedPreset.description}</p>
      )}
    </div>
  );
}

export function findPresetIndex(
  presets: Preset[],
  selection: FormSelection | undefined,
): string {
  if (!selection || selection.mode !== 'preset') return '';
  const presetName = String(selection.presetName || '').trim();
  const presetImage = String(selection.presetImage || '').trim();
  const idx = presets.findIndex((preset) => {
    const matchesImage = presetImage && String(preset.image || '').trim() === presetImage;
    const matchesName = presetName && String(preset.name || '').trim() === presetName;
    if (presetImage && presetName) return matchesImage && matchesName;
    return matchesImage || matchesName;
  });
  return idx >= 0 ? String(idx) : '';
}
