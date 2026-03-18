import { describe, expect, it } from "vitest";
import { create } from "@bufbuild/protobuf";
import {
  IdentifierType,
  InstrumentIdentifierSchema,
  InstrumentSchema,
} from "@/gen/api/v1/api_pb";
import type { Instrument } from "@/gen/api/v1/api_pb";
import { instrumentsToJson, jsonToInstruments } from "./instruments";

function makeInstrument(
  id: string,
  fields: Partial<{
    assetClass: string;
    exchange: string;
    currency: string;
    name: string;
  }>,
  identifiers: Array<{ type: IdentifierType; value: string; canonical: boolean; domain?: string }>
): Instrument {
  return create(InstrumentSchema, {
    id,
    assetClass: fields.assetClass ?? "",
    exchange: fields.exchange ?? "",
    currency: fields.currency ?? "",
    name: fields.name ?? "",
    identifiers: identifiers.map((i) =>
      create(InstrumentIdentifierSchema, {
        type: i.type,
        value: i.value,
        canonical: i.canonical,
        domain: i.domain,
      })
    ),
  });
}

describe("instrumentsToJson", () => {
  it("produces a JSON array", () => {
    const out = JSON.parse(instrumentsToJson([]));
    expect(Array.isArray(out)).toBe(true);
  });

  it("does not include instrument id", () => {
    const inst = makeInstrument("server-uuid", { assetClass: "STOCK" }, []);
    const out = JSON.parse(instrumentsToJson([inst]));
    expect(out[0]).not.toHaveProperty("id");
  });

  it("emits identifiers array with type name strings", () => {
    const inst = makeInstrument(
      "a1",
      { assetClass: "STOCK", exchange: "XNAS", currency: "USD", name: "Apple" },
      [
        { type: IdentifierType.ISIN, value: "US0378331005", canonical: true },
        { type: IdentifierType.TICKER, value: "AAPL", canonical: true, domain: "XNAS" },
      ]
    );
    const out = JSON.parse(instrumentsToJson([inst]));
    expect(out).toHaveLength(1);
    expect(out[0].identifiers[0].type).toBe("ISIN");
    expect(out[0].identifiers[1].type).toBe("TICKER");
    expect(out[0].identifiers[1].domain).toBe("XNAS");
  });

  it("emits underlying_index for derivatives, underlying appears before derivative", () => {
    const stock = makeInstrument("u1", { assetClass: "STOCK" }, [
      { type: IdentifierType.TICKER, value: "AAPL", canonical: true },
    ]);
    const option = create(InstrumentSchema, {
      id: "o1",
      assetClass: "OPTION",
      identifiers: [
        create(InstrumentIdentifierSchema, { type: IdentifierType.OCC, value: "AAPL240119C00180000", canonical: true }),
      ],
      underlying: stock,
    });
    const out = JSON.parse(instrumentsToJson([option]));
    // underlying emitted first
    expect(out[0].asset_class).toBe("STOCK");
    expect(out[1].asset_class).toBe("OPTION");
    expect(out[1].underlying_index).toBe(0);
  });

  it("deduplicates shared underlying", () => {
    const stock = makeInstrument("u1", { assetClass: "STOCK" }, []);
    const opt1 = create(InstrumentSchema, { id: "o1", assetClass: "OPTION", identifiers: [], underlying: stock });
    const opt2 = create(InstrumentSchema, { id: "o2", assetClass: "OPTION", identifiers: [], underlying: stock });
    const out = JSON.parse(instrumentsToJson([opt1, opt2]));
    // stock appears once
    expect(out.filter((x: { asset_class: string }) => x.asset_class === "STOCK")).toHaveLength(1);
    expect(out[1].underlying_index).toBe(0);
    expect(out[2].underlying_index).toBe(0);
  });
});

describe("jsonToInstruments", () => {
  it("returns error for invalid JSON", () => {
    const { errors } = jsonToInstruments("not json");
    expect(errors[0].field).toBe("file");
  });

  it("returns error when root is not an array", () => {
    const { errors } = jsonToInstruments("{}");
    expect(errors[0].field).toBe("file");
  });

  it("returns error for unknown identifier type", () => {
    const { errors } = jsonToInstruments(JSON.stringify([
      { asset_class: "STOCK", exchange: "", currency: "", name: "", identifiers: [{ type: "BOGUS", value: "x", canonical: true }] },
    ]));
    expect(errors[0].field).toContain("type");
  });

  it("parses a single instrument", () => {
    const { instruments, errors } = jsonToInstruments(JSON.stringify([
      {
        asset_class: "STOCK",
        exchange: "XNAS",
        currency: "USD",
        name: "Apple",
        identifiers: [
          { type: "ISIN", value: "US0378331005", canonical: true },
          { type: "TICKER", value: "AAPL", canonical: true, domain: "XNAS" },
        ],
      },
    ]));
    expect(errors).toHaveLength(0);
    expect(instruments).toHaveLength(1);
    expect(instruments[0].exchange).toBe("XNAS");
    expect(instruments[0].identifiers[1].domain).toBe("XNAS");
  });

  it("resolves underlying_index", () => {
    const { instruments, errors } = jsonToInstruments(JSON.stringify([
      { asset_class: "STOCK", exchange: "XNAS", currency: "USD", name: "Apple", identifiers: [] },
      { asset_class: "OPTION", exchange: "XCBO", currency: "USD", name: "Call", underlying_index: 0, identifiers: [] },
    ]));
    expect(errors).toHaveLength(0);
    expect(instruments[1].underlying?.assetClass).toBe("STOCK");
  });

  it("reports error for out-of-range underlying_index", () => {
    const { errors } = jsonToInstruments(JSON.stringify([
      { asset_class: "OPTION", exchange: "", currency: "", name: "", underlying_index: 99, identifiers: [] },
    ]));
    expect(errors[0].field).toBe("underlying_index");
  });

  it("round-trips instruments through JSON", () => {
    const inst = makeInstrument(
      "id1",
      { assetClass: "ETF", exchange: "ARCX", currency: "USD", name: "SPDR S&P 500" },
      [
        { type: IdentifierType.ISIN, value: "US78462F1030", canonical: true },
        { type: IdentifierType.TICKER, value: "SPY", canonical: true, domain: "ARCX" },
      ]
    );
    const { instruments, errors } = jsonToInstruments(instrumentsToJson([inst]));
    expect(errors).toHaveLength(0);
    const out = instruments[0];
    expect(out.assetClass).toBe("ETF");
    expect(out.name).toBe("SPDR S&P 500");
    expect(out.identifiers[0].type).toBe(IdentifierType.ISIN);
    expect(out.identifiers[1].domain).toBe("ARCX");
  });
});
