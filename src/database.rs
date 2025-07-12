use sea_orm::{Database, DatabaseConnection, EntityTrait, QueryFilter, ColumnTrait, TransactionTrait, ActiveModelTrait, Set, DatabaseTransaction, Query, Expr};
use anyhow::Result;
use tracing::info;
use std::sync::Arc;
use std::collections::HashSet;

use crate::portfolio_db::{Tx, DateRange, Symbol};
use crate::models::{Instruments, Symbols, Transactions, Prices, Derivatives, Instrument, Symbol as SymbolModel};

#[derive(Clone)]
pub struct DatabaseManager {
    conn: Arc<DatabaseConnection>,
}

impl DatabaseManager {
    pub async fn new(database_url: &str) -> Result<Self> {
        let conn = Database::connect(database_url).await?;
        Ok(Self { 
            conn: Arc::new(conn)
        })
    }

    /// Updates transactions for a specific account within a given time period.
    /// This operation replaces all existing transactions in the specified period with the provided
    /// transactions. If the transactions list is empty, this effectively deletes all transactions
    /// in the period.
    pub async fn update_txs(
        &self,
        _period: &DateRange,
        _txs: &[Tx],
    ) -> Result<()> {

        info!("update_transactions not implemented");

        Ok(())
    }

    /// Merges the instruments identified by the supplied Symbols.
    /// 
    /// Queries the database for all instruments with exact matches to the
    /// supplied Symbols (on domain, symbol, exchange and description).
    /// 
    /// If multiple instruments are found they are merged into a single
    /// instrument.  Then:
    ///  - The first instrument is chosen as the merged instrument.
    ///  - All transactions for the other existing instruments are moved
    ///    to the merged instrument.
    ///  - All prices for the other existing instruments are moved to 
    ///    the merged instrument.
    ///  - All derivatives that point to the other existing instruments are
    ///    moved to the merged instrument.
    ///  - The other existing instruments are deleted.
    ///  - All unique symbols from the union of the supplied symbols and the
    ///    existing instruments are added to the merged instrument.
    ///  - The merged instrument id is returned.
    /// 
    /// If a single instrument is found it is returned.
    /// 
    /// If no instruments are found then Ok(None) is returned.
    /// 
    /// All updates are performed within the provided transaction. The caller
    /// is responsible for committing or rolling back the transaction.
    /// 
    /// # Arguments
    /// * `symbols` - Slice of Symbols to merge
    /// * `txn` - Database transaction to use for all operations
    /// 
    /// # Returns
    /// * `Result<Option<i64>>` - The id of the merged instrument if found,
    /// otherwise None.
    /// 
    /// # Errors
    /// * `anyhow::Error` - If any database operation fails.
    pub async fn merge_instruments_with_txn(&self, symbols: &[Symbol], txn: &DatabaseTransaction) -> Result<Option<i64>> {
        if symbols.is_empty() {
            return Ok(None);
        }

        // Find all instruments that have exact matches to the supplied symbols
        let instrument_ids = self.find_instruments(symbols, txn).await?;

        if instrument_ids.is_empty() {
            return Ok(None);
        }
        
        // First instrument becomes the merged instrument
        let merged_instrument_id = instrument_ids[0]; 

        if instrument_ids.len() == 1 {
            // No merging needed
            return Ok(Some(merged_instrument_id));
        }

        let instruments_to_merge: Vec<i64> = instrument_ids[1..].to_vec();

        self.move_txs_to_instrument(&instruments_to_merge, merged_instrument_id, txn).await?;
        self.move_prices_to_instrument(&instruments_to_merge, merged_instrument_id, txn).await?;
        self.move_derivatives_to_instrument(&instruments_to_merge, merged_instrument_id, txn).await?;

        // Collect all unique symbol models
        let unique_symbol_models = self.unique_symbol_models(symbols, &instruments_to_merge, merged_instrument_id, txn).await?;

        // Delete instruments being merged
        self.delete_instruments(&instruments_to_merge, txn).await?;

        // Add all unique symbol models to the merged instrument
        self.add_symbol_models(unique_symbol_models, txn).await?;

        Ok(Some(merged_instrument_id))
    }

