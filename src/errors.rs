use anyhow;
use tonic::Status;

/// Error conversion utilities for RPC services
pub struct Errors;

impl Errors {
    /// Converts an anyhow error to a Status::internal, logging the error for debugging
    ///
    /// Centralised error handling which can be used for instrumentation, and logging
    /// of details for specific error types.
    ///
    /// # Arguments
    /// * `error` - The anyhow error to convert
    ///
    /// # Returns
    /// * `Status::internal` with the error message
    pub fn internal(error: anyhow::Error) -> Status {
        tracing::error!("Internal error: {}", error);
        Status::internal(error.to_string())
    }

    /// Converts an anyhow error to a Status::unauthenticated
    ///
    /// Centralised error handling which can be used for instrumentation.
    ///
    /// # Arguments
    /// * `error` - The anyhow error to convert
    ///
    /// # Returns
    /// * `Status::unauthenticated` with the error message
    pub fn unauthenticated(error: anyhow::Error) -> Status {
        Status::unauthenticated(error.to_string())
    }

    /// Converts an anyhow error to a Status::invalid_argument
    ///
    /// Centralised error handling which can be used for instrumentation.
    ///
    /// # Arguments
    /// * `error` - The anyhow error to convert
    ///
    /// # Returns
    /// * `Status::invalid_argument` with the error message
    pub fn invalid_argument(error: anyhow::Error) -> Status {
        Status::invalid_argument(error.to_string())
    }
}
