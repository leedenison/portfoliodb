package postgres

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/google/uuid"
	apiv1 "github.com/leedenison/portfoliodb/proto/api/v1"
	"github.com/leedenison/portfoliodb/server/db"
)

// CreateJob implements db.JobDB.
func (p *Postgres) CreateJob(ctx context.Context, params db.CreateJobParams) (string, error) {
	userUUID, err := uuid.Parse(params.UserID)
	if err != nil {
		return "", fmt.Errorf("invalid user id: %w", err)
	}
	var fromT, toT interface{}
	if params.PeriodFrom != nil && params.PeriodFrom.IsValid() {
		fromT = params.PeriodFrom.AsTime()
	}
	if params.PeriodTo != nil && params.PeriodTo.IsValid() {
		toT = params.PeriodTo.AsTime()
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
	var id uuid.UUID
	err = p.q.QueryRowContext(ctx, `
		INSERT INTO ingestion_jobs (user_id, job_type, broker, source, filename, period_from, period_to, payload, status)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, 'PENDING')
		RETURNING id
	`, userUUID, jobType, brokerVal, sourceVal, filenameVal, fromT, toT, payloadVal).Scan(&id)
	if err != nil {
		return "", fmt.Errorf("create job: %w", err)
	}
	return id.String(), nil
}

// GetJob implements db.JobDB.
func (p *Postgres) GetJob(ctx context.Context, jobID string) (apiv1.JobStatus, []*apiv1.ValidationError, []db.IdentificationError, string, int32, int32, error) {
	jobUUID, err := uuid.Parse(jobID)
	if err != nil {
		return apiv1.JobStatus_JOB_STATUS_UNSPECIFIED, nil, nil, "", 0, 0, fmt.Errorf("invalid job id: %w", err)
	}
	var statusStr string
	var jobUserID uuid.UUID
	var totalCount, processedCount int32
	err = p.q.QueryRowContext(ctx, `SELECT status, user_id, total_count, processed_count FROM ingestion_jobs WHERE id = $1`, jobUUID).Scan(&statusStr, &jobUserID, &totalCount, &processedCount)
	if err == sql.ErrNoRows {
		return apiv1.JobStatus_JOB_STATUS_UNSPECIFIED, nil, nil, "", 0, 0, nil
	}
	if err != nil {
		return apiv1.JobStatus_JOB_STATUS_UNSPECIFIED, nil, nil, "", 0, 0, fmt.Errorf("get job: %w", err)
	}
	rows, err := p.q.QueryContext(ctx, `SELECT row_index, field, message FROM validation_errors WHERE job_id = $1 ORDER BY row_index`, jobUUID)
	if err != nil {
		return apiv1.JobStatus_JOB_STATUS_UNSPECIFIED, nil, nil, "", 0, 0, fmt.Errorf("get validation errors: %w", err)
	}
	defer rows.Close()
	var errs []*apiv1.ValidationError
	for rows.Next() {
		var rowIndex int32
		var field, message string
		if err := rows.Scan(&rowIndex, &field, &message); err != nil {
			return apiv1.JobStatus_JOB_STATUS_UNSPECIFIED, nil, nil, "", 0, 0, err
		}
		errs = append(errs, &apiv1.ValidationError{RowIndex: rowIndex, Field: field, Message: message})
	}
	if err := rows.Err(); err != nil {
		return apiv1.JobStatus_JOB_STATUS_UNSPECIFIED, nil, nil, "", 0, 0, err
	}
	// Identification errors
	idRows, err := p.q.QueryContext(ctx, `SELECT row_index, instrument_description, message FROM identification_errors WHERE job_id = $1 ORDER BY row_index`, jobUUID)
	if err != nil {
		return apiv1.JobStatus_JOB_STATUS_UNSPECIFIED, nil, nil, "", 0, 0, fmt.Errorf("get identification errors: %w", err)
	}
	defer idRows.Close()
	var idErrs []db.IdentificationError
	for idRows.Next() {
		var e db.IdentificationError
		if err := idRows.Scan(&e.RowIndex, &e.InstrumentDescription, &e.Message); err != nil {
			return apiv1.JobStatus_JOB_STATUS_UNSPECIFIED, nil, nil, "", 0, 0, err
		}
		idErrs = append(idErrs, e)
	}
	if err := idRows.Err(); err != nil {
		return apiv1.JobStatus_JOB_STATUS_UNSPECIFIED, nil, nil, "", 0, 0, err
	}
	return strToJobStatus(statusStr), errs, idErrs, jobUserID.String(), totalCount, processedCount, nil
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
func (p *Postgres) AppendValidationErrors(ctx context.Context, jobID string, errs []*apiv1.ValidationError) error {
	jobUUID, err := uuid.Parse(jobID)
	if err != nil {
		return fmt.Errorf("invalid job id: %w", err)
	}
	for _, e := range errs {
		_, err = p.q.ExecContext(ctx, `INSERT INTO validation_errors (job_id, row_index, field, message) VALUES ($1, $2, $3, $4)`,
			jobUUID, e.RowIndex, e.Field, e.Message)
		if err != nil {
			return fmt.Errorf("append validation error: %w", err)
		}
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
func (p *Postgres) ListJobs(ctx context.Context, userID string, pageSize int32, pageToken string) ([]db.JobRow, int32, string, error) {
	userUUID, err := uuid.Parse(userID)
	if err != nil {
		return nil, 0, "", fmt.Errorf("invalid user id: %w", err)
	}

	offset := decodePageToken(pageToken)

	var total int32
	if err := p.q.QueryRowContext(ctx, `SELECT COUNT(*) FROM ingestion_jobs WHERE user_id = $1`, userUUID).Scan(&total); err != nil {
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
		WHERE j.user_id = $1
		ORDER BY j.created_at DESC
		LIMIT $2 OFFSET $3
	`, userUUID, pageSize+1, offset)
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
