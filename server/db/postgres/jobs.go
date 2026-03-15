package postgres

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/google/uuid"
	apiv1 "github.com/leedenison/portfoliodb/proto/api/v1"
	"github.com/leedenison/portfoliodb/server/db"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// CreateJob implements db.JobDB.
func (p *Postgres) CreateJob(ctx context.Context, userID, broker, source, filename string, periodFrom, periodTo *timestamppb.Timestamp) (string, error) {
	userUUID, err := uuid.Parse(userID)
	if err != nil {
		return "", fmt.Errorf("invalid user id: %w", err)
	}
	var fromT, toT interface{}
	if periodFrom != nil && periodFrom.IsValid() {
		fromT = periodFrom.AsTime()
	}
	if periodTo != nil && periodTo.IsValid() {
		toT = periodTo.AsTime()
	}
	var filenameVal interface{}
	if filename != "" {
		filenameVal = filename
	}
	var id uuid.UUID
	err = p.q.QueryRowContext(ctx, `
		INSERT INTO ingestion_jobs (user_id, broker, source, filename, period_from, period_to, status)
		VALUES ($1, $2, $3, $4, $5, $6, 'PENDING')
		RETURNING id
	`, userUUID, broker, source, filenameVal, fromT, toT).Scan(&id)
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

// ListPendingJobIDs implements db.JobDB.
func (p *Postgres) ListPendingJobIDs(ctx context.Context) ([]string, error) {
	rows, err := p.q.QueryContext(ctx, `SELECT id FROM ingestion_jobs WHERE status = 'PENDING' ORDER BY created_at`)
	if err != nil {
		return nil, fmt.Errorf("list pending jobs: %w", err)
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id.String())
	}
	return ids, rows.Err()
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
		SELECT j.id, COALESCE(j.filename, ''), j.broker, j.status, j.created_at,
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
		if err := rows.Scan(&id, &r.Filename, &r.Broker, &r.Status, &r.CreatedAt, &r.ValidationErrorCount, &r.IdentificationErrorCount); err != nil {
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
