import { describe, it, expect } from "vitest";
import { readTxDocument } from "@/lib/archive/tx-document";
import { marshalUser } from "@/lib/archive/codec";
import { Broker, TxType } from "@/gen/type/v1/type_pb";
import { timestampFromDate } from "@bufbuild/protobuf/wkt";

function doc(extra: Record<string, unknown> = {}, windows?: unknown[]) {
  return marshalUser({
    envelope: { sourceInstance: "test", exportedAt: timestampFromDate(new Date("2026-08-01")) },
    txs: {
      windows: (windows ?? [
        {
          broker: Broker.FIDELITY,
          source: "Fidelity:web:archive",
          periodFrom: timestampFromDate(new Date("2024-01-01")),
          periodBefore: timestampFromDate(new Date("2024-02-01")),
          postings: [
            {
              timestamp: timestampFromDate(new Date("2024-01-15")),
              instrumentDescription: "AAPL",
              brokerTxType: [TxType.TRADE_ASSET],
              quantity: "10",
            },
          ],
        },
      ]) as never,
    },
    ...extra,
  });
}

describe("readTxDocument", () => {
  it("takes the one window the document states", () => {
    const { window, errors } = readTxDocument(doc());

    expect(errors).toEqual([]);
    expect(window?.broker).toBe(Broker.FIDELITY);
    expect(window?.postings).toHaveLength(1);
  });

  // The upload replaces one broker's period, so it has nowhere to put a second
  // window or a part it is not asked about. Refusing beats importing some of it.
  it("refuses a document covering more than one broker or period", () => {
    const { window, errors } = readTxDocument(
      doc({}, [
        { broker: Broker.FIDELITY, source: "a", postings: [] },
        { broker: Broker.IBKR, source: "b", postings: [] },
      ]),
    );

    expect(window).toBeUndefined();
    expect(errors[0]!.message).toContain("more than one broker or period");
  });

  it("refuses a document that carries parts other than transactions", () => {
    const { window, errors } = readTxDocument(doc({ preferences: { displayCurrency: "GBP" } }));

    expect(window).toBeUndefined();
    expect(errors[0]!.message).toContain("more than transactions");
  });

  // Declarations are the other user part, and a broker upload has nowhere to
  // put them: they restate holdings rather than replacing a period.
  it("refuses a document that carries declarations", () => {
    const { window, errors } = readTxDocument(
      doc({
        declarations: {
          statements: [
            {
              broker: Broker.FIDELITY,
              account: "Z1",
              asOfDate: "2024-01-31",
              declarations: [
                { instrument: { type: 11, value: "AAPL", domain: "XNAS" }, declaredQty: "100" },
              ],
            },
          ],
        },
      }),
    );

    expect(window).toBeUndefined();
    expect(errors[0]!.message).toContain("more than transactions");
  });

  it("reports a file that is not an archive as a parse error rather than throwing", () => {
    const { window, errors } = readTxDocument("date,type\n2024-01-01,BUYSTOCK");

    expect(window).toBeUndefined();
    expect(errors).toHaveLength(1);
  });

  it("refuses an archive with no transaction part", () => {
    const json = marshalUser({
      envelope: { sourceInstance: "test", exportedAt: timestampFromDate(new Date("2026-08-01")) },
    });

    expect(readTxDocument(json).errors[0]!.message).toContain("no transactions");
  });
});
