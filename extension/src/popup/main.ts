/**
 * Popup entry point.
 *
 * Settings, session connect and dry run are wired up; the sync button and the
 * run log are placeholders until the sync orchestration lands.
 */

import { loadConfig, missingRequired, saveConfig, type Config } from "../config";
import { formatDate } from "../lib/dates";
import type { DryRunResult, SessionStatus } from "../lib/messages";
import { requestOriginPermission, requestPatternPermission } from "../lib/permissions";
import { getRecipe } from "../brokers";

const RECIPE_ID = "fidelity-uk";

function describeDryRun(r: DryRunResult): string {
  const lines: string[] = [];
  if (r.requested) lines.push(`window     ${r.requested.from} to ${r.requested.to}`);
  if (r.rowCount !== undefined) lines.push(`rows       ${r.rowCount}`);
  if (r.txCount !== undefined) lines.push(`converted  ${r.txCount}`);
  if (r.droppedRows) lines.push(`dropped    ${r.droppedRows}`);
  if (r.droppedTypes?.length) lines.push(`unknown    ${r.droppedTypes.join(", ")}`);
  if (r.error) lines.push(`error      ${r.error}`);
  if (r.preview) lines.push(`preview    ${r.preview}`);
  return lines.join("\n");
}

function field(name: string): HTMLInputElement {
  const el = document.getElementById(name);
  if (!(el instanceof HTMLInputElement)) throw new Error(`missing input: ${name}`);
  return el;
}

function render(config: Config): void {
  field("portfoliodbOrigin").value = config.portfoliodbOrigin;
  field("currency").value = config.currency;
  field("historyStartDate").value = config.historyStartDate;
  field("lookbackDays").value = String(config.lookbackDays);
  field("timeZone").value = config.timeZone;
}

function readForm(): Config {
  const lookbackDays = parseInt(field("lookbackDays").value, 10);
  return {
    portfoliodbOrigin: field("portfoliodbOrigin").value.trim().replace(/\/$/, ""),
    currency: field("currency").value.trim().toUpperCase(),
    historyStartDate: field("historyStartDate").value,
    lookbackDays: Number.isNaN(lookbackDays) ? 0 : lookbackDays,
    timeZone: field("timeZone").value.trim(),
  };
}

function setStatus(text: string, ok: boolean): void {
  const el = document.getElementById("config-status");
  if (!el) return;
  el.textContent = text;
  el.className = `status ${ok ? "status-ok" : "status-error"}`;
}

function renderSession(status: SessionStatus): void {
  const el = document.getElementById("session-status");
  if (!el) return;
  if (status.connected) {
    el.textContent = `Session: connected${status.email ? ` as ${status.email}` : ""}`;
    el.className = "status status-ok";
    return;
  }
  el.textContent = status.error ? `Session: ${status.error}` : "Session: not connected";
  el.className = status.error ? "status status-error" : "status status-unknown";
}

async function main(): Promise<void> {
  // Held so the connect handler can read the origin without awaiting. It uses the
  // saved value rather than the live form field, because the service worker
  // checks the permission against the saved one too.
  let config = await loadConfig();
  render(config);

  renderSession(await chrome.runtime.sendMessage({ type: "status" }));

  document.getElementById("connect")?.addEventListener("click", () => {
    const origin = config.portfoliodbOrigin;
    if (!origin) {
      renderSession({ connected: false, error: "set the PortfolioDB origin in settings first" });
      return;
    }
    // Called synchronously inside the gesture: permissions.request requires one,
    // and awaiting anything first can lose it. The service worker cannot make
    // this call at all, so it only checks the result.
    void requestOriginPermission(origin).then(async (granted) => {
      if (!granted) {
        renderSession({ connected: false, error: `access to ${origin} was declined` });
        return;
      }
      renderSession({ connected: false, error: "connecting..." });
      renderSession(await chrome.runtime.sendMessage({ type: "connect" }));
    });
  });

  // Default the dry-run window to the last 30 days, ending yesterday: a
  // transaction dated today may not have completed.
  const yesterday = new Date();
  yesterday.setDate(yesterday.getDate() - 1);
  const monthBefore = new Date(yesterday);
  monthBefore.setDate(monthBefore.getDate() - 30);
  const asInput = (d: Date) => formatDate(d, "yyyy-MM-dd");
  field("dry-from").value = asInput(monthBefore);
  field("dry-to").value = asInput(yesterday);

  const output = document.getElementById("dry-run-output");
  document.getElementById("dry-run")?.addEventListener("click", () => {
    const recipe = getRecipe(RECIPE_ID);
    if (!recipe) return;
    const from = field("dry-from").value;
    const to = field("dry-to").value;
    // Requested in the gesture, as permissions.request requires.
    void requestPatternPermission(recipe.origins).then(async (granted) => {
      if (!output) return;
      output.hidden = false;
      if (!granted) {
        output.textContent = `access to ${recipe.origins.join(", ")} was declined`;
        return;
      }
      output.textContent = "running...";
      const result: DryRunResult = await chrome.runtime.sendMessage({
        type: "dry-run",
        recipeId: RECIPE_ID,
        from,
        to,
      });
      output.textContent = describeDryRun(result);
    });
  });

  const form = document.getElementById("config-form");
  form?.addEventListener("submit", (e) => {
    e.preventDefault();
    const next = readForm();
    void saveConfig(next).then((saved) => {
      config = saved;
      const missing = missingRequired(next);
      if (missing.length > 0) {
        setStatus(`Saved. Still required: ${missing.join(", ")}`, false);
      } else {
        setStatus("Saved.", true);
      }
    });
  });
}

void main();
