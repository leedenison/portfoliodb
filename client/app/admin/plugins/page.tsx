"use client";

import { useCallback, useEffect, useState } from "react";
import {
  listIdentifierPlugins,
  updateIdentifierPlugin,
} from "@/lib/portfolio-api";
import type { IdentifierPluginConfig } from "@/gen/api/v1/api_pb";

export default function AdminPluginsPage() {
  const [plugins, setPlugins] = useState<IdentifierPluginConfig[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [editingId, setEditingId] = useState<string | null>(null);
  const [editConfig, setEditConfig] = useState("");
  const [saving, setSaving] = useState(false);

  const load = useCallback(async () => {
    setLoading(true);
    setError(null);
    try {
      const list = await listIdentifierPlugins();
      setPlugins(list);
    } catch (e) {
      setError(e instanceof Error ? e.message : "Failed to load plugins");
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    load();
  }, [load]);

  async function handleToggleEnabled(plugin: IdentifierPluginConfig) {
    setSaving(true);
    setError(null);
    try {
      const updated = await updateIdentifierPlugin(plugin.pluginId, {
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

  function startEdit(plugin: IdentifierPluginConfig) {
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
      const updated = await updateIdentifierPlugin(editingId, {
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

  if (loading) {
    return (
      <div className="text-text-muted">Loading plugin configs…</div>
    );
  }

  return (
    <div className="space-y-6">
      <h1 className="text-xl font-semibold text-text-primary">
        Identifier plugins
      </h1>
      <p className="text-sm text-text-muted">
        Enable or disable plugins and set precedence (higher runs first).
        Config JSON can include API keys (e.g. openfigi_api_key, openai_api_key);
        only admins can view or edit.
      </p>
      {error && (
        <div className="rounded border border-red-500/50 bg-red-500/10 px-3 py-2 text-sm text-red-700 dark:text-red-300">
          {error}
        </div>
      )}
      <ul className="space-y-4">
        {plugins.map((plugin) => (
          <li
            key={plugin.pluginId}
            className="rounded-lg border border-border bg-background p-4 shadow-sm"
          >
            <div className="flex flex-wrap items-center justify-between gap-2">
              <div>
                <span className="font-medium text-text-primary">
                  {plugin.pluginId}
                </span>
                <span className="ml-2 text-sm text-text-muted">
                  precedence {plugin.precedence}
                </span>
              </div>
              <div className="flex items-center gap-2">
                <button
                  type="button"
                  onClick={() => handleToggleEnabled(plugin)}
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
                      onClick={saveEdit}
                      disabled={saving}
                      className="rounded bg-primary px-3 py-1.5 text-sm font-medium text-white hover:bg-primary-dark"
                    >
                      Save
                    </button>
                    <button
                      type="button"
                      onClick={cancelEdit}
                      disabled={saving}
                      className="rounded border border-border px-3 py-1.5 text-sm hover:bg-background"
                    >
                      Cancel
                    </button>
                  </>
                ) : (
                  <button
                    type="button"
                    onClick={() => startEdit(plugin)}
                    className="rounded border border-border px-3 py-1.5 text-sm hover:bg-background"
                  >
                    Edit config
                  </button>
                )}
              </div>
            </div>
            {editingId === plugin.pluginId && (
              <div className="mt-4">
                <label className="block text-sm font-medium text-text-muted">
                  Config JSON
                </label>
                <textarea
                  value={editConfig}
                  onChange={(e) => setEditConfig(e.target.value)}
                  rows={8}
                  className="mt-1 w-full rounded border border-border bg-background px-3 py-2 font-mono text-sm"
                  spellCheck={false}
                />
              </div>
            )}
          </li>
        ))}
      </ul>
      {plugins.length === 0 && !loading && (
        <p className="text-text-muted">No plugins in config.</p>
      )}
    </div>
  );
}
