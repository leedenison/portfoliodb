package ingestion

import (
	"context"

	apiv1 "github.com/leedenison/portfoliodb/proto/api/v1"
	archivev1 "github.com/leedenison/portfoliodb/proto/archive/v1"
	"github.com/leedenison/portfoliodb/server/db"
)

// progressFlushEvery is how many rows a part advances before its counter is
// written. A six-figure price import that wrote its progress a row at a time
// would spend more time reporting than importing, and nobody watching a
// progress bar can tell a hundred rows apart.
const progressFlushEvery = 100

// partReporter records one archive part's progress and problems. It batches the
// progress counter and accumulates validation errors so that a part costs a
// handful of writes rather than one per row.
//
// A reporter whose part is ARCHIVE_PART_UNSPECIFIED reports against the job
// itself. That is the shape the per-entity price and corporate event imports
// still have -- one job, one part, no part rows -- and it goes when those RPCs do.
type partReporter struct {
	db      db.DB
	jobID   string
	part    archivev1.ArchivePart
	errs    []*apiv1.ValidationError
	pending int32
}

func newPartReporter(database db.DB, jobID string, part archivev1.ArchivePart) *partReporter {
	return &partReporter{db: database, jobID: jobID, part: part}
}

// scoped reports whether this reporter writes to a part row rather than to the
// job.
func (r *partReporter) scoped() bool {
	return r.part != archivev1.ArchivePart_ARCHIVE_PART_UNSPECIFIED
}

// Total records how much work the part has, which is what turns a spinner into
// a proportion.
func (r *partReporter) Total(ctx context.Context, n int) {
	if r.scoped() {
		_ = r.db.SetJobPartTotalCount(ctx, r.jobID, r.part, int32(n))
		return
	}
	_ = r.db.SetJobTotalCount(ctx, r.jobID, int32(n))
}

// Advance moves the part's progress on by n rows, writing through once enough
// have accumulated.
func (r *partReporter) Advance(ctx context.Context, n int) {
	if !r.scoped() {
		for i := 0; i < n; i++ {
			_ = r.db.IncrJobProcessedCount(ctx, r.jobID)
		}
		return
	}
	r.pending += int32(n)
	if r.pending >= progressFlushEvery {
		r.flushProgress(ctx)
	}
}

func (r *partReporter) flushProgress(ctx context.Context) {
	if r.pending == 0 {
		return
	}
	_ = r.db.AddJobPartProcessedCount(ctx, r.jobID, r.part, r.pending)
	r.pending = 0
}

// Err records one row-level problem. A part with errors has still succeeded --
// what failed is a row, and the count is what lets a result read "completed, 12
// rows rejected".
func (r *partReporter) Err(e *apiv1.ValidationError) {
	if e != nil {
		r.errs = append(r.errs, e)
	}
}

// Errf records a problem against a row and a field.
func (r *partReporter) Errf(rowIndex int, field, message string) {
	r.Err(&apiv1.ValidationError{RowIndex: int32(rowIndex), Field: field, Message: message})
}

// Errs records several problems at once.
func (r *partReporter) Errs(errs []*apiv1.ValidationError) {
	r.errs = append(r.errs, errs...)
}

// ErrCount is how many row-level problems have been recorded.
func (r *partReporter) ErrCount() int {
	return len(r.errs)
}

// Flush writes the outstanding progress and validation errors. It is safe to
// call more than once and must be called before the part is marked terminal,
// including on the failure path: errors gathered before a hard failure explain
// it and are dropped if they are never written.
func (r *partReporter) Flush(ctx context.Context) {
	r.flushProgress(ctx)
	if len(r.errs) == 0 {
		return
	}
	_ = r.db.AppendValidationErrors(ctx, r.jobID, r.part, r.errs)
	r.errs = nil
}
