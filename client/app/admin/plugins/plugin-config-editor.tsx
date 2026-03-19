"use client";

import { useCallback, useEffect, useState } from "react";
import {
  DndContext,
  closestCenter,
  KeyboardSensor,
  PointerSensor,
  useSensor,
  useSensors,
  type DragEndEvent,
} from "@dnd-kit/core";
import {
  SortableContext,
  sortableKeyboardCoordinates,
  useSortable,
  verticalListSortingStrategy,
  arrayMove,
} from "@dnd-kit/sortable";
import { CSS } from "@dnd-kit/utilities";
import { ErrorAlert } from "@/app/components/error-alert";

interface PluginConfig {
  pluginId: string;
  enabled: boolean;
  precedence: number;
  configJson: string;
}

function getConfigEntries(configJson: string | undefined): [string, string][] {
  if (!configJson?.trim()) return [];
  try {
    const obj = JSON.parse(configJson) as Record<string, unknown>;
    if (obj === null || typeof obj !== "object" || Array.isArray(obj))
      return [];
    return Object.entries(obj).map(([k, v]) => [
      k,
      v === null ? "null" : typeof v === "string" ? v : JSON.stringify(v),
    ]);
  } catch {
    return [];
  }
}

function GripIcon() {
  return (
    <svg
      width="16"
      height="16"
      viewBox="0 0 16 16"
      fill="currentColor"
      className="shrink-0"
    >
      <circle cx="5" cy="3" r="1.5" />
      <circle cx="11" cy="3" r="1.5" />
      <circle cx="5" cy="8" r="1.5" />
      <circle cx="11" cy="8" r="1.5" />
      <circle cx="5" cy="13" r="1.5" />
      <circle cx="11" cy="13" r="1.5" />
    </svg>
  );
}

function SortablePluginCard<T extends PluginConfig>({
  plugin,
  editingId,
  editConfig,
  saving,
  onToggleEnabled,
  onStartEdit,
  onSaveEdit,
  onCancelEdit,
  onEditConfigChange,
  renderExtra,
  onUpdate,
}: {
  plugin: T;
  editingId: string | null;
  editConfig: string;
  saving: boolean;
  onToggleEnabled: (plugin: T) => void;
  onStartEdit: (plugin: T) => void;
  onSaveEdit: () => void;
  onCancelEdit: () => void;
  onEditConfigChange: (value: string) => void;
  renderExtra?: (plugin: T, opts: { saving: boolean; onUpdate: (updated: T) => void }) => React.ReactNode;
  onUpdate: (updated: T) => void;
}) {
  const {
    attributes,
    listeners,
    setNodeRef,
    transform,
    transition,
    isDragging,
  } = useSortable({ id: plugin.pluginId });

  const style = {
    transform: CSS.Transform.toString(transform),
    transition,
    opacity: isDragging ? 0.5 : 1,
  };

  const configEntries = getConfigEntries(plugin.configJson);

  return (
    <li
      ref={setNodeRef}
      style={style}
      className="rounded-md border border-border bg-surface p-4 shadow-sm"
    >
      <div className="flex flex-wrap items-center justify-between gap-2">
        <div className="flex items-center gap-2">
          <button
            type="button"
            className="cursor-grab touch-none text-text-muted hover:text-text-primary active:cursor-grabbing"
            aria-label={`Drag to reorder ${plugin.pluginId}`}
            {...attributes}
            {...listeners}
          >
            <GripIcon />
          </button>
          <span className="font-medium text-text-primary">
            {plugin.pluginId}
          </span>
          <span className="text-sm text-text-muted">
            precedence {plugin.precedence}
          </span>
        </div>
        <div className="flex items-center gap-2">
          <button
            type="button"
            onClick={() => onToggleEnabled(plugin)}
            disabled={saving}
            className={`rounded px-3 py-1.5 text-sm font-medium ${
              plugin.enabled
                ? "bg-green-600 text-white hover:bg-green-700"
                : "bg-border text-text-muted hover:bg-border/80"
            }`}
          >
            {plugin.enabled ? "Enabled" : "Disabled"}
          </button>
          {editingId === plugin.pluginId ? (
            <>
              <button
                type="button"
                onClick={onSaveEdit}
                disabled={saving}
                className="rounded bg-primary px-3 py-1.5 text-sm font-medium text-white hover:bg-primary-dark"
              >
                Save
              </button>
              <button
                type="button"
                onClick={onCancelEdit}
                disabled={saving}
                className="rounded border border-border px-3 py-1.5 text-sm hover:bg-background"
              >
                Cancel
              </button>
            </>
          ) : (
            <button
              type="button"
              onClick={() => onStartEdit(plugin)}
              className="rounded border border-border px-3 py-1.5 text-sm hover:bg-background"
            >
              Edit config
            </button>
          )}
        </div>
      </div>
      {configEntries.length > 0 && (
        <dl className="mt-3 flex flex-col gap-y-1 text-sm">
          {configEntries.map(([key, value]) => (
            <div key={key} className="flex gap-2">
              <dt className="shrink-0 font-medium text-text-muted after:content-[':']">
                {key}
              </dt>
              <dd className="min-w-0 break-all text-text-primary">{value}</dd>
            </div>
          ))}
        </dl>
      )}
      {renderExtra?.(plugin, { saving, onUpdate })}
      {editingId === plugin.pluginId && (
        <div className="mt-4">
          <label className="block text-sm font-medium text-text-muted">
            Config JSON
          </label>
          <textarea
            value={editConfig}
            onChange={(e) => onEditConfigChange(e.target.value)}
            rows={8}
            className="mt-1 w-full rounded border border-border bg-background px-3 py-2 font-mono text-sm"
            spellCheck={false}
          />
        </div>
      )}
    </li>
  );
}

