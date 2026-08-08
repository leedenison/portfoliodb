// Package archiveimport applies the parts of a system archive. It sits below
// both the API and the ingestion worker: an import runs in the worker, but the
// synchronous per-entity RPCs still apply the same parts, and neither package
// can import the other.
package archiveimport

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
type PartReporter struct {
	db      db.DB
	jobID   string
	part    archivev1.ArchivePart
	errs    []*apiv1.ValidationError
	pending int32
}

func NewPartReporter(database db.DB, jobID string, part archivev1.ArchivePart) *PartReporter {
	return &PartReporter{db: database, jobID: jobID, part: part}
}

// NewDetachedReporter makes a reporter that records problems without writing
// them anywhere. It is what a synchronous RPC uses: there is no job to report
// against, and the caller reads the errors off the reporter instead.
func NewDetachedReporter() *PartReporter {
	return &PartReporter{}
}

// Errors returns the problems recorded so far, for a caller that reports them
// in its own response rather than against a job.
func (r *PartReporter) Errors() []*apiv1.ValidationError {
	return r.errs
}

// scoped reports whether this reporter writes to a part row rather than to the
// job.
func (r *PartReporter) scoped() bool {
	return r.db != nil && r.part != archivev1.ArchivePart_ARCHIVE_PART_UNSPECIFIED
}

// Total records how much work the part has, which is what turns a spinner into
// a proportion.
func (r *PartReporter) Total(ctx context.Context, n int) {
	if r.db == nil {
		return
	}
	if r.scoped() {
		_ = r.db.SetJobPartTotalCount(ctx, r.jobID, r.part, int32(n))
		return
	}
	_ = r.db.SetJobTotalCount(ctx, r.jobID, int32(n))
}

// Advance moves the part's progress on by n rows, writing through once enough
// have accumulated.
func (r *PartReporter) Advance(ctx context.Context, n int) {
	if r.db == nil {
		return
	}
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

func (r *PartReporter) flushProgress(ctx context.Context) {
	if r.pending == 0 || r.db == nil {
		return
	}
	_ = r.db.AddJobPartProcessedCount(ctx, r.jobID, r.part, r.pending)
	r.pending = 0
}

// Err records one row-level problem. A part with errors has still succeeded --
// what failed is a row, and the count is what lets a result read "completed, 12
// rows rejected".
func (r *PartReporter) Err(e *apiv1.ValidationError) {
	if e != nil {
		r.errs = append(r.errs, e)
	}
}

// Errf records a problem against a row and a field.
func (r *PartReporter) Errf(rowIndex int, field, message string) {
	r.Err(&apiv1.ValidationError{RowIndex: int32(rowIndex), Field: field, Message: message})
}

// Errs records several problems at once.
func (r *PartReporter) Errs(errs []*apiv1.ValidationError) {
	r.errs = append(r.errs, errs...)
}

// ErrCount is how many row-level problems have been recorded.
func (r *PartReporter) ErrCount() int {
	return len(r.errs)
}

// Flush writes the outstanding progress and validation errors. It is safe to
// call more than once and must be called before the part is marked terminal,
// including on the failure path: errors gathered before a hard failure explain
// it and are dropped if they are never written.
func (r *PartReporter) Flush(ctx context.Context) {
	r.flushProgress(ctx)
	if len(r.errs) == 0 || r.db == nil {
		return
	}
	_ = r.db.AppendValidationErrors(ctx, r.jobID, r.part, r.errs)
	r.errs = nil
}
