use crate::portfolio_db::{Identifier, Instrument};
use crate::id_resolvers::IdResolver;
use anyhow::Result;

#[derive(Clone)]
pub struct OpenfigiResolver {}

impl OpenfigiResolver {
    pub fn new() -> Self {
        Self {}
    }
}

impl IdResolver for OpenfigiResolver {
    fn name(&self) -> String {
        "openfigi".to_string()
    }

    async fn resolve(&self, _ids: Vec<Identifier>) -> Result<Vec<Instrument>> {
        Ok(vec![])
    }
}
