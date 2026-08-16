import { describe, expect, it } from "vitest";
import { create } from "@bufbuild/protobuf";
import { PortfolioTxSchema, TxSchema } from "@/gen/api/v1/api_pb";
import { AccountType, TxType, TxTypeSchema } from "@/gen/type/v1/type_pb";
import { groupTxs } from "./tx-groups";

function posting(
  groupId: string,
  resolvedTxType: TxType,
  quantity: string,
  opts?: { accountType?: AccountType; description?: string; syntheticPurpose?: string }
) {
  return create(PortfolioTxSchema, {
    tx: create(TxSchema, {
      groupId,
      resolvedTxType,
      quantity,
      accountType: opts?.accountType ?? AccountType.USER,
      instrumentDescription: opts?.description ?? "",
      syntheticPurpose: opts?.syntheticPurpose ?? "",
    }),
  });
}

/** A buy as it is stored: the asset, the cash it cost, the commission and its counter leg. */
const buy = [
  posting("g1", TxType.TRADE_ASSET, "100", { description: "AAPL" }),
  posting("g1", TxType.TRADE_CASH, "-15000", { description: "USD" }),
  posting("g1", TxType.TRANSACTION_COST, "-5", { description: "USD" }),
  posting("g1", TxType.TRANSACTION_COST, "5", {
    accountType: AccountType.EXPENSE,
    description: "USD",
    syntheticPurpose: "BOUNDARY",
  }),
];