export function PluginConfigEditor<T extends PluginConfig>({
  title,
  description,
  listFn,
  updateFn,
  reorderFn,
  renderExtra,
}: {
  title: string;
  description: string;
  listFn: () => Promise<T[]>;
  updateFn: (
    pluginId: string,
    opts: { enabled?: boolean; configJson?: string }
  ) => Promise<T>;
  reorderFn?: (pluginIds: string[]) => Promise<void>;
  renderExtra?: (plugin: T, opts: { saving: boolean; onUpdate: (updated: T) => void }) => React.ReactNode;
}) {
  const [plugins, setPlugins] = useState<T[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [editingId, setEditingId] = useState<string | null>(null);
  const [editConfig, setEditConfig] = useState("");
  const [saving, setSaving] = useState(false);

  const sensors = useSensors(
    useSensor(PointerSensor, { activationConstraint: { distance: 5 } }),
    useSensor(KeyboardSensor, { coordinateGetter: sortableKeyboardCoordinates })
  );

  const load = useCallback(async () => {
    setLoading(true);
    setError(null);
    try {
      const list = await listFn();
      setPlugins(list);
    } catch (e) {
      setError(e instanceof Error ? e.message : "Failed to load plugins");
    } finally {
      setLoading(false);
    }
  }, [listFn]);

  useEffect(() => {
    load();
  }, [load]);

  async function handleToggleEnabled(plugin: T) {
    setSaving(true);
    setError(null);
    try {
      const updated = await updateFn(plugin.pluginId, {
        enabled: !plugin.enabled,
      });
      setPlugins((prev) =>
        prev.map((p) => (p.pluginId === updated.pluginId ? updated : p))
      );
    } catch (e) {
      setError(e instanceof Error ? e.message : "Failed to update");
    } finally {
      setSaving(false);
    }
  }

  function startEdit(plugin: T) {
    setEditingId(plugin.pluginId);
    setEditConfig(plugin.configJson || "{}");
  }

  function cancelEdit() {
    setEditingId(null);
    setEditConfig("");
  }

  async function saveEdit() {
    if (!editingId) return;
    setSaving(true);
    setError(null);
    try {
      const updated = await updateFn(editingId, {
        configJson: editConfig,
      });
      setPlugins((prev) =>
        prev.map((p) => (p.pluginId === updated.pluginId ? updated : p))
      );
      cancelEdit();
    } catch (e) {
      setError(e instanceof Error ? e.message : "Failed to save config");
    } finally {
      setSaving(false);
    }
  }

  async function handleDragEnd(event: DragEndEvent) {
    const { active, over } = event;
    if (!over || active.id === over.id || !reorderFn) return;
    const oldIdx = plugins.findIndex((p) => p.pluginId === active.id);
    const newIdx = plugins.findIndex((p) => p.pluginId === over.id);
    const reordered = arrayMove(plugins, oldIdx, newIdx);
    const prev = plugins;
    setPlugins(reordered);
    setError(null);
    try {
      await reorderFn(reordered.map((p) => p.pluginId));
    } catch (e) {
      setPlugins(prev);
      setError(e instanceof Error ? e.message : "Failed to reorder");
    }
  }

  if (loading) {
    return (
      <div className="text-text-muted">Loading plugin configs...</div>
    );
  }

  const pluginIds = plugins.map((p) => p.pluginId);

  return (
    <div className="space-y-6">
      <h1 className="font-display text-xl font-bold text-text-primary">
        {title}
      </h1>
      <p className="text-sm text-text-muted">{description}</p>
      {error && <ErrorAlert>{error}</ErrorAlert>}
      <DndContext
        sensors={sensors}
        collisionDetection={closestCenter}
        onDragEnd={handleDragEnd}
      >
        <SortableContext items={pluginIds} strategy={verticalListSortingStrategy}>
          <ul className="space-y-4">
            {plugins.map((plugin) => (
              <SortablePluginCard
                key={plugin.pluginId}
                plugin={plugin}
                editingId={editingId}
                editConfig={editConfig}
                saving={saving}
                onToggleEnabled={handleToggleEnabled}
                onStartEdit={startEdit}
                onSaveEdit={saveEdit}
                onCancelEdit={cancelEdit}
                onEditConfigChange={setEditConfig}
                renderExtra={renderExtra}
                onUpdate={(updated) =>
                  setPlugins((prev) =>
                    prev.map((p) => (p.pluginId === updated.pluginId ? updated : p))
                  )
                }
              />
            ))}
          </ul>
        </SortableContext>
      </DndContext>
      {plugins.length === 0 && !loading && (
        <p className="text-text-muted">No plugins in config.</p>
      )}
    </div>
  );
}
