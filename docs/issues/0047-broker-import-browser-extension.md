---
status: closed
title: Automate broker transaction import with a browser extension
milestone: M13
---

A Chrome MV3 extension that replaces the manual import loop: query the most
recent transaction held for a broker, compute a date window, drive the broker
website to export the transactions covering it, convert and upload them, and
report the outcome. Fidelity UK first, with the site-specific parts expressed as
data so further brokers are added by writing a recipe.

Specified in spec/broker-import-extension.md; see
adr/0014-extension-transaction-import.md and adr/0015-listtxs-broker-filter.md.
