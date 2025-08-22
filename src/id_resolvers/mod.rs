//! ID resolvers module for handling identifier resolution in the portfolio database.
//!
//! This module provides traits and implementations for resolving financial instrument
//! identifiers to instrument data from various sources.

pub mod id_resolver;
pub mod openfigi_resolver;

pub use id_resolver::{IdResolver, PriorityResolver, SimpleResolver, StagingResolver};
pub use openfigi_resolver::OpenfigiResolver;

#[cfg(test)]
pub use id_resolver::MockStagingResolver;
