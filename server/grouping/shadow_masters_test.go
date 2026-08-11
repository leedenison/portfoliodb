//go:build shadow

// Package-level shadow run against the sample exports.
//
// Build-tagged because the exports it reads are gitignored: they are real broker
// data and belong in local/ rather than in the repository. Run it with
//
//	go test -tags shadow ./server/grouping/ -run TestShadow -v
//
// after the extraction harness has written local/shadow/*.json.
package grouping

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	typev1 "github.com/leedenison/portfoliodb/proto/type/v1"
	"github.com/leedenison/portfoliodb/server/db"
	"github.com/leedenison/portfoliodb/server/txtype"
	"github.com/shopspring/decimal"
)

type shadowCorrelation struct {
	Label       string   `json:"label"`
	Token       string   `json:"token"`
	Ordinal     *int64   `json:"ordinal"`
	OrdinalSpan *int64   `json:"ordinal_span"`
	Scope       string   `json:"scope"`
	Match       []string `json:"match"`
}

type shadowPosting struct {
	ID               string              `json:"id"`
	Account          string              `json:"account"`
	Timestamp        string              `json:"timestamp"`
	Quantity         string              `json:"quantity"`
	UnitPrice        *string             `json:"unit_price"`
	SettlementAmount *string             `json:"settlement_amount"`
	BrokerTxType     []int               `json:"broker_tx_type"`
	IsUser           bool                `json:"is_user"`
	GroupRef         string              `json:"group_ref"`
	Correlations     []shadowCorrelation `json:"correlations"`
}

// toDomain converts one extracted posting into what the engine reads.
//
// group_ref becomes the stored group, which is what makes this a shadow run: the
// converters' partition is the thing being reproduced. A posting the converter left
// unnamed is its own group, as it would be once stored.
func (p shadowPosting) toDomain(i int) db.GroupingPosting {
	ts, _ := time.Parse(time.RFC3339, p.Timestamp)
	declared := make([]typev1.TxType, 0, len(p.BrokerTxType))
	for _, t := range p.BrokerTxType {
		declared = append(declared, typev1.TxType(t))
	}
	group := p.GroupRef
	if group == "" {
		group = fmt.Sprintf("solo-%d", i)
	}
	out := db.GroupingPosting{
		ID:        p.ID,
		UserID:    "shadow",
		Broker:    typev1.Broker_FIDELITY,
		Account:   p.Account,
		Timestamp: ts,
		Quantity:  decimal.RequireFromString(p.Quantity),
		Declared:  declared,
		JobID:     "shadow-job",
		GroupID:   group,
	}
	if p.UnitPrice != nil {
		d := decimal.RequireFromString(*p.UnitPrice)
		out.UnitPrice = &d
	}
	if p.SettlementAmount != nil {
		d := decimal.RequireFromString(*p.SettlementAmount)
		out.SettlementAmount = &d
	}
	for _, c := range p.Correlations {
		out.Correlations = append(out.Correlations, db.Correlation{
			Label:       c.Label,
			Token:       c.Token,
			Ordinal:     c.Ordinal,
			OrdinalSpan: c.OrdinalSpan,
			Scope:       c.Scope,
			Match:       c.Match,
		})
	}
	return out
}

func TestShadowAgainstMasters(t *testing.T) {
	dir := filepath.Join("..", "..", "local", "shadow")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Skipf("no %s: run the extraction harness first", dir)
	}
	for _, e := range entries {
		if filepath.Ext(e.Name()) != ".json" {
			continue
		}
		t.Run(e.Name(), func(t *testing.T) {
			raw, err := os.ReadFile(filepath.Join(dir, e.Name()))
			if err != nil {
				t.Fatalf("read: %v", err)
			}
			var rows []shadowPosting
			if err := json.Unmarshal(raw, &rows); err != nil {
				t.Fatalf("decode: %v", err)
			}
			// Only the transcribed postings are partitioned. A derived counter-leg
			// and a routed residual carry no evidence and are re-derived after.
			var ps []db.GroupingPosting
			for i, r := range rows {
				if !r.IsUser {
					continue
				}
				ps = append(ps, r.toDomain(i))
			}

			gs := Partition(ps, DefaultRules(), DefaultOpts())
			d := Compare(ps, gs)
			t.Logf("postings=%d derived=%d stored=%d agreed=%d joined=%d split=%d",
				len(ps), d.Groups, d.Stored, d.Agreed, d.Joined, d.Split)
			t.Logf("  %s", breakdown(ps, gs))
			for _, ex := range d.Examples {
				t.Logf("  %s", ex)
			}
			if !d.Agrees() {
				t.Errorf("engine did not reproduce the converter's partition")
			}
		})
	}
}

// breakdown counts which kind of event the engine decided each multi-posting group
// is, so the run can be checked against the counts the converter's rules are pinned
// to: sells 91/91, buys 78/78, and 21 deposit runs across both masters.
func breakdown(ps []db.GroupingPosting, gs []Group) string {
	byID := map[string]db.GroupingPosting{}
	for _, p := range ps {
		byID[p.ID] = p
	}
	var sells, buys, runs, cash, reinvest, other int
	for _, g := range gs {
		if len(g.Members) < 2 {
			continue
		}
		var transfer, asset, sold, income bool
		cashLegs := 0
		for _, m := range g.Members {
			switch {
			case m.Resolved == typev1.TxType_TRANSFER:
				transfer = true
			case m.Resolved == typev1.TxType_TRADE_ASSET:
				asset = true
				sold = byID[m.ID].Quantity.IsNegative()
			case m.Resolved == typev1.TxType_TRADE_CASH:
				cashLegs++
			case txtype.ResolvedMustBe(m.Resolved, typev1.TxType_INCOME):
				income = true
			}
		}
		switch {
		case transfer:
			runs++
		case asset && income:
			// A reinvestment is a compressed two-event group -- the units bought
			// plus the dividend that paid for them -- rather than a purchase.
			reinvest++
		case asset && sold:
			sells++
		case asset:
			buys++
		case cashLegs == 2:
			cash++
		default:
			other++
		}
	}
	return fmt.Sprintf("groups>1: security sells=%d buys=%d cash-for-cash=%d deposit runs=%d reinvestments=%d other=%d",
		sells, buys, cash, runs, reinvest, other)
}
