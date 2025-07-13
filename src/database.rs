use sea_orm::{Database, DatabaseConnection, EntityTrait, QueryFilter, TransactionTrait, DatabaseTransaction, ColumnTrait};
use sea_orm::prelude::Expr;
use anyhow::Result;
use tracing::info;
use std::sync::Arc;
use std::collections::HashSet;

use crate::portfolio_db::{Tx, DateRange};
use crate::models::{Instruments, Symbols, Transactions, Prices, Derivatives, Symbol};
use crate::models::symbols::Column as SymCol;
use crate::models::transactions::Column as TxCol;
use crate::models::prices::Column as PriceCol;
use crate::models::derivatives::Column as DerivativeCol;

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
        let tgt_instrument_id = instrument_ids[0]; 

        if instrument_ids.len() == 1 {
            // No merging needed
            return Ok(Some(tgt_instrument_id));
        }

        let src_instrument_ids: Vec<i64> = instrument_ids[1..].to_vec();

        self.move_txs_to_instrument(src_instrument_ids.clone(), tgt_instrument_id, txn).await?;
        self.move_prices_to_instrument(src_instrument_ids.clone(), tgt_instrument_id, txn).await?;
        self.move_derivatives_to_instrument(src_instrument_ids.clone(), tgt_instrument_id, txn).await?;

        // Collect all unique symbols
        let unique_symbols = self.unique_symbols(symbols, src_instrument_ids.clone(), txn).await?;

        // Delete instruments being merged
        // These should not differ in other metadata, but if they do we have no way
        // to reconcile them anyway so we just delete them.
        self.delete_instruments(src_instrument_ids.clone(), txn).await?;

        // Add all unique symbols to the merged instrument
        self.add_symbols(unique_symbols, tgt_instrument_id, txn).await?;

        Ok(Some(tgt_instrument_id))
    }

    /// Finds all instruments that have exact matches to the supplied symbols.
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

        // Build the symbol tuples for the IN clause
        let symbol_tuples: Vec<(String, String, String, String)> = symbols
            .iter()
            .map(|s| (s.domain.clone(), s.symbol.clone(), s.exchange.clone(), s.description.clone()))
            .collect();
        
        let matching = Symbols::find()
            .filter(Expr::tuple([
                Expr::col(SymCol::Domain).into(),
                Expr::col(SymCol::Symbol).into(),
                Expr::col(SymCol::Exchange).into(),
                Expr::col(SymCol::Description).into(),
            ]).in_tuples(symbol_tuples))
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

    /// Creates a HashSet of unique symbol tuples from input symbols and existing instruments.
    /// 
    /// Deduplicates any symbols that already exist in the database.
    /// 
    /// # Arguments
    /// * `symbols` - Slice of Symbols from input
    /// * `src_instrument_ids` - Slice of instrument IDs to get existing symbols from
    /// * `txn` - Database transaction to use for queries
    /// 
    /// # Returns
    /// * `Result<HashSet<(String, String, String, String)>>` - Set of unique symbol tuples
    /// 
    /// # Errors
    /// * `anyhow::Error` - If the database query fails
    async fn unique_symbols(
        &self,
        symbols: &[Symbol],
        src_instrument_ids: Vec<i64>,
        txn: &DatabaseTransaction,
    ) -> Result<HashSet<(String, String, String, String)>> {
        let mut unique_symbols = HashSet::new();
        
        // Add symbols from the input
        for symbol in symbols {
            unique_symbols.insert((
                symbol.domain.clone(),
                symbol.symbol.clone(),
                symbol.exchange.clone(),
                symbol.description.clone(),
            ));
        }

        // Add existing symbols from instruments being merged
        let existing_symbols = self.find_symbols_by_instruments(src_instrument_ids, txn).await?;
        for symbol in existing_symbols {
            unique_symbols.insert(symbol.to_tuple());
        }

        Ok(unique_symbols)
    }

    /// Adds symbols to the database using bulk insert with conflict handling.
    /// 
    /// # Arguments
    /// * `symbols` - HashSet of unique symbol tuples (domain, symbol, exchange, description)
    /// * `instrument_id` - Instrument ID to associate the symbols with
    /// * `txn` - Database transaction to use for the operation
    /// 
    /// # Returns
    /// * `Result<()>` - Success or error
    /// 
    /// # Errors
    /// * `anyhow::Error` - If the database operation fails
    async fn add_symbols(
        &self,
        symbols: HashSet<(String, String, String, String)>,
        instrument_id: i64,
        txn: &DatabaseTransaction,
    ) -> Result<()> {
        if symbols.is_empty() {
            return Ok(());
        }

        let symbol_models: Vec<crate::models::symbols::ActiveModel> = symbols
            .into_iter()
            .map(|symbol| (symbol, instrument_id).into())
            .collect();

        Symbols::insert_many(symbol_models)
            .exec(txn)
            .await?;

        Ok(())
    }

    /// Finds all symbols for the specified instrument IDs.
    /// 
    /// # Arguments
    /// * `instrument_ids` - Vector of instrument IDs to fetch symbols for
    /// * `txn` - Database transaction to use for the query
    /// 
    /// # Returns
    /// * `Result<Vec<Symbol>>` - Vector of Symbols
    /// 
    /// # Errors
    /// * `anyhow::Error` - If the database query fails
    async fn find_symbols_by_instruments(
        &self,
        instrument_ids: Vec<i64>,
        txn: &DatabaseTransaction,
    ) -> Result<Vec<Symbol>> {
        if instrument_ids.is_empty() {
            return Ok(Vec::new());
        }

        let symbols = Symbols::find()
            .filter(SymCol::InstrumentId.is_in(instrument_ids))
            .all(txn)
            .await?;

        Ok(symbols)
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
        instrument_ids: Vec<i64>,
        txn: &DatabaseTransaction,
    ) -> Result<()> {
        if instrument_ids.is_empty() {
            return Ok(());
        }

        Symbols::delete_many()
            .filter(SymCol::InstrumentId.is_in(instrument_ids.clone()))
            .exec(txn)
            .await?;

        // Delete the instruments themselves
        for instrument_id in instrument_ids {
            Instruments::delete_by_id(instrument_id)
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
    /// * `src_instrument_ids` - Vector of instrument IDs to move transactions from
    /// * `tgt_instrument_id` - Instrument ID to move transactions to
    /// * `txn` - Database transaction to use for the operations
    /// 
    /// # Returns
    /// * `Result<()>` - Success or error
    /// 
    /// # Errors
    /// * `anyhow::Error` - If any database operation fails
    async fn move_txs_to_instrument(
        &self,
        src_instrument_ids: Vec<i64>,
        tgt_instrument_id: i64,
        txn: &DatabaseTransaction,
    ) -> Result<()> {
        if src_instrument_ids.is_empty() {
            return Ok(());
        }

        // Move all transactions from source instruments to target instrument
        Transactions::update_many()
            .col_expr(TxCol::InstrumentId, Expr::val(tgt_instrument_id).into())
            .filter(TxCol::InstrumentId.is_in(src_instrument_ids))
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
    /// * `src_instrument_ids` - Vector of instrument IDs to move prices from
    /// * `tgt_instrument_id` - Instrument ID to move prices to
    /// * `txn` - Database transaction to use for the operations
    /// 
    /// # Returns
    /// * `Result<()>` - Success or error
    /// 
    /// # Errors
    /// * `anyhow::Error` - If any database operation fails
    async fn move_prices_to_instrument(
        &self,
        src_instrument_ids: Vec<i64>,
        tgt_instrument_id: i64,
        txn: &DatabaseTransaction,
    ) -> Result<()> {
        if src_instrument_ids.is_empty() {
            return Ok(());
        }

        // Move all prices from source instruments to target instrument
        Prices::update_many()
            .col_expr(PriceCol::InstrumentId, Expr::val(tgt_instrument_id).into())
            .filter(PriceCol::InstrumentId.is_in(src_instrument_ids))
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
    /// * `src_instrument_ids` - Vector of instrument IDs that derivatives currently reference
    /// * `tgt_instrument_id` - Instrument ID to update derivatives to reference
    /// * `txn` - Database transaction to use for the operations
    /// 
    /// # Returns
    /// * `Result<()>` - Success or error
    /// 
    /// # Errors
    /// * `anyhow::Error` - If any database operation fails
    async fn move_derivatives_to_instrument(
        &self,
        src_instrument_ids: Vec<i64>,
        tgt_instrument_id: i64,
        txn: &DatabaseTransaction,
    ) -> Result<()> {
        if src_instrument_ids.is_empty() {
            return Ok(());
        }

        // Move all derivatives that reference source instruments to target instrument
        Derivatives::update_many()
            .col_expr(DerivativeCol::UnderlyingInstrumentId, Expr::val(tgt_instrument_id).into())
            .filter(DerivativeCol::UnderlyingInstrumentId.is_in(src_instrument_ids))
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