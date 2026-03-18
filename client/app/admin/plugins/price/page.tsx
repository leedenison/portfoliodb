"use client";

import { PluginConfigEditor } from "@/app/admin/plugins/plugin-config-editor";
import {
  listPricePlugins,
  updatePricePlugin,
} from "@/lib/portfolio-api";

export default function AdminPricePluginsPage() {
  return (
    <PluginConfigEditor
      title="Price plugins"
      description="Enable or disable plugins that fetch end-of-day prices for identified instruments. Config JSON can include API keys and rate limits; only admins can view or edit."
      listFn={listPricePlugins}
      updateFn={updatePricePlugin}
    />
  );
}
