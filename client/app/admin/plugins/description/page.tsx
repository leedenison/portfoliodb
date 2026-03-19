"use client";

import { PluginConfigEditor } from "@/app/admin/plugins/plugin-config-editor";
import {
  listDescriptionPlugins,
  updateDescriptionPlugin,
  reorderPlugins,
} from "@/lib/portfolio-api";

export default function AdminDescriptionPluginsPage() {
  return (
    <PluginConfigEditor
      title="Description plugins"
      description="Enable or disable plugins that extract identifier hints from broker instrument descriptions. They run in series by precedence (higher runs first); the first that returns hints is used. Config JSON can include API keys; only admins can view or edit."
      listFn={listDescriptionPlugins}
      updateFn={updateDescriptionPlugin}
      reorderFn={(ids) => reorderPlugins("description", ids)}
    />
  );
}