    /// Finds all instruments that have exact matches to the supplied symbols.
    /// 
    /// Uses a single efficient query with an IN clause to find all matching
    /// symbols and their associated instruments, then extracts unique instrument IDs.
    /// 
    /// # Arguments
    /// * `symbols` - Slice of Symbols to search for
    /// * `txn` - Database transaction to use for the query
    /// 
    /// # Returns
    /// * `Result<Vec<i64>>` - Vector of unique instrument IDs that match the symbols
    /// 
    /// # Errors
    /// * `anyhow::Error` - If the database query fails
    async fn find_instruments(
        &self,
        symbols: &[Symbol],
        txn: &DatabaseTransaction,
    ) -> Result<Vec<i64>> {
        if symbols.is_empty() {
            return Ok(Vec::new());
        }

        // Build the symbol combinations for the IN clause
        let symbol_combinations: Vec<(String, String, String, String)> = symbols
            .iter()
            .map(|s| (s.domain.clone(), s.symbol.clone(), s.exchange.clone(), s.description.clone()))
            .collect();
        
        // Single query to find all matching symbols and their instruments
        let matching = Symbols::find()
            .filter(Expr::tuple([
                Expr::col(crate::models::symbols::Column::Domain),
                Expr::col(crate::models::symbols::Column::Symbol),
                Expr::col(crate::models::symbols::Column::Exchange),
                Expr::col(crate::models::symbols::Column::Description),
            ]).in_tuples(symbol_combinations))
            .find_also_related(Instruments)
            .all(txn)
            .await?;

        // Extract unique instrument IDs from the matching results
        let instrument_ids: Vec<i64> = matching
            .into_iter()
            .filter_map(|(_, instrument)| instrument.map(|i| i.id))
            .collect::<HashSet<_>>()
            .into_iter()
            .collect();

        Ok(instrument_ids)
    }

    /// Creates a HashSet of unique symbol ActiveModels from input symbols and existing instruments.
    /// 
    /// Combines symbols from the input with existing symbols from instruments being merged,
    /// creating ActiveModels for the target instrument. Handles deduplication automatically.
    /// 
    /// # Arguments
    /// * `symbols` - Slice of Symbols from input
    /// * `instruments_to_merge` - Vector of instrument IDs to get existing symbols from
    /// * `target_instrument_id` - Instrument ID to associate the symbols with
    /// * `txn` - Database transaction to use for queries
    /// 
    /// # Returns
    /// * `Result<HashSet<crate::models::symbols::ActiveModel>>` - Set of unique symbol ActiveModels
    /// 
    /// # Errors
    /// * `anyhow::Error` - If the database query fails
    async fn unique_symbol_models(
        &self,
        symbols: &[Symbol],
        instruments_to_merge: &[i64],
        target_instrument_id: i64,
        txn: &DatabaseTransaction,
    ) -> Result<HashSet<crate::models::symbols::ActiveModel>> {
        let mut unique_symbol_models = HashSet::new();
        
        // Add symbols from the input
        for symbol in symbols {
            let model = crate::models::symbols::ActiveModel {
                instrument_id: Set(target_instrument_id),
                domain: Set(symbol.domain.clone()),
                symbol: Set(symbol.symbol.clone()),
                exchange: Set(symbol.exchange.clone()),
                description: Set(symbol.description.clone()),
                ..Default::default()
            };
            unique_symbol_models.insert(model);
        }

        // Add existing symbols from instruments being merged
        let existing_symbols = self.find_symbols_by_instruments(instruments_to_merge, txn).await?;
        for (domain, symbol, exchange, description) in existing_symbols {
            let model = crate::models::symbols::ActiveModel {
                instrument_id: Set(target_instrument_id),
                domain: Set(domain),
                symbol: Set(symbol),
                exchange: Set(exchange),
                description: Set(description),
                ..Default::default()
            };
            unique_symbol_models.insert(model);
        }

        Ok(unique_symbol_models)
    }

