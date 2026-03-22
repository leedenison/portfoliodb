package ingestion

import (
	apiv1 "github.com/leedenison/portfoliodb/proto/api/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// JobRequest is a unit of work for the ingestion worker.
type JobRequest struct {
	JobID      string
	UserID     string
	Broker     string
	Source     string
	Bulk       bool
	PeriodFrom *timestamppb.Timestamp
	PeriodTo   *timestamppb.Timestamp
	Txs        []*apiv1.Tx
}
