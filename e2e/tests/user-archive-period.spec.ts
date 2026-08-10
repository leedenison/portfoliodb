// E2E test: a period-scoped user archive export, and the partial replace on the
// way back in.
//
// The two halves are one contract. An export asked for a period adheres strictly
// to it, so a group straddling a bound contributes only its in-period legs and
// the exported group does not balance. An import replaces the period by deleting
// the postings inside it rather than the groups holding them, so the legs the file
// does not carry survive and the remainder is re-balanced. Neither half is safe
// without the other: a whole-group delete would destroy legs nothing re-inserts.
// See docs/adr/0039-replace-by-period-deletes-postings-not-groups.md.
//
// The archive page has no period control, so the export runs through the RPC.

import { test, expect } from "@playwright/test";
import { seedSession, closeRedis, TEST_USER_ID } from "../helpers/auth";
import {
  resetAndSeedBase,
  closeDB,
  rawQuery,
  seedFixture,
} from "../helpers/db";
import { exportUserTxWindows, importUserArchiveAndWait } from "../helpers/api";
import { timestampNow } from "@bufbuild/protobuf/wkt";
import { ArchiveKind } from "../gen/archive/v1/common_pb";
import { Broker } from "../gen/type/v1/type_pb";
import { JobStatus } from "../gen/api/v1/api_pb";

// The seeded run: -5000 leaving ACC-1 on the tenth, +5000 arriving in ACC-2 on
// the eleventh, in one group.
const DAY_TWO = new Date("2024-03-11T00:00:00Z");
const DAY_THREE = new Date("2024-03-12T00:00:00Z");

type Posting = {
  account: string;
  timestamp: Date;
  quantity: string;
  account_type: string;
};

async function postings(): Promise<Posting[]> {
  return (await rawQuery(
    `SELECT account, timestamp, quantity::text AS quantity, account_type
     FROM txs WHERE user_id = $1 ORDER BY timestamp, account_type`,
    [TEST_USER_ID],
  )) as Posting[];
}

test.beforeAll(async () => {
  await resetAndSeedBase();
  await seedFixture("user-archive-straddle.sql");
});

test.afterAll(async () => {
  await closeRedis();
  await closeDB();
});

test.describe("period-scoped user archive", () => {
  let sessionId: string;

  test.beforeAll(async () => {
    sessionId = await seedSession("user");
  });

  test("exports only the period asked for and re-imports it without losing the rest", async () => {
    // Asked for nothing, the window is derived from the postings it carries and
    // holds the whole run.
    const whole = await exportUserTxWindows(sessionId);
    expect(whole).toHaveLength(1);
    expect(whole[0].postings).toHaveLength(2);

    // Asked for the second day alone, the window states that day and the group
    // contributes one leg. The exported group does not balance, which the format
    // accepts: the importer routes the residual.
    const windows = await exportUserTxWindows(sessionId, {
      from: DAY_TWO,
      before: DAY_THREE,
    });
    expect(windows).toHaveLength(1);
    const window = windows[0];
    expect(window.broker).toBe(Broker.FIDELITY);
    expect(window.periodFrom?.seconds).toBe(
      BigInt(DAY_TWO.getTime() / 1000),
    );
    expect(window.periodBefore?.seconds).toBe(
      BigInt(DAY_THREE.getTime() / 1000),
    );
    expect(window.postings).toHaveLength(1);
    expect(window.postings[0].quantity).toBe("5000");
    expect(window.postings[0].groupRef).toBe("g0");

    // Re-importing that file replaces the second day. The first day's leg is
    // outside it and the file does not carry it, so the whole-group delete this
    // replaced would have destroyed it with nothing to put it back.
    const job = await importUserArchiveAndWait(sessionId, {
      envelope: {
        formatVersion: 1,
        exportedAt: timestampNow(),
        kind: ArchiveKind.USER,
      },
      txs: { windows: [window] },
    });
    expect(job.status).toBe(JobStatus.SUCCESS);

    const rows = await postings();
    // The surviving leg and the counterparty routed for it, plus the re-imported
    // leg and the counterparty routed for that.
    expect(rows).toHaveLength(4);

    const day10 = rows.filter(
      (r) => new Date(r.timestamp) < DAY_TWO,
    );
    expect(day10).toHaveLength(2);
    // Compared as numbers: the seeded row keeps the scale raw SQL wrote it with
    // and the routed one does not, which is a trailing zero rather than a value.
    expect(day10.map((r) => Number(r.quantity)).sort()).toEqual([-5000, 5000]);
    // Transfer family, so the residual is clearing rather than an imbalance:
    // a plain IMBALANCE would hide the surviving half from the transfer matcher,
    // which keys strictly on TRANSFER_CLEARING.
    expect(
      day10.filter((r) => r.account_type === "TRANSFER_CLEARING"),
    ).toHaveLength(1);

    // Two groups where the converter wrote one, each balanced on its own.
    const groups = (await rawQuery(
      `SELECT count(DISTINCT group_id)::int AS n FROM txs WHERE user_id = $1`,
      [TEST_USER_ID],
    )) as { n: number }[];
    expect(groups[0].n).toBe(2);

    const unbalanced = (await rawQuery(
      `SELECT group_id FROM txs WHERE user_id = $1
       GROUP BY group_id, weight_commodity HAVING SUM(weight) <> 0`,
      [TEST_USER_ID],
    )) as unknown[];
    expect(unbalanced).toHaveLength(0);
  });

  test("re-importing the same file again lands on the same rows", async () => {
    const before = await postings();
    const windows = await exportUserTxWindows(sessionId, {
      from: DAY_TWO,
      before: DAY_THREE,
    });
    const job = await importUserArchiveAndWait(sessionId, {
      envelope: {
        formatVersion: 1,
        exportedAt: timestampNow(),
        kind: ArchiveKind.USER,
      },
      txs: { windows },
    });
    expect(job.status).toBe(JobStatus.SUCCESS);

    // Stable under repetition: the second day's group is replaced by an
    // identical one and the first day is not touched at all.
    const after = await postings();
    expect(after.map((r) => [r.account, r.quantity, r.account_type])).toEqual(
      before.map((r) => [r.account, r.quantity, r.account_type]),
    );
  });
});
