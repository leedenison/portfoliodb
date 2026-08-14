import { describe, expect, it } from "vitest";
import { ArchivePart, ArchivePartSchema } from "@/gen/archive/v1/common_pb";
import {
  ARCHIVE_PART_LABELS,
  SYSTEM_ARCHIVE_PART_OPTIONS,
  USER_ARCHIVE_PART_OPTIONS,
  type ArchivePartOption,
} from "./parts";

/**
 * The export menu is written out by hand against a vocabulary that is not, so what
 * is worth asserting is that the two have not drifted: a part added to the proto
 * and left out here cannot be exported and reports its progress under no name.
 */
const declared = ArchivePartSchema.values.filter((v) => v.number !== 0);
const menus: [string, ArchivePartOption[]][] = [
  ["system", SYSTEM_ARCHIVE_PART_OPTIONS],
  ["user", USER_ARCHIVE_PART_OPTIONS],
];

describe("the export menus", () => {
  it("cover every part the proto declares, between them and without overlap", () => {
    const offered = menus.flatMap(([, m]) => m.map((o) => o.part));
    expect([...offered].sort()).toEqual(declared.map((v) => v.number).sort());
  });

  it.each(menus)("gives every %s part a label and a note", (_name, menu) => {
    for (const o of menu) {
      expect(o.label).not.toBe("");
      expect(o.note).not.toBe("");
    }
  });

  it.each(menus)("lists no part twice in the %s menu", (_name, menu) => {
    expect(new Set(menu.map((o) => o.part)).size).toBe(menu.length);
  });

  it("names every part a job can report against", () => {
    // A job reports progress per part, so a part with no entry here shows the
    // user a blank row rather than what it was working on.
    for (const v of declared) {
      expect(ARCHIVE_PART_LABELS[v.number]).toBeTruthy();
    }
  });

  it("labels each part the same way in the menu and in a job's progress", () => {
    for (const [, menu] of menus) {
      for (const o of menu) {
        expect(ARCHIVE_PART_LABELS[o.part]).toBe(o.label);
      }
    }
  });
});

describe("what is selected by default", () => {
  it("selects everything except plugin config", () => {
    // Plugin config carries live API keys, which makes the archive a secret and
    // changes where it can safely be kept. It is the one thing somebody has to
    // ask for rather than opt out of, so this is the assertion worth having:
    // adding a part that defaults off, or flipping this one on, both fail here.
    const off = menus.flatMap(([, m]) => m.filter((o) => !o.defaultSelected).map((o) => o.part));
    expect(off).toEqual([ArchivePart.PLUGIN_CONFIG]);
  });

  it("says in the plugin config note that it makes the file a secret", () => {
    const opt = SYSTEM_ARCHIVE_PART_OPTIONS.find((o) => o.part === ArchivePart.PLUGIN_CONFIG)!;
    expect(opt.note).toMatch(/secret/i);
  });
});

describe("the notes", () => {
  it("says the grouping is derived on import rather than carried", () => {
    // It is not carried. The postings are flat under the window and the importing
    // instance derives the partition from the evidence they hold, so a note
    // promising that grouping is preserved describes a format that was replaced.
    // See docs/adr/0043-grouping-does-not-travel-in-the-archive.md.
    const txs = USER_ARCHIVE_PART_OPTIONS.find((o) => o.part === ArchivePart.TXS)!;
    expect(txs.note).toMatch(/derived again on import/i);
    expect(txs.note).not.toMatch(/travels with|so it travels|grouping is preserved/i);
  });
});