    /// Adds symbol models to the database using bulk insert with conflict handling.
    /// 
    /// Uses ON CONFLICT DO NOTHING to handle cases where symbols already exist,
    /// preventing duplicate key violations.
    /// 
    /// # Arguments
    /// * `symbol_models` - HashSet of unique symbol ActiveModels to insert
    /// * `txn` - Database transaction to use for the operation
    /// 
    /// # Returns
    /// * `Result<()>` - Success or error
    /// 
    /// # Errors
    /// * `anyhow::Error` - If the database operation fails
    async fn add_symbol_models(
        &self,
        symbol_models: HashSet<crate::models::symbols::ActiveModel>,
        txn: &DatabaseTransaction,
    ) -> Result<()> {
        if symbol_models.is_empty() {
            return Ok(());
        }

        Symbols::insert_many(symbol_models.into_iter().collect())
            .on_conflict(
                OnConflict::columns([
                    crate::models::symbols::Column::InstrumentId,
                    crate::models::symbols::Column::Domain,
                    crate::models::symbols::Column::Symbol,
                    crate::models::symbols::Column::Exchange,
                    crate::models::symbols::Column::Description,
                ])
                .do_nothing()
            )
            .exec(txn)
            .await?;

        Ok(())
    }

    /// Finds all symbols for the specified instrument IDs using a single efficient query.
    /// 
    /// Uses an IN clause to fetch all symbols in one database query.
    /// 
    /// # Arguments
    /// * `instrument_ids` - Vector of instrument IDs to fetch symbols for
    /// * `txn` - Database transaction to use for the query
    /// 
    /// # Returns
    /// * `Result<Vec<(String, String, String, String)>>` - Vector of symbol tuples (domain, symbol, exchange, description)
    /// 
    /// # Errors
    /// * `anyhow::Error` - If the database query fails
    async fn find_symbols_by_instruments(
        &self,
        instrument_ids: &[i64],
        txn: &DatabaseTransaction,
    ) -> Result<Vec<(String, String, String, String)>> {
        if instrument_ids.is_empty() {
            return Ok(Vec::new());
        }

        // Single query to find all symbols for the specified instruments
        let symbols = Symbols::find()
            .filter(crate::models::symbols::Column::InstrumentId.in_tuples(instrument_ids))
            .all(txn)
            .await?;

        // Convert to tuples
        let symbol_tuples: Vec<(String, String, String, String)> = symbols
            .into_iter()
            .map(|s| (s.domain, s.symbol, s.exchange, s.description))
            .collect();

        Ok(symbol_tuples)
    }

    /// Deletes instruments and their associated symbols.
    /// 
    /// First deletes all symbols associated with the instruments, then deletes
    /// the instruments themselves. Does not delete associated transactions, prices
    /// or derivative references.
    /// 
    /// # Arguments
    /// * `instrument_ids` - Vector of instrument IDs to delete
    /// * `txn` - Database transaction to use for the operations
    /// 
    /// # Returns
    /// * `Result<()>` - Success or error
    /// 
    /// # Errors
    /// * `anyhow::Error` - If any database operation fails
    async fn delete_instruments(
        &self,
        instrument_ids: &[i64],
        txn: &DatabaseTransaction,
    ) -> Result<()> {
        if instrument_ids.is_empty() {
            return Ok(());
        }

        // Delete symbols from instruments being deleted
        Symbols::delete_many()
            .filter(crate::models::symbols::Column::InstrumentId.in_tuples(instrument_ids))
            .exec(txn)
            .await?;

        // Delete the instruments themselves
        for instrument_id in instrument_ids {
            Instruments::delete_by_id(*instrument_id)
                .exec(txn)
                .await?;
        }

        Ok(())
    }

