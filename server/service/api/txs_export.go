package api

import (
	"time"

	archivev1 "github.com/leedenison/portfoliodb/proto/archive/v1"
	"github.com/leedenison/portfoliodb/server/db"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// txWindows turns the flat export postings into archive windows, one per broker,
// each nesting its groups and each group its postings.
//
// A window is a replacement scope, so there is exactly one per broker: two
// windows naming one broker would overlap in time, and an import replaces a
// period rather than appending to it, so the second would delete what the first
// had just written. See docs/adr/0002-transaction-ingestion-model.md.
//
// The requested period, where there is one, is the window's period verbatim: an
// export adheres strictly to the period asked for, so the window says what was
// asked for rather than what happened to land in it, and a group straddling a
// bound contributes only its in-period legs. The exported group then does not
// balance, which the format accepts -- the importer routes the residual.
//
// Without a period, each bound is derived from the postings the window carries --
// the first posting's instant to the day after the last one's -- so the window
// provably contains everything in it and no group straddles its edge. A bound
// given on one side only is honoured on that side and derived on the other.
//
// A broker with no posting in the period gets no window. An export is a picture of
// what is stored, and an empty window is an instruction to clear a period, which
// is not something the export was asked to say.
//
// The rows arrive ordered by broker, then group, then posting, so the whole
// thing is a scan and the output order follows the query's.
func txWindows(rows []db.ExportPosting, periodFrom, periodBefore *timestamppb.Timestamp) []*archivev1.TxWindow {
	var out []*archivev1.TxWindow
	var cur *archivev1.TxWindow
	var curGroup *archivev1.TxGroup
	var curBroker, curGroupID string
	var from, last time.Time

	closeWindow := func() {
		if cur == nil {
			return
		}
		cur.PeriodFrom = periodFrom
		if cur.PeriodFrom == nil {
			cur.PeriodFrom = timestamppb.New(from)
		}
		cur.PeriodBefore = periodBefore
		if cur.PeriodBefore == nil {
			cur.PeriodBefore = timestamppb.New(last.UTC().Truncate(24*time.Hour).AddDate(0, 0, 1))
		}
	}
	for _, r := range rows {
		if cur == nil || r.Broker != curBroker {
			closeWindow()
			cur = &archivev1.TxWindow{
				Broker: db.StrToBroker(r.Broker),
				// The window says where the file came from, and it came from an
				// export. A posting's own source, where one was recorded, is the
				// domain of its BROKER_DESCRIPTION identifier, which is where it
				// was stored and where it travels; the window's is only the
				// fallback for a posting carrying no identifier at all.
				Source: r.Broker + ":archive:export",
			}
			curBroker = r.Broker
			from = r.Timestamp
			curGroup = nil
			out = append(out, cur)
		}
		if curGroup == nil || r.GroupID != curGroupID {
			curGroup = &archivev1.TxGroup{}
			curGroupID = r.GroupID
			cur.Groups = append(cur.Groups, curGroup)
		}
		last = r.Timestamp
		curGroup.Postings = append(curGroup.Postings, posting(r))
	}
	closeWindow()
	return out
}

// posting converts one export row to its archive form.
//
// Every optional field is written only where the column held something: an
// absent unit price is not a price of zero, and an absent share count basis is
// the posting's own timestamp date rather than an unstated one.
func posting(r db.ExportPosting) *archivev1.Posting {
	p := &archivev1.Posting{
		Timestamp:             timestamppb.New(r.Timestamp),
		Account:               r.Account,
		AccountType:           db.StrToAccountType(r.AccountType),
		Type:                  db.StrToTxType(r.TxType),
		InstrumentDescription: r.Description,
		Quantity:              r.Quantity.String(),
	}
	// A posting whose instrument never resolved has no identifier to name, and
	// resolves from its description on the way back in.
	if r.IdentifierType != "" {
		p.IdentifierHints = []*archivev1.InstrumentRef{{
			Type:   identifierTypeFromString(r.IdentifierType),
			Value:  r.IdentifierValue,
			Domain: r.IdentifierDomain,
		}}
	}
	if r.UnitPrice != nil {
		p.UnitPrice = proto.String(r.UnitPrice.String())
	}
	if r.TradingCurrency != "" {
		p.TradingCurrency = proto.String(r.TradingCurrency)
	}
	if r.SettlementCurrency != "" {
		p.SettlementCurrency = proto.String(r.SettlementCurrency)
	}
	if r.BrokerRef != "" {
		p.BrokerRef = proto.String(r.BrokerRef)
	}
	if r.CounterpartyAccount != "" {
		p.CounterpartyAccount = proto.String(r.CounterpartyAccount)
	}
	if r.ShareCountBasis != nil {
		p.ShareCountBasis = proto.String(r.ShareCountBasis.Format("2006-01-02"))
	}
	return p
}
