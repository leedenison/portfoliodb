"use client";

import { PluginConfigEditor } from "@/app/admin/plugins/plugin-config-editor";
import {
  listCandidatePlugins,
  updateCandidatePlugin,
  reorderPlugins,
} from "@/lib/portfolio-api";

export default function AdminCandidatePluginsPage() {
  return (
    <PluginConfigEditor
      title="Candidate plugins"
      description="Enable or disable plugins that extract identifier hints from broker instrument descriptions. They run in series by precedence (higher runs first); the first that returns hints is used. Config JSON can include API keys; only admins can view or edit."
      category="candidate"
      listFn={listCandidatePlugins}
      updateFn={updateCandidatePlugin}
      reorderFn={(ids) => reorderPlugins("candidate", ids)}
    />
  );
}
