import { useState, useCallback, useEffect, useRef } from 'react';
import { PlusIcon, DicesIcon, ChevronDownIcon } from 'lucide-react';
import { Tooltip, TooltipTrigger, TooltipContent } from '@/components/ui/tooltip';
import { request } from '@/lib/api';
import { useConfig, type Preset } from '@/lib/config';
import { usePresets } from '@/lib/presets';
import { buildCreatePayload, parseUserConfigInput } from '@/lib/create-payload';
import {
  readCreateFormState,
  writeCreateFormState,
  buildCreateFormState,
} from '@/lib/form-state';
import { findPresetIndex, PresetPanel } from '@/components/preset-panel';
import { hideRepoInputs, defaultRepoUrl, defaultRepoBranch, defaultRepoDir } from '@/lib/urls';
import { useNotice } from '@/components/notice-banner';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Label } from '@/components/ui/label';
import { Textarea } from '@/components/ui/textarea';

interface CreateFormProps {
  onCreated?: () => void;
}

const USER_CONFIG_PLACEHOLDER = `sharedMounts:
  - name: config
    mountPath: /home/dev/.config
    scope: owner
    mode: snapshot
    syncMode: poll
    pollSeconds: 30
ttl: 8h`;

export function CreateForm({ onCreated }: CreateFormProps) {
  const config = useConfig();
  const presets = usePresets();
  const { showNotice } = useNotice();

  const [name, setName] = useState('');
  const [image, setImage] = useState('');
  const [repo, setRepo] = useState('');
  const [branch, setBranch] = useState('');
  const [ttl, setTtl] = useState('');
  const [namespace, setNamespace] = useState('');
  const [userConfig, setUserConfig] = useState('');
  const [advancedOpen, setAdvancedOpen] = useState(false);
  const [submitting, setSubmitting] = useState(false);
  const [generatingName, setGeneratingName] = useState(false);
  const [activePreset, setActivePreset] = useState<Preset | null>(null);
  const [presetIndex, setPresetIndex] = useState('');
  const initialized = useRef(false);

  const generateName = useCallback(async (imageOverride?: string) => {
    const imageValue = (imageOverride ?? image).trim();
    if (!imageValue) return;
    setGeneratingName(true);
    try {
      const payload: Record<string, string> = { image: imageValue };
      if (namespace.trim()) payload.namespace = namespace.trim();
      const data = await request<{ name: string }>('/spritzes/suggest-name', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(payload),
      });
      setName(String(data?.name || '').trim());
    } catch {
      // Silently ignore — user can retry manually
    } finally {
      setGeneratingName(false);
    }
  }, [image, namespace]);

  // Restore form state from localStorage on mount
  useEffect(() => {
    if (initialized.current) return;
    initialized.current = true;

    const saved = readCreateFormState();
    if (saved) {
      setImage(saved.fields.image);
      setRepo(saved.fields.repo);
      setBranch(saved.fields.branch);
      setTtl(saved.fields.ttl);
      setNamespace(saved.fields.namespace);
      setUserConfig(saved.fields.userConfig);

      if (saved.selection.mode === 'preset' && presets.length) {
        const idx = findPresetIndex(presets, saved.selection);
        if (idx) {
          setPresetIndex(idx);
          setActivePreset(presets[Number(idx)]);
        }
      }
      // Auto-generate a name from saved image
      if (saved.fields.image) {
        generateName(saved.fields.image);
      }
    } else if (presets.length > 0) {
      // Default to first preset
      setPresetIndex('0');
      setActivePreset(presets[0]);
      setImage(presets[0].image || '');
      if (presets[0].repoUrl !== undefined) setRepo(presets[0].repoUrl || '');
      if (presets[0].branch !== undefined) setBranch(presets[0].branch || '');
      if (presets[0].ttl !== undefined) setTtl(presets[0].ttl || '');
      // Auto-generate a name from preset image
      if (presets[0].image) {
        generateName(presets[0].image);
      }
    }
  }, [presets, generateName]);

  // Persist form state on changes
  const persistState = useCallback(() => {
    const state = buildCreateFormState({
      activePreset,
      image,
      repo,
      branch,
      ttl,
      namespace,
      userConfig,
    });
    writeCreateFormState(state);
  }, [activePreset, image, repo, branch, ttl, namespace, userConfig]);

  useEffect(() => {
    if (!initialized.current) return;
    persistState();
  }, [persistState]);

  const handlePresetSelect = useCallback(
    (preset: Preset | null, index: string) => {
      setPresetIndex(index);
      setActivePreset(preset);
      if (preset) {
        setImage(preset.image || '');
        if (preset.repoUrl !== undefined) setRepo(preset.repoUrl || '');
        if (preset.branch !== undefined) setBranch(preset.branch || '');
        if (preset.ttl !== undefined) setTtl(preset.ttl || '');
      }
    },
    [],
  );

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    const imageValue = image.trim();

    const payload = buildCreatePayload({
      name: name.trim(),
      imageValue,
      namespace: namespace.trim(),
      ownerId: config.ownerId || '',
      activePreset,
      repoValue: repo.trim(),
      branchValue: branch.trim(),
      defaultRepoUrl,
      defaultRepoBranch,
      defaultRepoDir,
      ttlValue: ttl.trim(),
    });

    if (userConfig.trim()) {
      try {
        const parsed = parseUserConfigInput(userConfig);
        if (parsed && Object.keys(parsed).length > 0) {
          (payload as Record<string, unknown>).userConfig = parsed;
        }
      } catch (err: unknown) {
        showNotice(err instanceof Error ? err.message : 'Invalid user config YAML/JSON.');
        return;
      }
    }

    setSubmitting(true);
    try {
      await request('/spritzes', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(payload),
      });
      setName('');
      persistState();
      showNotice('', 'info');
      onCreated?.();
    } catch (err: unknown) {
      showNotice(err instanceof Error ? err.message : 'Failed to create instance.');
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <form onSubmit={handleSubmit} className="flex flex-col gap-5">
      <PresetPanel
        presets={presets}
        selectedIndex={presetIndex}
        onSelect={handlePresetSelect}
      />

      <div className="flex flex-col gap-2">
        <Label htmlFor="name">Name</Label>
        <div className="flex gap-2">
          <Input
            id="name"
            value={name}
            onChange={(e) => setName(e.target.value)}
            placeholder="Auto-generated name"
            className="h-11"
          />
          <Tooltip>
            <TooltipTrigger
              render={
                <Button
                  type="button"
                  variant="outline"
                  className="size-11 shrink-0 p-0"
                  onClick={() => generateName()}
                  disabled={generatingName}
                />
              }
            >
              <DicesIcon className={`size-4 ${generatingName ? 'animate-spin' : ''}`} />
            </TooltipTrigger>
            <TooltipContent>Generate random name</TooltipContent>
          </Tooltip>
        </div>
      </div>

      <div>
        <Button
          type="button"
          variant="ghost"
          size="sm"
          aria-expanded={advancedOpen}
          className="gap-1 text-muted-foreground"
          onClick={() => setAdvancedOpen(!advancedOpen)}
        >
          <ChevronDownIcon
            aria-hidden="true"
            className={`size-4 transition-transform will-change-transform ${advancedOpen ? 'rotate-180' : ''}`}
          />
          Advanced options
        </Button>
        <div
          className="grid transition-[grid-template-rows] duration-300 ease-in-out will-change-[grid-template-rows]"
          style={{ gridTemplateRows: advancedOpen ? '1fr' : '0fr' }}
        >
          <div className="overflow-hidden min-h-0">
            <div className="flex flex-col gap-5 pt-3">
          <div className="flex flex-col gap-2">
            <Label htmlFor="image">Image</Label>
            <Input
              id="image"
              className="h-11"
              value={image}
              onChange={(e) => setImage(e.target.value)}
              placeholder="spritz-starter:latest"
            />
          </div>

          {!hideRepoInputs && (
            <div className="grid gap-4 sm:grid-cols-2">
              <div className="flex flex-col gap-2">
                <Label htmlFor="repo">Repository URL</Label>
                <Input
                  id="repo"
                  className="h-11"
                  value={repo}
                  onChange={(e) => setRepo(e.target.value)}
                  placeholder="https://github.com/..."
                />
              </div>
              <div className="flex flex-col gap-2">
                <Label htmlFor="branch">Branch</Label>
                <Input
                  id="branch"
                  className="h-11"
                  value={branch}
                  onChange={(e) => setBranch(e.target.value)}
                  placeholder="main"
                />
              </div>
            </div>
          )}

          <div className="grid gap-4 sm:grid-cols-2">
            <div className="flex flex-col gap-2">
              <Label htmlFor="ttl">TTL</Label>
              <Input
                id="ttl"
                className="h-11"
                value={ttl}
                onChange={(e) => setTtl(e.target.value)}
                placeholder="8h"
              />
            </div>
            <div className="flex flex-col gap-2">
              <Label htmlFor="namespace">Namespace</Label>
              <Input
                id="namespace"
                className="h-11"
                value={namespace}
                onChange={(e) => setNamespace(e.target.value)}
                placeholder="default"
              />
            </div>
          </div>

          <div className="flex flex-col gap-2">
            <Label htmlFor="user-config">User config (YAML/JSON)</Label>
            <Textarea
              id="user-config"
              value={userConfig}
              onChange={(e) => setUserConfig(e.target.value)}
              rows={8}
              spellCheck={false}
              placeholder={USER_CONFIG_PLACEHOLDER}
              className="font-mono text-sm"
            />
            <p className="text-xs text-muted-foreground">
              Provide shared mounts, ttl, repo, env, or resources. JSON is also accepted.
            </p>
          </div>
            </div>
          </div>
        </div>
      </div>

      <Button type="submit" disabled={submitting} aria-busy={submitting} className="h-11 w-fit cursor-pointer px-4">
        {submitting ? (
          'Creating…'
        ) : (
          <>
            <PlusIcon aria-hidden="true" className="size-4" />
            Create instance
          </>
        )}
      </Button>
    </form>
  );
}
