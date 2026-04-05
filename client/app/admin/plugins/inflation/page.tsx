"use client";

import { PluginConfigEditor } from "@/app/admin/plugins/plugin-config-editor";
import {
  listInflationPlugins,
  updateInflationPlugin,
  reorderPlugins,
} from "@/lib/portfolio-api";

export default function AdminInflationPluginsPage() {
  return (
    <PluginConfigEditor
      title="Inflation plugins"
      description="Enable or disable plugins that fetch monthly inflation index data. Config JSON can include API endpoints and rate limits; only admins can view or edit."
      listFn={listInflationPlugins}
      updateFn={updateInflationPlugin}
      reorderFn={(ids) => reorderPlugins("inflation", ids)}
    />
  );
}
