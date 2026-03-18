"use client";

import { useState } from "react";
import { PluginConfigEditor } from "@/app/admin/plugins/plugin-config-editor";
import {
  listPricePlugins,
  updatePricePlugin,
} from "@/lib/portfolio-api";
import type { PricePluginConfig } from "@/gen/api/v1/api_pb";

export default function AdminPricePluginsPage() {
  return (
    <PluginConfigEditor
      title="Price plugins"
      description="Enable or disable plugins that fetch end-of-day prices for identified instruments. Config JSON can include API keys and rate limits; only admins can view or edit."
      listFn={listPricePlugins}
      updateFn={updatePricePlugin}
      renderExtra={(plugin, { saving, onUpdate }) => (
        <MaxHistoryDaysField
          plugin={plugin}
          saving={saving}
          onUpdate={onUpdate}
        />
      )}
    />
  );
}

function MaxHistoryDaysField({
  plugin,
  saving,
  onUpdate,
}: {
  plugin: PricePluginConfig;
  saving: boolean;
  onUpdate: (updated: PricePluginConfig) => void;
}) {
  const current = plugin.maxHistoryDays ?? undefined;
  const [editing, setEditing] = useState(false);
  const [value, setValue] = useState(current?.toString() ?? "");
  const [localSaving, setLocalSaving] = useState(false);

  async function save() {
    setLocalSaving(true);
    try {
      const days = value.trim() === "" ? 0 : parseInt(value, 10);
      if (value.trim() !== "" && (isNaN(days) || days < 0)) return;
      const updated = await updatePricePlugin(plugin.pluginId, {
        maxHistoryDays: days,
      });
      onUpdate(updated);
      setEditing(false);
    } finally {
      setLocalSaving(false);
    }
  }

  return (
    <div className="mt-3 flex items-center gap-2 text-sm">
      <span className="font-medium text-text-muted">Max history days:</span>
      {editing ? (
        <>
          <input
            type="number"
            min="0"
            value={value}
            onChange={(e) => setValue(e.target.value)}
            className="w-24 rounded border border-border bg-background px-2 py-1 text-sm"
          />
          <button
            type="button"
            onClick={save}
            disabled={saving || localSaving}
            className="rounded bg-primary px-2 py-1 text-xs font-medium text-white hover:bg-primary-dark disabled:opacity-50"
          >
            Save
          </button>
          <button
            type="button"
            onClick={() => setEditing(false)}
            disabled={localSaving}
            className="rounded border border-border px-2 py-1 text-xs hover:bg-background"
          >
            Cancel
          </button>
        </>
      ) : (
        <>
          <span className="text-text-primary">
            {current != null && current > 0 ? `${current} days` : "Unlimited"}
          </span>
          <button
            type="button"
            onClick={() => {
              setValue(current?.toString() ?? "");
              setEditing(true);
            }}
            className="rounded border border-border px-2 py-1 text-xs hover:bg-background"
          >
            Edit
          </button>
        </>
      )}
    </div>
  );
}
