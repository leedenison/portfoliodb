"use client";

import { PluginConfigEditor } from "@/app/admin/plugins/plugin-config-editor";
import {
  listIdentifierPlugins,
  updateIdentifierPlugin,
  reorderPlugins,
} from "@/lib/portfolio-api";

export default function AdminIdentifierPluginsPage() {
  return (
    <PluginConfigEditor
      title="Identifier plugins"
      description="Enable or disable plugins and set precedence (higher runs first). Config JSON can include API keys (e.g. openfigi_api_key, openai_api_key); only admins can view or edit."
      listFn={listIdentifierPlugins}
      updateFn={updateIdentifierPlugin}
      reorderFn={(ids) => reorderPlugins("identifier", ids)}
    />
  );
}
