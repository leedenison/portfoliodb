package postgres

import (
	"context"
	"database/sql"
	"fmt"
	"sort"

	"github.com/google/uuid"
	apiv1 "github.com/leedenison/portfoliodb/proto/api/v1"
	archivev1 "github.com/leedenison/portfoliodb/proto/archive/v1"
	"github.com/leedenison/portfoliodb/server/db"
)

// CreateJob implements db.JobDB.
func (p *Postgres) CreateJob(ctx context.Context, params db.CreateJobParams) (string, error) {
	userUUID, err := uuid.Parse(params.UserID)
	if err != nil {
		return "", fmt.Errorf("invalid user id: %w", err)
	}
	var fromT, beforeT interface{}
	if params.PeriodFrom != nil && params.PeriodFrom.IsValid() {
		fromT = params.PeriodFrom.AsTime()
	}
	if params.PeriodBefore != nil && params.PeriodBefore.IsValid() {
		beforeT = params.PeriodBefore.AsTime()
	}
	var filenameVal, brokerVal, sourceVal interface{}
	if params.Filename != "" {
		filenameVal = params.Filename
	}
	if params.Broker != "" {
		brokerVal = params.Broker
	}
	if params.Source != "" {
		sourceVal = params.Source
	}
	var payloadVal interface{}
	if len(params.Payload) > 0 {
		payloadVal = params.Payload
	}
	jobType := params.JobType
	if jobType == "" {
		jobType = "tx"
	}
	// The job and its part rows are written together: a caller that polls the
	// moment CreateJob returns must see a row per part, not a list that fills in
	// later and is indistinguishable meanwhile from an archive carrying nothing.
	var id uuid.UUID
	err = p.runInTx(ctx, func(exec queryable) error {
		if err := exec.QueryRowContext(ctx, `
			INSERT INTO ingestion_jobs (user_id, job_type, broker, source, filename, period_from, period_before, payload, status)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, 'PENDING')
			RETURNING id
		`, userUUID, jobType, brokerVal, sourceVal, filenameVal, fromT, beforeT, payloadVal).Scan(&id); err != nil {
			return fmt.Errorf("create job: %w", err)
		}
		for _, part := range params.Parts {
			if _, err := exec.ExecContext(ctx, `
				INSERT INTO ingestion_job_parts (job_id, part, status) VALUES ($1, $2, 'PENDING')
			`, id, archivePartToStr(part)); err != nil {
				return fmt.Errorf("create job part %s: %w", archivePartToStr(part), err)
			}
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	return id.String(), nil
}

// archivePartToStr spells an ArchivePart the way the column holds it, which is
// the enum name itself.
func archivePartToStr(p archivev1.ArchivePart) string {
	return archivev1.ArchivePart_name[int32(p)]
}

func strToArchivePart(s string) archivev1.ArchivePart {
	return archivev1.ArchivePart(archivev1.ArchivePart_value[s])
}

// GetJob implements db.JobDB.
func (p *Postgres) GetJob(ctx context.Context, jobID string) (*db.JobDetail, error) {
	jobUUID, err := uuid.Parse(jobID)
	if err != nil {
		return nil, fmt.Errorf("invalid job id: %w", err)
	}
	var d db.JobDetail
	var statusStr string
	var jobUserID uuid.UUID
	err = p.q.QueryRowContext(ctx, `SELECT status, user_id, COALESCE(broker, ''), COALESCE(source, ''), total_count, processed_count FROM ingestion_jobs WHERE id = $1`, jobUUID).
		Scan(&statusStr, &jobUserID, &d.Broker, &d.Source, &d.TotalCount, &d.ProcessedCount)
	if err == sql.ErrNoRows {
		// No such job. UserID stays empty, which is how the caller tells this
		// apart from a failure to read one that exists.
		return &db.JobDetail{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get job: %w", err)
	}
	d.Status = strToJobStatus(statusStr)
	d.UserID = jobUserID.String()

	idRows, err := p.q.QueryContext(ctx, `SELECT row_index, instrument_description, message FROM identification_errors WHERE job_id = $1 ORDER BY row_index`, jobUUID)
	if err != nil {
		return nil, fmt.Errorf("get identification errors: %w", err)
	}
	defer idRows.Close()
	for idRows.Next() {
		var e db.IdentificationError
		if err := idRows.Scan(&e.RowIndex, &e.InstrumentDescription, &e.Message); err != nil {
			return nil, err
		}
		d.IdentificationErrors = append(d.IdentificationErrors, e)
	}
	if err := idRows.Err(); err != nil {
		return nil, err
	}

	// Parts are read before the validation errors, because which parts exist is
	// what decides where an error can be attributed.
	partRows, err := p.q.QueryContext(ctx, `SELECT part, status, total_count, processed_count, COALESCE(message, '') FROM ingestion_job_parts WHERE job_id = $1`, jobUUID)
	if err != nil {
		return nil, fmt.Errorf("get job parts: %w", err)
	}
	defer partRows.Close()
	partIndex := map[archivev1.ArchivePart]int{}
	for partRows.Next() {
		var r db.JobPartResult
		var partStr, partStatus string
		if err := partRows.Scan(&partStr, &partStatus, &r.TotalCount, &r.ProcessedCount, &r.Message); err != nil {
			return nil, err
		}
		r.Part = strToArchivePart(partStr)
		r.Status = strToJobStatus(partStatus)
		partIndex[r.Part] = len(d.Parts)
		d.Parts = append(d.Parts, r)
	}
	if err := partRows.Err(); err != nil {
		return nil, err
	}
	// Restore order is the enum's order. The column holds the enum name, so
	// sorting has to be by number and not by the string.
	sort.Slice(d.Parts, func(i, j int) bool { return d.Parts[i].Part < d.Parts[j].Part })
	for i, r := range d.Parts {
		partIndex[r.Part] = i
	}

	// Validation errors go to the part they name. A NULL part, or one naming a
	// part this job has no row for, belongs to the job: an error that cannot be
	// attributed is still an error, and dropping it would report a clean run.
	rows, err := p.q.QueryContext(ctx, `SELECT COALESCE(part, ''), row_index, field, message FROM validation_errors WHERE job_id = $1 ORDER BY row_index`, jobUUID)
	if err != nil {
		return nil, fmt.Errorf("get validation errors: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var partStr string
		var e apiv1.ValidationError
		if err := rows.Scan(&partStr, &e.RowIndex, &e.Field, &e.Message); err != nil {
			return nil, err
		}
		i, ok := partIndex[strToArchivePart(partStr)]
		if !ok {
			d.ValidationErrors = append(d.ValidationErrors, &e)
			continue
		}
		d.Parts[i].ValidationErrors = append(d.Parts[i].ValidationErrors, &e)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return &d, nil
}

// SetJobStatus implements db.JobDB.
func (p *Postgres) SetJobStatus(ctx context.Context, jobID string, status apiv1.JobStatus) error {
	jobUUID, err := uuid.Parse(jobID)
	if err != nil {
		return fmt.Errorf("invalid job id: %w", err)
	}
	_, err = p.q.ExecContext(ctx, `UPDATE ingestion_jobs SET status = $2 WHERE id = $1`, jobUUID, jobStatusToStr(status))
	if err != nil {
		return fmt.Errorf("set job status: %w", err)
	}
	return nil
}

// SetJobTotalCount implements db.JobDB.
func (p *Postgres) SetJobTotalCount(ctx context.Context, jobID string, total int32) error {
	jobUUID, err := uuid.Parse(jobID)
	if err != nil {
		return fmt.Errorf("invalid job id: %w", err)
	}
	_, err = p.q.ExecContext(ctx, `UPDATE ingestion_jobs SET total_count = $2 WHERE id = $1`, jobUUID, total)
	if err != nil {
		return fmt.Errorf("set job total count: %w", err)
	}
	return nil
}

// SetJobProcessedCount implements db.JobDB.
func (p *Postgres) SetJobProcessedCount(ctx context.Context, jobID string, processed int32) error {
	jobUUID, err := uuid.Parse(jobID)
	if err != nil {
		return fmt.Errorf("invalid job id: %w", err)
	}
	_, err = p.q.ExecContext(ctx, `UPDATE ingestion_jobs SET processed_count = $2 WHERE id = $1`, jobUUID, processed)
	if err != nil {
		return fmt.Errorf("set job processed count: %w", err)
	}
	return nil
}

// IncrJobProcessedCount implements db.JobDB.
func (p *Postgres) IncrJobProcessedCount(ctx context.Context, jobID string) error {
	jobUUID, err := uuid.Parse(jobID)
	if err != nil {
		return fmt.Errorf("invalid job id: %w", err)
	}
	_, err = p.q.ExecContext(ctx, `UPDATE ingestion_jobs SET processed_count = processed_count + 1 WHERE id = $1`, jobUUID)
	if err != nil {
		return fmt.Errorf("incr job processed count: %w", err)
	}
	return nil
}

// AppendValidationErrors implements db.JobDB.
func (p *Postgres) AppendValidationErrors(ctx context.Context, jobID string, part archivev1.ArchivePart, errs []*apiv1.ValidationError) error {
	jobUUID, err := uuid.Parse(jobID)
	if err != nil {
		return fmt.Errorf("invalid job id: %w", err)
	}
	var partVal interface{}
	if part != archivev1.ArchivePart_ARCHIVE_PART_UNSPECIFIED {
		partVal = archivePartToStr(part)
	}
	for _, e := range errs {
		_, err = p.q.ExecContext(ctx, `INSERT INTO validation_errors (job_id, part, row_index, field, message) VALUES ($1, $2, $3, $4, $5)`,
			jobUUID, partVal, e.RowIndex, e.Field, e.Message)
		if err != nil {
			return fmt.Errorf("append validation error: %w", err)
		}
	}
	return nil
}

// SetJobPartStatus implements db.JobDB.
func (p *Postgres) SetJobPartStatus(ctx context.Context, jobID string, part archivev1.ArchivePart, status apiv1.JobStatus) error {
	return p.execJobPart(ctx, jobID, part, `UPDATE ingestion_job_parts SET status = $3 WHERE job_id = $1 AND part = $2`,
		"set job part status", jobStatusToStr(status))
}

// SetJobPartFailed implements db.JobDB.
func (p *Postgres) SetJobPartFailed(ctx context.Context, jobID string, part archivev1.ArchivePart, message string) error {
	return p.execJobPart(ctx, jobID, part, `UPDATE ingestion_job_parts SET status = 'FAILED', message = $3 WHERE job_id = $1 AND part = $2`,
		"set job part failed", message)
}

// SetJobPartTotalCount implements db.JobDB.
func (p *Postgres) SetJobPartTotalCount(ctx context.Context, jobID string, part archivev1.ArchivePart, total int32) error {
	return p.execJobPart(ctx, jobID, part, `UPDATE ingestion_job_parts SET total_count = $3 WHERE job_id = $1 AND part = $2`,
		"set job part total count", total)
}

// AddJobPartProcessedCount implements db.JobDB.
func (p *Postgres) AddJobPartProcessedCount(ctx context.Context, jobID string, part archivev1.ArchivePart, n int32) error {
	return p.execJobPart(ctx, jobID, part, `UPDATE ingestion_job_parts SET processed_count = processed_count + $3 WHERE job_id = $1 AND part = $2`,
		"add job part processed count", n)
}

// ResetJobPartProgress implements db.JobDB.
func (p *Postgres) ResetJobPartProgress(ctx context.Context, jobID string, part archivev1.ArchivePart) error {
	return p.execJobPart(ctx, jobID, part, `UPDATE ingestion_job_parts SET processed_count = 0, message = NULL WHERE job_id = $1 AND part = $2`,
		"reset job part progress")
}

// execJobPart runs one UPDATE against a part row. Every part mutation is the
// same shape -- parse the id, name the part, set one column -- so they share it.
func (p *Postgres) execJobPart(ctx context.Context, jobID string, part archivev1.ArchivePart, query, what string, args ...interface{}) error {
	jobUUID, err := uuid.Parse(jobID)
	if err != nil {
		return fmt.Errorf("invalid job id: %w", err)
	}
	args = append([]interface{}{jobUUID, archivePartToStr(part)}, args...)
	if _, err := p.q.ExecContext(ctx, query, args...); err != nil {
		return fmt.Errorf("%s: %w", what, err)
	}
	return nil
}

// AppendIdentificationErrors implements db.JobDB.
func (p *Postgres) AppendIdentificationErrors(ctx context.Context, jobID string, errs []db.IdentificationError) error {
	jobUUID, err := uuid.Parse(jobID)
	if err != nil {
		return fmt.Errorf("invalid job id: %w", err)
	}
	for _, e := range errs {
		_, err = p.q.ExecContext(ctx, `INSERT INTO identification_errors (job_id, row_index, instrument_description, message) VALUES ($1, $2, $3, $4)`,
			jobUUID, e.RowIndex, e.InstrumentDescription, e.Message)
		if err != nil {
			return fmt.Errorf("append identification error: %w", err)
		}
	}
	return nil
}

// LoadJobPayload implements db.JobDB.
func (p *Postgres) LoadJobPayload(ctx context.Context, jobID string) ([]byte, error) {
	jobUUID, err := uuid.Parse(jobID)
	if err != nil {
		return nil, fmt.Errorf("invalid job id: %w", err)
	}
	var payload []byte
	err = p.q.QueryRowContext(ctx, `SELECT payload FROM ingestion_jobs WHERE id = $1`, jobUUID).Scan(&payload)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("job not found: %s", jobID)
	}
	if err != nil {
		return nil, fmt.Errorf("load job payload: %w", err)
	}
	return payload, nil
}

// ClearJobPayload implements db.JobDB.
func (p *Postgres) ClearJobPayload(ctx context.Context, jobID string) error {
	jobUUID, err := uuid.Parse(jobID)
	if err != nil {
		return fmt.Errorf("invalid job id: %w", err)
	}
	_, err = p.q.ExecContext(ctx, `UPDATE ingestion_jobs SET payload = NULL WHERE id = $1`, jobUUID)
	if err != nil {
		return fmt.Errorf("clear job payload: %w", err)
	}
	return nil
}

// ListPendingJobs implements db.JobDB.
func (p *Postgres) ListPendingJobs(ctx context.Context) ([]db.PendingJob, error) {
	rows, err := p.q.QueryContext(ctx, `SELECT id, job_type FROM ingestion_jobs WHERE status IN ('PENDING', 'RUNNING') ORDER BY created_at`)
	if err != nil {
		return nil, fmt.Errorf("list pending jobs: %w", err)
	}
	defer rows.Close()
	var jobs []db.PendingJob
	for rows.Next() {
		var id uuid.UUID
		var jobType string
		if err := rows.Scan(&id, &jobType); err != nil {
			return nil, err
		}
		jobs = append(jobs, db.PendingJob{ID: id.String(), JobType: jobType})
	}
	return jobs, rows.Err()
}

// ListJobs implements db.JobDB.
func (p *Postgres) ListJobs(ctx context.Context, userID, jobType string, pageSize int32, pageToken string) ([]db.JobRow, int32, string, error) {
	userUUID, err := uuid.Parse(userID)
	if err != nil {
		return nil, 0, "", fmt.Errorf("invalid user id: %w", err)
	}

	offset := decodePageToken(pageToken)

	// An empty jobType matches every type, so the filter is written once as a
	// predicate that is trivially true rather than as two spellings of the query.
	var typeVal interface{}
	if jobType != "" {
		typeVal = jobType
	}

	var total int32
	if err := p.q.QueryRowContext(ctx, `SELECT COUNT(*) FROM ingestion_jobs WHERE user_id = $1 AND ($2::text IS NULL OR job_type = $2)`, userUUID, typeVal).Scan(&total); err != nil {
		return nil, 0, "", fmt.Errorf("count jobs: %w", err)
	}
	if total == 0 {
		return nil, 0, "", nil
	}

	rows, err := p.q.QueryContext(ctx, `
		SELECT j.id, j.job_type, COALESCE(j.filename, ''), COALESCE(j.broker, ''), j.status, j.created_at,
			(SELECT COUNT(*) FROM validation_errors WHERE job_id = j.id),
			(SELECT COUNT(*) FROM identification_errors WHERE job_id = j.id)
		FROM ingestion_jobs j
		WHERE j.user_id = $1 AND ($2::text IS NULL OR j.job_type = $2)
		ORDER BY j.created_at DESC
		LIMIT $3 OFFSET $4
	`, userUUID, typeVal, pageSize+1, offset)
	if err != nil {
		return nil, 0, "", fmt.Errorf("list jobs: %w", err)
	}
	defer rows.Close()

	var result []db.JobRow
	for rows.Next() {
		var r db.JobRow
		var id uuid.UUID
		if err := rows.Scan(&id, &r.JobType, &r.Filename, &r.Broker, &r.Status, &r.CreatedAt, &r.ValidationErrorCount, &r.IdentificationErrorCount); err != nil {
			return nil, 0, "", err
		}
		r.ID = id.String()
		result = append(result, r)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, "", err
	}

	var nextToken string
	if len(result) > int(pageSize) {
		result = result[:pageSize]
		nextToken = encodePageToken(offset + int64(pageSize))
	}
	return result, total, nextToken, nil
}
