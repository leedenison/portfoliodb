/**
 * Popup entry point.
 *
 * Only the settings form is wired up; the sync controls and the run log are
 * placeholders until the sync orchestration lands.
 */

import { loadConfig, missingRequired, saveConfig, type Config } from "../config";
import type { SessionStatus } from "../lib/messages";

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
  render(await loadConfig());

  renderSession(await chrome.runtime.sendMessage({ type: "status" }));

  document.getElementById("connect")?.addEventListener("click", () => {
    renderSession({ connected: false, error: "connecting..." });
    void chrome.runtime.sendMessage({ type: "connect" }).then(renderSession);
  });

  const form = document.getElementById("config-form");
  form?.addEventListener("submit", (e) => {
    e.preventDefault();
    const next = readForm();
    void saveConfig(next).then(() => {
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
