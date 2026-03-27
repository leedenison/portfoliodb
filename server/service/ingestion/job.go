package ingestion

// JobRequest is a unit of work for the ingestion worker.
// The actual payload data is persisted in the database (ingestion_jobs.payload)
// and loaded by the worker; only the job ID and type travel on the channel.
type JobRequest struct {
	JobID   string
	JobType string // "tx" or "price_import"
}
