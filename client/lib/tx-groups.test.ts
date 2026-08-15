import { describe, expect, it } from "vitest";
import { create } from "@bufbuild/protobuf";
import { PortfolioTxSchema, TxSchema } from "@/gen/api/v1/api_pb";
import { AccountType, TxType, TxTypeSchema } from "@/gen/type/v1/type_pb";
import { groupTxs } from "./tx-groups";

function posting(
  groupId: string,
  resolvedTxType: TxType,
  quantity: string,
  opts?: { accountType?: AccountType; description?: string }
) {
  return create(PortfolioTxSchema, {
    tx: create(TxSchema, {
      groupId,
      resolvedTxType,
      quantity,
      accountType: opts?.accountType ?? AccountType.USER,
      instrumentDescription: opts?.description ?? "",
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
    expect(group.rest).toHaveLength(3);
  });

  it("does not repeat the principal among the rest", () => {
    const [group] = groupTxs(buy);
    expect(group.rest).not.toContain(group.principal);
  });

  it("shows a dividend as the cash it credited, not the income it came from", () => {
    const [group] = groupTxs([
      posting("g1", TxType.DIVIDEND, "-42", { accountType: AccountType.INCOME }),
      posting("g1", TxType.DIVIDEND, "42"),
    ]);
    expect(group.principal.tx?.accountType).toBe(AccountType.USER);
  });

  it("shows a fee as the charge, not its expense counter leg", () => {
    const [group] = groupTxs([
      posting("g1", TxType.HOLDING_COST, "10", { accountType: AccountType.EXPENSE }),
      posting("g1", TxType.HOLDING_COST, "-10"),
    ]);
    expect(group.principal.tx?.accountType).toBe(AccountType.USER);
  });

  // A group whose legs the server routed to a residual has an IMBALANCE leg and
  // no other counterparty; the user's own posting is still the event.
  it("prefers a user posting to a residual", () => {
    const [group] = groupTxs([
      posting("g1", TxType.AMBIGUOUS, "-500", { accountType: AccountType.IMBALANCE }),
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
      posting("g1", TxType.DIVIDEND, "-42", { accountType: AccountType.INCOME }),
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
