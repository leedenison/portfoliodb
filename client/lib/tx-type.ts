import { AccountType, TxType } from "@/gen/api/v1/api_pb";

/** Human-readable labels for each tx type. */
export const TX_TYPE_LABEL: Record<number, string> = {
  [TxType.BUYDEBT]: "Buy Debt",
  [TxType.BUYMF]: "Buy MF",
  [TxType.BUYOPT]: "Buy Option",
  [TxType.BUYOTHER]: "Buy Other",
  [TxType.BUYSTOCK]: "Buy Stock",
  [TxType.SELLDEBT]: "Sell Debt",
  [TxType.SELLMF]: "Sell MF",
  [TxType.SELLOPT]: "Sell Option",
  [TxType.SELLOTHER]: "Sell Other",
  [TxType.SELLSTOCK]: "Sell Stock",
  [TxType.INCOME]: "Income",
  [TxType.INVEXPENSE]: "Expense",
  [TxType.REINVEST]: "Reinvest",
  [TxType.RETOFCAP]: "Return of Capital",
  [TxType.SPLIT]: "Split",
  [TxType.TRANSFER]: "Transfer",
  [TxType.JRNLFUND]: "Journal Fund",
  [TxType.JRNLSEC]: "Journal Security",
  [TxType.MARGININTEREST]: "Margin Interest",
  [TxType.CLOSUREOPT]: "Option Closure",
  [TxType.CASHFLOW]: "Cash Flow",
  [TxType.BUYFUTURE]: "Buy Future",
  [TxType.SELLFUTURE]: "Sell Future",
};

// Labels for the non-asset side of an event. USER postings are the ordinary case and
// carry no badge; the rest are shown so that a group's legs can be told apart, which
// is also how an imbalance becomes visible rather than silently absorbed.
export const ACCOUNT_TYPE_LABEL: Record<number, string> = {
  [AccountType.EQUITY]: "Equity",
  [AccountType.INCOME]: "Income",
  [AccountType.EXPENSE]: "Expense",
  [AccountType.IMBALANCE]: "Imbalance",
  [AccountType.TRANSFER_CLEARING]: "In Flight",
};