describe("groupTxs", () => {
  it("returns one group per group id, in the order the page supplied", () => {
    const groups = groupTxs([
      posting("g1", TxType.TRADE_ASSET, "1"),
      posting("g2", TxType.DIVIDEND, "1"),
      posting("g1", TxType.TRADE_CASH, "-1"),
    ]);
    expect(groups.map((g) => g.id)).toEqual(["g1", "g2"]);
    expect(groups[0].rest).toHaveLength(1);
  });

  it("shows a trade as its asset leg, not its cash", () => {
    const [group] = groupTxs(buy);
    expect(group.principal.tx?.resolvedTxType).toBe(TxType.TRADE_ASSET);
    // The cash and the commission, but not the expense leg derived from the
    // commission.
    expect(group.rest).toHaveLength(2);
  });

  it("does not repeat the principal among the rest", () => {
    const [group] = groupTxs(buy);
    expect(group.rest).not.toContain(group.principal);
  });

  it("shows a dividend as the cash it credited, not the income it came from", () => {
    const [group] = groupTxs([
      posting("g1", TxType.DIVIDEND, "-42", {
        accountType: AccountType.INCOME,
        syntheticPurpose: "BOUNDARY",
      }),
      posting("g1", TxType.DIVIDEND, "42"),
    ]);
    expect(group.principal.tx?.accountType).toBe(AccountType.USER);
    // The income leg restates the credit, so the dividend has nothing to expand to.
    expect(group.rest).toHaveLength(0);
  });

  it("shows a fee as the charge, not its expense counter leg", () => {
    const [group] = groupTxs([
      posting("g1", TxType.HOLDING_COST, "10", {
        accountType: AccountType.EXPENSE,
        syntheticPurpose: "BOUNDARY",
      }),
      posting("g1", TxType.HOLDING_COST, "-10"),
    ]);
    expect(group.principal.tx?.accountType).toBe(AccountType.USER);
    expect(group.rest).toHaveLength(0);
  });

  // An opening balance is a pad and the equity half that makes it balance, both
  // carrying the INITIALIZE purpose. The equity half is the pad negated.
  it("shows an opening balance without its equity half", () => {
    const [group] = groupTxs([
      posting("g1", TxType.TRANSFER, "100", { syntheticPurpose: "INITIALIZE" }),
      posting("g1", TxType.TRANSFER, "-100", {
        accountType: AccountType.EQUITY,
        syntheticPurpose: "INITIALIZE",
      }),
    ]);
    expect(group.principal.tx?.accountType).toBe(AccountType.USER);
    expect(group.rest).toHaveLength(0);
  });

  // A residual is what the group failed to balance to, which the source did not
  // supply and the principal cannot imply.
  it("keeps the residuals a group did not balance to", () => {
    const [group] = groupTxs([
      posting("g1", TxType.TRADE_ASSET, "10"),
      posting("g1", TxType.AMBIGUOUS, "-1900", {
        accountType: AccountType.IMBALANCE,
        syntheticPurpose: "RESIDUAL",
      }),
    ]);
    expect(group.rest).toHaveLength(1);
    expect(group.rest[0].tx?.accountType).toBe(AccountType.IMBALANCE);
  });

  // A routed leg is what the event failed to account for, so it reads after the
  // legs the source stated however the page happened to order them.
  it("puts routed legs after stated ones", () => {
    const [group] = groupTxs([
      posting("g1", TxType.TRADE_ASSET, "10"),
      posting("g1", TxType.AMBIGUOUS, "-1900", {
        accountType: AccountType.IMBALANCE,
        syntheticPurpose: "RESIDUAL",
        description: "routed",
      }),
      posting("g1", TxType.TRADE_CASH, "-100", { description: "stated" }),
    ]);
    expect(group.rest.map((p) => p.tx?.instrumentDescription)).toEqual([
      "stated",
      "routed",
    ]);
  });

  // Page order is the last resort rather than the rule: it decides only between
  // legs the composite cannot separate, which is two of one kind and one amount.
  it("keeps the page's order between legs it cannot otherwise separate", () => {
    const [group] = groupTxs([
      posting("g1", TxType.TRADE_ASSET, "10"),
      posting("g1", TxType.TRANSACTION_COST, "-7.50", { description: "fee first" }),
      posting("g1", TxType.TRADE_CASH, "-100", { description: "cash" }),
      posting("g1", TxType.TRANSACTION_COST, "-7.50", { description: "fee second" }),
    ]);
    expect(group.rest.map((p) => p.tx?.instrumentDescription)).toEqual([
      "cash",
      "fee first",
      "fee second",
    ]);
  });

  // The reason for a second rank table. PRINCIPAL_RANK puts cash below charges,
  // because a trade is its asset leg and the money says least about the event.
  // Reading the legs asks the opposite question, and reusing that table would
  // print a trade's fees above the cash that paid for it.
  it("reads a trade's cash before the charges levied on it", () => {
    const [group] = groupTxs([
      posting("g1", TxType.TRADE_ASSET, "28", { description: "EQQQ" }),
      posting("g1", TxType.TRANSACTION_COST, "-1.50", { description: "PTM levy" }),
      posting("g1", TxType.TRANSACTION_COST, "-7.50", { description: "dealing fee" }),
      posting("g1", TxType.TRADE_CASH, "-7380.19", { description: "GBP" }),
    ]);
    expect(group.principal.tx?.instrumentDescription).toBe("EQQQ");
    expect(group.rest.map((p) => p.tx?.instrumentDescription)).toEqual([
      "GBP",
      "dealing fee",
      "PTM levy",
    ]);
  });

  // The order is a property of the group, not of the page. Any arrival order of
  // one group's legs reads out the same way.
  it("orders the same legs identically however the page supplied them", () => {
    const legs = [
      posting("g1", TxType.TRADE_ASSET, "28", { description: "EQQQ" }),
      posting("g1", TxType.TRADE_CASH, "-7380.19", { description: "GBP" }),
      posting("g1", TxType.TRANSACTION_COST, "-7.50", { description: "dealing fee" }),
      posting("g1", TxType.TRANSACTION_COST, "-1.50", { description: "PTM levy" }),
      posting("g1", TxType.AMBIGUOUS, "-0.05", {
        accountType: AccountType.SOURCE_ROUNDING,
        syntheticPurpose: "RESIDUAL",
        description: "rounding",
      }),
    ];
    const want = ["GBP", "dealing fee", "PTM levy", "rounding"];
    // Every rotation, which is enough to move each leg through every position.
    for (let i = 0; i < legs.length; i++) {
      const rotated = [...legs.slice(i), ...legs.slice(0, i)];
      const [group] = groupTxs(rotated);
      expect(group.rest.map((p) => p.tx?.instrumentDescription)).toEqual(want);
    }
  });

  // A charge is still read after a transfer, so a deposit run that cost a fee
  // reads as the movement then what it cost.
  it("reads what an event was levied after what constituted it", () => {
    const [group] = groupTxs([
      posting("g1", TxType.TRANSFER, "5000", { description: "in" }),
      posting("g1", TxType.HOLDING_COST, "-3.24", { description: "platform fee" }),
      posting("g1", TxType.TRANSFER, "-5000", { description: "out" }),
    ]);
    expect(group.rest.map((p) => p.tx?.instrumentDescription)).toEqual([
      "out",
      "platform fee",
    ]);
  });

  it("keeps a leg the source stated", () => {
    const [group] = groupTxs([
      posting("g1", TxType.TRADE_ASSET, "10"),
      posting("g1", TxType.TRADE_CASH, "-1900"),
    ]);
    expect(group.rest).toHaveLength(1);
  });

  // A group whose legs the server routed to a residual has an IMBALANCE leg and
  // no other counterparty; the user's own posting is still the event.
  it("prefers a user posting to a residual", () => {
    const [group] = groupTxs([
      posting("g1", TxType.AMBIGUOUS, "-500", {
        accountType: AccountType.IMBALANCE,
        syntheticPurpose: "RESIDUAL",
      }),
      posting("g1", TxType.TRANSFER_EXTERNAL, "500"),
    ]);
    expect(group.principal.tx?.resolvedTxType).toBe(TxType.TRANSFER_EXTERNAL);
  });

  it("separates a trade from its commission by amount when the types tie", () => {
    const [group] = groupTxs([
      posting("g1", TxType.TRANSACTION_COST, "-5"),
      posting("g1", TxType.TRANSACTION_COST, "-4995"),
    ]);
    expect(group.principal.tx?.quantity).toBe("-4995");
  });

  // parseFloat would answer this one wrong: the two magnitudes agree to more
  // digits than a float64 keeps.
  it("compares tied amounts exactly", () => {
    const [group] = groupTxs([
      posting("g1", TxType.TRADE_CASH, "-1000000000000000000.0000000001"),
      posting("g1", TxType.TRADE_CASH, "1000000000000000000.0000000002"),
    ]);
    expect(group.principal.tx?.quantity).toBe("1000000000000000000.0000000002");
  });

  it("falls back to the counter legs when a group has no user posting", () => {
    const [group] = groupTxs([
      posting("g1", TxType.DIVIDEND, "-42", {
        accountType: AccountType.INCOME,
        syntheticPurpose: "BOUNDARY",
      }),
    ]);
    expect(group.principal.tx?.resolvedTxType).toBe(TxType.DIVIDEND);
    expect(group.rest).toHaveLength(0);
  });

  it("ranks every declared type, so no value falls through to the floor", () => {
    for (const v of TxTypeSchema.values) {
      const [group] = groupTxs([
        posting("g1", v.number as TxType, "1"),
        posting("g1", TxType.TX_TYPE_UNSPECIFIED, "1000"),
      ]);
      if (v.number === TxType.TX_TYPE_UNSPECIFIED) continue;
      expect(
        group.principal.tx?.resolvedTxType,
        `${v.name} does not outrank an unset type`
      ).toBe(v.number);
    }
  });

  it("keeps a page with no transactions empty", () => {
    expect(groupTxs([])).toEqual([]);
  });
});
