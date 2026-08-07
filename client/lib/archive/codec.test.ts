import { describe, it, expect } from "vitest";
import { readFileSync } from "node:fs";
import { timestampFromDate } from "@bufbuild/protobuf/wkt";
import {
  FORMAT_VERSION,
  ArchiveKindError,
  ArchiveVersionError,
  marshalAdmin,
  marshalUser,
  unmarshalAdmin,
  unmarshalUser,
} from "@/lib/archive/codec";
import { ArchiveKind } from "@/gen/archive/v1/common_pb";
import { AssetClass, AccountType, Broker, IdentifierType, TxType } from "@/gen/type/v1/type_pb";

const exportedAt = timestampFromDate(new Date("2026-07-30T00:00:00Z"));

/** The same document as adminFixture() in server/archive/codec_test.go. */
function adminFixture() {
  return {
    envelope: { exportedAt, sourceInstance: "portfoliodb.example.com" },
    instruments: {
      instruments: [
        {
          assetClass: AssetClass.STOCK,
          currency: "USD",
          exchangeMic: "XNAS",
          identifiers: [
            { type: IdentifierType.MIC_TICKER, value: "AAPL", domain: "XNAS", canonical: true },
          ],
        },
      ],
    },
    prices: {
      groups: [
        {
          instrument: { type: IdentifierType.MIC_TICKER, value: "AAPL", domain: "XNAS" },
          assetClass: AssetClass.STOCK,
          currency: "USD",
          coverage: [{ from: "2024-01-01", before: "2024-01-17" }],
          rows: [{ priceDate: "2024-01-15", close: "185.9", volume: 48088700n }],
        },
      ],
    },
  };
}

/** The same document as userFixture() in server/archive/codec_test.go. */
function userFixture() {
  return {
    envelope: { exportedAt, sourceInstance: "portfoliodb.example.com" },
    preferences: { displayCurrency: "GBP", ignoredAssetClasses: {} },
    txs: {
      windows: [
        {
          broker: Broker.IBKR,
          periodFrom: timestampFromDate(new Date("2024-01-01T00:00:00Z")),
          periodBefore: timestampFromDate(new Date("2024-02-01T00:00:00Z")),
          source: "IBKR:web:ibkr-ofx",
          groups: [
            {
              postings: [
                {
                  timestamp: timestampFromDate(new Date("2024-01-15T00:00:00Z")),
                  account: "U123",
                  accountType: AccountType.USER,
                  type: TxType.BUYSTOCK,
                  instrumentDescription: "APPLE INC",
                  quantity: "10",
                  unitPrice: "185.9",
                },
              ],
            },
          ],
        },
      ],
    },
  };
}

describe("archive codec", () => {
  it("writes proto field names and enum names", () => {
    const s = marshalAdmin(adminFixture());
    for (const want of [
      '"format_version":1',
      '"exported_at":"2026-07-30T00:00:00Z"',
      '"kind":"ADMIN"',
      '"asset_class":"STOCK"',
      '"price_date":"2024-01-15"',
    ]) {
      expect(s).toContain(want);
    }
    for (const unwanted of ["formatVersion", "assetClass", "ASSET_CLASS_STOCK", '"asset_class":1']) {
      expect(s).not.toContain(unwanted);
    }
  });

  // A declared zero price is not the same as no price: an option expiring
  // worthless converts at zero, a posting with no price cannot convert at all.
  it("keeps an absent optional apart from a zero one", () => {
    const withZero = userFixture();
    withZero.txs.windows[0].groups[0].postings[0].unitPrice = "0";
    expect(marshalUser(withZero)).toContain('"unit_price":"0"');

    const absent = userFixture();
    delete (absent.txs.windows[0].groups[0].postings[0] as { unitPrice?: string }).unitPrice;
    expect(marshalUser(absent)).not.toContain("unit_price");
  });

  it("round trips an admin archive", () => {
    const got = unmarshalAdmin(marshalAdmin(adminFixture()));
    expect(got.envelope?.formatVersion).toBe(FORMAT_VERSION);
    expect(got.envelope?.kind).toBe(ArchiveKind.ADMIN);
    expect(got.prices?.groups[0].rows[0].close).toBe("185.9");
    expect(got.prices?.groups[0].coverage[0].before).toBe("2024-01-17");
  });

  it("round trips a user archive", () => {
    const got = unmarshalUser(marshalUser(userFixture()));
    expect(got.envelope?.kind).toBe(ArchiveKind.USER);
    expect(got.txs?.windows[0].groups[0].postings[0].quantity).toBe("10");
  });

  // No call site can write a document that misdescribes its own version or kind.
  it("stamps the envelope over whatever the caller supplied", () => {
    const a = adminFixture() as ReturnType<typeof adminFixture> & {
      envelope: { formatVersion?: number; kind?: ArchiveKind };
    };
    a.envelope.formatVersion = 99;
    a.envelope.kind = ArchiveKind.USER;
    const got = unmarshalAdmin(marshalAdmin(a));
    expect(got.envelope?.formatVersion).toBe(FORMAT_VERSION);
    expect(got.envelope?.kind).toBe(ArchiveKind.ADMIN);
  });

  it("refuses an archive from a later PortfolioDB by version, not by parse error", () => {
    const doc = JSON.parse(marshalAdmin(adminFixture()));
    doc.envelope.format_version = 2;
    expect(() => unmarshalAdmin(JSON.stringify(doc))).toThrow(ArchiveVersionError);
  });

  it("refuses a user archive handed to the admin reader", () => {
    expect(() => unmarshalAdmin(marshalUser(userFixture()))).toThrow(ArchiveKindError);
  });

  // What makes the version gate safe to be one-sided.
  it("ignores a field added by a later writer", () => {
    const doc = JSON.parse(marshalAdmin(adminFixture()));
    doc.inflation_indices = [{ series: "CPIH" }];
    doc.envelope.written_by = "a later portfoliodb";
    expect(() => unmarshalAdmin(JSON.stringify(doc))).not.toThrow();
  });

  // Not representable anywhere in PortfolioDB, not merely in this file.
  it("refuses a value outside the vocabulary", () => {
    const doc = JSON.parse(marshalUser(userFixture()));
    doc.txs.windows[0].broker = "REVOLUT";
    expect(() => unmarshalUser(JSON.stringify(doc))).toThrow();
  });

  it("reads the lowerCamelCase spelling a hand-written file may use", () => {
    const doc = JSON.parse(marshalAdmin(adminFixture()));
    doc.envelope = { formatVersion: 1, exportedAt: "2026-07-30T00:00:00Z", kind: "ADMIN" };
    expect(unmarshalAdmin(JSON.stringify(doc)).envelope?.formatVersion).toBe(1);
  });
});

// The point of fixing the options in one place in each language: a document
// written by the Go server and one written by the browser are the same bytes,
// so neither can drift without this failing.
describe("cross-runtime agreement", () => {
  it.each([
    ["../server/archive/testdata/admin.json", () => marshalAdmin(adminFixture())],
    ["../server/archive/testdata/user.json", () => marshalUser(userFixture())],
  ])("matches the Go golden %s", (path, write) => {
    const want = readFileSync(path, "utf8").trimEnd();
    expect(write()).toBe(want);
  });

  it("reads what Go wrote", () => {
    const admin = unmarshalAdmin(readFileSync("../server/archive/testdata/admin.json", "utf8"));
    expect(admin.instruments?.instruments[0].identifiers[0].value).toBe("AAPL");
    const user = unmarshalUser(readFileSync("../server/archive/testdata/user.json", "utf8"));
    expect(user.preferences?.displayCurrency).toBe("GBP");
  });
});
