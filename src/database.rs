use sea_orm::{Database, DatabaseConnection, EntityTrait, QueryFilter, DatabaseTransaction, ColumnTrait, IntoActiveModel};
use sea_orm::prelude::Expr;
use anyhow::Result;
use tracing::info;
use std::sync::Arc;
use std::collections::HashSet;

use crate::transaction::LocalTxn;
use crate::portfolio_db::{Tx, DateRange};
use crate::models::{Instruments, Symbols, Transactions, Prices, Derivatives, Symbol, Instrument, Derivative, Transaction, Price};
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
    /// If no transaction is provided, a new transaction will be created and committed.
    /// If a transaction is provided, the caller is responsible for committing or rolling back.
    /// 
    /// # Arguments
    /// * `symbols` - Slice of Symbols to merge
    /// * `txn` - Optional database transaction to use for all operations
    /// 
    /// # Returns
    /// * `Result<Option<i64>>` - The id of the merged instrument if found,
    /// otherwise None.
    /// 
    /// # Errors
    /// * `anyhow::Error` - If any database operation fails.
    pub async fn merge_instruments(&self, symbols: &[Symbol], txn: Option<&DatabaseTransaction>) -> Result<Option<i64>> {
        if symbols.is_empty() {
            return Ok(None);
        }

        let mut l_txn = LocalTxn::new(&self.conn, txn).await?;

        // Find all instruments that have exact matches to the supplied symbols
        let instrument_ids = self.find_instruments(symbols, Some(l_txn.txn())).await?;
        if instrument_ids.is_empty() {
            l_txn.commit_if_owned().await?;
            return Ok(None);
        }
        
        // First instrument becomes the merged instrument
        let tgt_instrument_id = instrument_ids[0]; 
        if instrument_ids.len() == 1 {
            l_txn.commit_if_owned().await?;
            return Ok(Some(tgt_instrument_id));
        }

        let src_instrument_ids: Vec<i64> = instrument_ids[1..].to_vec();

        self.move_txs_to_instrument(src_instrument_ids.clone(), tgt_instrument_id, Some(l_txn.txn())).await?;
        self.move_prices_to_instrument(src_instrument_ids.clone(), tgt_instrument_id, Some(l_txn.txn())).await?;
        self.move_derivatives_to_instrument(src_instrument_ids.clone(), tgt_instrument_id, Some(l_txn.txn())).await?;

        // Collect all unique symbols
        let unique_symbols = self.unique_symbols(symbols, src_instrument_ids.clone(), Some(l_txn.txn())).await?;

        // Delete instruments being merged
        self.delete_instruments(src_instrument_ids.clone(), Some(l_txn.txn())).await?;

        // Add all unique symbols to the merged instrument
        self.add_symbols(unique_symbols, tgt_instrument_id, Some(l_txn.txn())).await?;

        l_txn.commit_if_owned().await?;
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
        txn: Option<&DatabaseTransaction>,
    ) -> Result<Vec<i64>> {
        if symbols.is_empty() {
            return Ok(Vec::new());
        }

        let mut l_txn = LocalTxn::new(&self.conn, txn).await?;

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
            .all(l_txn.txn())
            .await?;

        // Extract unique instrument IDs from the matching results
        let instrument_ids: Vec<i64> = matching
            .into_iter()
            .filter_map(|(_, instrument)| instrument.map(|i| i.id))
            .collect::<HashSet<_>>()
            .into_iter()
            .collect();

        l_txn.commit_if_owned().await?;
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
        txn: Option<&DatabaseTransaction>,
    ) -> Result<HashSet<(String, String, String, String)>> {
        let mut l_txn = LocalTxn::new(&self.conn, txn).await?;
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
        let existing_symbols = self.find_symbols_by_instruments(src_instrument_ids, Some(l_txn.txn())).await?;
        for symbol in existing_symbols {
            unique_symbols.insert(symbol.to_tuple());
        }

        l_txn.commit_if_owned().await?;
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
        txn: Option<&DatabaseTransaction>,
    ) -> Result<()> {
        if symbols.is_empty() {
            return Ok(());
        }

        let mut l_txn = LocalTxn::new(&self.conn, txn).await?;

        let symbol_models: Vec<crate::models::symbols::ActiveModel> = symbols
            .into_iter()
            .map(|symbol| (symbol, instrument_id).into())
            .collect();

        Symbols::insert_many(symbol_models)
            .exec(l_txn.txn())
            .await?;

        l_txn.commit_if_owned().await?;
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
        txn: Option<&DatabaseTransaction>,
    ) -> Result<Vec<Symbol>> {
        if instrument_ids.is_empty() {
            return Ok(Vec::new());
        }

        let mut l_txn = LocalTxn::new(&self.conn, txn).await?;

        let symbols = Symbols::find()
            .filter(SymCol::InstrumentId.is_in(instrument_ids))
            .all(l_txn.txn())
            .await?;

        l_txn.commit_if_owned().await?;
        Ok(symbols)
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
        txn: Option<&DatabaseTransaction>,
    ) -> Result<()> {
        if src_instrument_ids.is_empty() {
            return Ok(());
        }

        let mut l_txn = LocalTxn::new(&self.conn, txn).await?;

        // Move all transactions from source instruments to target instrument
        Transactions::update_many()
            .col_expr(TxCol::InstrumentId, Expr::val(tgt_instrument_id).into())
            .filter(TxCol::InstrumentId.is_in(src_instrument_ids))
            .exec(l_txn.txn())
            .await?;

        l_txn.commit_if_owned().await?;
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
        txn: Option<&DatabaseTransaction>,
    ) -> Result<()> {
        if src_instrument_ids.is_empty() {
            return Ok(());
        }

        let mut l_txn = LocalTxn::new(&self.conn, txn).await?;

        // Move all prices from source instruments to target instrument
        Prices::update_many()
            .col_expr(PriceCol::InstrumentId, Expr::val(tgt_instrument_id).into())
            .filter(PriceCol::InstrumentId.is_in(src_instrument_ids))
            .exec(l_txn.txn())
            .await?;

        l_txn.commit_if_owned().await?;
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
        txn: Option<&DatabaseTransaction>,
    ) -> Result<()> {
        if src_instrument_ids.is_empty() {
            return Ok(());
        }

        let mut l_txn = LocalTxn::new(&self.conn, txn).await?;

        // Move all derivatives that reference source instruments to target instrument
        Derivatives::update_many()
            .col_expr(DerivativeCol::UnderlyingInstrumentId, Expr::val(tgt_instrument_id).into())
            .filter(DerivativeCol::UnderlyingInstrumentId.is_in(src_instrument_ids))
            .exec(l_txn.txn())
            .await?;

        l_txn.commit_if_owned().await?;
        Ok(())
    }



    /// Creates a new symbol in the database.
    /// 
    /// # Arguments
    /// * `symbol` - Symbol model without id field populated
    /// 
    /// # Returns
    /// * `Result<i64>` - The id of the created symbol
    /// 
    /// # Errors
    /// * `anyhow::Error` - If the database operation fails
    pub async fn create_symbol(&self, symbol: Symbol) -> Result<i64> {
        let active_model = symbol.into_active_model();
        let result = Symbols::insert(active_model)
            .exec(&*self.conn)
            .await?;
        Ok(result.last_insert_id)
    }

    /// Deletes a symbol by its id.
    /// 
    /// # Arguments
    /// * `id` - The id of the symbol to delete
    /// 
    /// # Returns
    /// * `Result<()>` - Success or error
    /// 
    /// # Errors
    /// * `anyhow::Error` - If the database operation fails
    pub async fn delete_symbol(&self, id: i64) -> Result<()> {
        Symbols::delete_by_id(id)
            .exec(&*self.conn)
            .await?;
        Ok(())
    }

    /// Creates a new instrument in the database.
    /// 
    /// # Arguments
    /// * `instrument` - Instrument model without id field populated
    /// 
    /// # Returns
    /// * `Result<i64>` - The id of the created instrument
    /// 
    /// # Errors
    /// * `anyhow::Error` - If the database operation fails
    pub async fn create_instrument(&self, instrument: Instrument) -> Result<i64> {
        let active_model = instrument.into_active_model();
        let result = Instruments::insert(active_model)
            .exec(&*self.conn)
            .await?;
        Ok(result.last_insert_id)
    }
  
    /// Deletes instruments and their associated symbols.
    /// 
    /// Symbols or Derivative metadata owned by this instrument
    /// are assumed to be deleted by ON DELETE CASCADE.
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
        txn: Option<&DatabaseTransaction>,
    ) -> Result<()> {
        if instrument_ids.is_empty() {
            return Ok(());
        }
        
        let mut l_txn = LocalTxn::new(&self.conn, txn).await?;

        // Delete the instruments themselves
        for instrument_id in instrument_ids {
            Instruments::delete_by_id(instrument_id)
                .exec(l_txn.txn())
                .await?;
        }

        l_txn.commit_if_owned().await?;
        Ok(())
    }

    /// Creates a new derivative in the database.
    /// 
    /// # Arguments
    /// * `derivative` - Derivative model without id field populated
    /// 
    /// # Returns
    /// * `Result<i64>` - The id of the created derivative
    /// 
    /// # Errors
    /// * `anyhow::Error` - If the database operation fails
    pub async fn create_derivative(&self, derivative: Derivative) -> Result<i64> {
        let active_model = derivative.into_active_model();
        let result = Derivatives::insert(active_model)
            .exec(&*self.conn)
            .await?;
        Ok(result.last_insert_id)
    }

    /// Deletes a derivative by its id.
    /// 
    /// # Arguments
    /// * `id` - The id of the derivative to delete
    /// 
    /// # Returns
    /// * `Result<()>` - Success or error
    /// 
    /// # Errors
    /// * `anyhow::Error` - If the database operation fails
    pub async fn delete_derivative(&self, id: i64) -> Result<()> {
        Derivatives::delete_by_id(id)
            .exec(&*self.conn)
            .await?;
        Ok(())
    }

    /// Creates a new transaction in the database.
    /// 
    /// # Arguments
    /// * `transaction` - Transaction model without id field populated
    /// 
    /// # Returns
    /// * `Result<i64>` - The id of the created transaction
    /// 
    /// # Errors
    /// * `anyhow::Error` - If the database operation fails
    pub async fn create_transaction(&self, transaction: Transaction) -> Result<i64> {
        let active_model = transaction.into_active_model();
        let result = Transactions::insert(active_model)
            .exec(&*self.conn)
            .await?;
        Ok(result.last_insert_id)
    }

    /// Deletes a transaction by its id.
    /// 
    /// # Arguments
    /// * `id` - The id of the transaction to delete
    /// 
    /// # Returns
    /// * `Result<()>` - Success or error
    /// 
    /// # Errors
    /// * `anyhow::Error` - If the database operation fails
    pub async fn delete_transaction(&self, id: i64) -> Result<()> {
        Transactions::delete_by_id(id)
            .exec(&*self.conn)
            .await?;
        Ok(())
    }

    /// Creates a new price in the database.
    /// 
    /// # Arguments
    /// * `price` - Price model without id field populated
    /// 
    /// # Returns
    /// * `Result<i64>` - The id of the created price
    /// 
    /// # Errors
    /// * `anyhow::Error` - If the database operation fails
    pub async fn create_price(&self, price: Price) -> Result<i64> {
        let active_model = price.into_active_model();
        let result = Prices::insert(active_model)
            .exec(&*self.conn)
            .await?;
        Ok(result.last_insert_id)
    }

    /// Deletes a price by its id.
    /// 
    /// # Arguments
    /// * `id` - The id of the price to delete
    /// 
    /// # Returns
    /// * `Result<()>` - Success or error
    /// 
    /// # Errors
    /// * `anyhow::Error` - If the database operation fails
    pub async fn delete_price(&self, id: i64) -> Result<()> {
        Prices::delete_by_id(id)
            .exec(&*self.conn)
            .await?;
        Ok(())
    }
}