    /// Moves all transactions from source instruments to a target instrument.
    /// 
    /// Updates the instrument_id of all transactions that belong to the source
    /// instruments to point to the target instrument.
    /// 
    /// # Arguments
    /// * `source_instrument_ids` - Vector of instrument IDs to move transactions from
    /// * `target_instrument_id` - Instrument ID to move transactions to
    /// * `txn` - Database transaction to use for the operations
    /// 
    /// # Returns
    /// * `Result<()>` - Success or error
    /// 
    /// # Errors
    /// * `anyhow::Error` - If any database operation fails
    async fn move_txs_to_instrument(
        &self,
        source_instrument_ids: &[i64],
        target_instrument_id: i64,
        txn: &DatabaseTransaction,
    ) -> Result<()> {
        if source_instrument_ids.is_empty() {
            return Ok(());
        }

        // Move all transactions from source instruments to target instrument
        Transactions::update_many()
            .col_expr(crate::models::transactions::Column::InstrumentId, Set(target_instrument_id))
            .filter(crate::models::transactions::Column::InstrumentId.in_tuples(source_instrument_ids))
            .exec(txn)
            .await?;

        Ok(())
    }

    /// Moves all prices from source instruments to a target instrument.
    /// 
    /// Updates the instrument_id of all prices that belong to the source
    /// instruments to point to the target instrument.
    /// 
    /// # Arguments
    /// * `source_instrument_ids` - Vector of instrument IDs to move prices from
    /// * `target_instrument_id` - Instrument ID to move prices to
    /// * `txn` - Database transaction to use for the operations
    /// 
    /// # Returns
    /// * `Result<()>` - Success or error
    /// 
    /// # Errors
    /// * `anyhow::Error` - If any database operation fails
    async fn move_prices_to_instrument(
        &self,
        source_instrument_ids: &[i64],
        target_instrument_id: i64,
        txn: &DatabaseTransaction,
    ) -> Result<()> {
        if source_instrument_ids.is_empty() {
            return Ok(());
        }

        // Move all prices from source instruments to target instrument
        Prices::update_many()
            .col_expr(crate::models::prices::Column::InstrumentId, Set(target_instrument_id))
            .filter(crate::models::prices::Column::InstrumentId.in_tuples(source_instrument_ids))
            .exec(txn)
            .await?;

        Ok(())
    }

    /// Moves all derivatives that reference source instruments to point to a target instrument.
    /// 
    /// Updates the underlying_instrument_id of all derivatives that reference the source
    /// instruments to point to the target instrument.
    /// 
    /// # Arguments
    /// * `source_instrument_ids` - Vector of instrument IDs that derivatives currently reference
    /// * `target_instrument_id` - Instrument ID to update derivatives to reference
    /// * `txn` - Database transaction to use for the operations
    /// 
    /// # Returns
    /// * `Result<()>` - Success or error
    /// 
    /// # Errors
    /// * `anyhow::Error` - If any database operation fails
    async fn move_derivatives_to_instrument(
        &self,
        source_instrument_ids: &[i64],
        target_instrument_id: i64,
        txn: &DatabaseTransaction,
    ) -> Result<()> {
        if source_instrument_ids.is_empty() {
            return Ok(());
        }

        // Move all derivatives that reference source instruments to target instrument
        Derivatives::update_many()
            .col_expr(crate::models::derivatives::Column::UnderlyingInstrumentId, Set(target_instrument_id))
            .filter(crate::models::derivatives::Column::UnderlyingInstrumentId.in_tuples(source_instrument_ids))
            .exec(txn)
            .await?;

        Ok(())
    }

    /// Convenience method that wraps `merge_instruments_with_txn` in a transaction and commits it.
    /// 
    /// # Arguments
    /// * `symbols` - Slice of Symbols to merge
    /// 
    /// # Returns
    /// * `Result<Option<i64>>` - The id of the merged instrument if found,
    /// otherwise None.
    /// 
    /// # Errors
    /// * `anyhow::Error` - If any database operation fails or the transaction fails to commit.
    pub async fn merge_instruments(&self, symbols: &[Symbol]) -> Result<Option<i64>> {
        let txn = self.conn.begin().await?;
        let result = self.merge_instruments_with_txn(symbols, &txn).await?;
        txn.commit().await?;
        Ok(result)
    }
}