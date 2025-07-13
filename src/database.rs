use sea_orm::{Database, DatabaseConnection, EntityTrait, QueryFilter, DatabaseTransaction, ColumnTrait, IntoActiveModel};
use sea_orm::prelude::Expr;
use anyhow::Result;
use tracing::info;
use std::sync::Arc;
use std::collections::HashSet;

use crate::transaction::LocalTxn;
use crate::portfolio_db::{Tx, DateRange};
use crate::models::{Instruments, Identifiers, Transactions, Prices, Derivatives, Identifier, Instrument, Derivative, Transaction, Price};
use crate::models::identifiers::Column as IdCol;
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

    /// Merges the instruments identified by the supplied Identifiers.
    /// 
    /// Queries the database for all instruments with exact matches to the
    /// supplied Identifiers (on domain, symbol, exchange and description).
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
    ///  - All unique identifiers from the union of the supplied identifiers and the
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
    /// * `ids` - Slice of Identifiers to merge
    /// * `txn` - Optional database transaction to use for all operations
    /// 
    /// # Returns
    /// * `Result<Option<i64>>` - The id of the merged instrument if found,
    /// otherwise None.
    /// 
    /// # Errors
    /// * `anyhow::Error` - If any database operation fails.
    pub async fn merge_instruments(&self, ids: &[Identifier], txn: Option<&DatabaseTransaction>) -> Result<Option<i64>> {
        if ids.is_empty() {
            return Ok(None);
        }

        let mut l_txn = LocalTxn::new(&self.conn, txn).await?;

        // Find all instruments that have exact matches to the supplied identifiers
        let instr_dbids = self.find_instruments(ids, Some(l_txn.txn())).await?;
        if instr_dbids.is_empty() {
            l_txn.commit_if_owned().await?;
            return Ok(None);
        }
        
        // First instrument becomes the merged instrument
        let tgt_instr_dbid = instr_dbids[0]; 
        if instr_dbids.len() == 1 {
            l_txn.commit_if_owned().await?;
            return Ok(Some(tgt_instr_dbid));
        }

        let src_instr_dbids: Vec<i64> = instr_dbids[1..].to_vec();

        self.move_txs_to_instrument(src_instr_dbids.clone(), tgt_instr_dbid, Some(l_txn.txn())).await?;
        self.move_prices_to_instrument(src_instr_dbids.clone(), tgt_instr_dbid, Some(l_txn.txn())).await?;
        self.move_derivatives_to_instrument(src_instr_dbids.clone(), tgt_instr_dbid, Some(l_txn.txn())).await?;

        // Collect all unique identifiers
        let unique_ids = self.unique_identifiers(ids, src_instr_dbids.clone(), Some(l_txn.txn())).await?;

        // Delete instruments being merged
        self.delete_instruments(src_instr_dbids.clone(), Some(l_txn.txn())).await?;

        // Add all unique identifiers to the merged instrument
        self.add_identifiers(unique_ids, tgt_instr_dbid, Some(l_txn.txn())).await?;

        l_txn.commit_if_owned().await?;
        Ok(Some(tgt_instr_dbid))
    }

    /// Finds all instruments that have exact matches to the supplied identifiers.
    /// 
    /// # Arguments
    /// * `ids` - Slice of Identifiers to search for
    /// * `txn` - Database transaction to use for the query
    /// 
    /// # Returns
    /// * `Result<Vec<i64>>` - Vector of unique instrument IDs that match the identifiers
    /// 
    /// # Errors
    /// * `anyhow::Error` - If the database query fails
    async fn find_instruments(
        &self,
        ids: &[Identifier],
        txn: Option<&DatabaseTransaction>,
    ) -> Result<Vec<i64>> {
        if ids.is_empty() {
            return Ok(Vec::new());
        }

        let mut l_txn = LocalTxn::new(&self.conn, txn).await?;

        // Build the identifier tuples for the IN clause
        let id_tuples: Vec<(String, String, String, String, String)> = ids
            .iter()
            .map(|i| (i.id.clone(), i.domain.clone(), i.symbol.clone(), i.exchange.clone(), i.description.clone()))
            .collect();
        
        let matching = Identifiers::find()
            .filter(Expr::tuple([
                Expr::col(IdCol::Id).into(),
                Expr::col(IdCol::Domain).into(),
                Expr::col(IdCol::Symbol).into(),
                Expr::col(IdCol::Exchange).into(),
                Expr::col(IdCol::Description).into(),
            ]).in_tuples(id_tuples))
            .find_also_related(Instruments)
            .all(l_txn.txn())
            .await?;

        // Extract unique instrument IDs from the matching results
        let instr_dbids: Vec<i64> = matching
            .into_iter()
            .filter_map(|(_, instrument)| instrument.map(|i| i.dbid))
            .collect::<HashSet<_>>()
            .into_iter()
            .collect();

        l_txn.commit_if_owned().await?;
        Ok(instr_dbids)
    }

    /// Creates a HashSet of unique identifier tuples from input identifiers and existing instruments.
    /// Deduplicates any identifiers that already exist in the database.
    /// 
    /// # Arguments
    /// * `ids` - Slice of Identifiers from input
    /// * `src_instr_dbids` - Slice of instrument IDs to get existing identifiers from
    /// * `Result<HashSet<(String, String, String, String, String)>>` - Set of unique identifier tuples
    /// 
    /// # Errors
    /// * `anyhow::Error` - If the database query fails
    async fn unique_identifiers(
        &self,
        ids: &[Identifier],
        src_instr_dbids: Vec<i64>,
        txn: Option<&DatabaseTransaction>,
    ) -> Result<HashSet<(String, String, String, String, String)>> {
        let mut l_txn = LocalTxn::new(&self.conn, txn).await?;
        let mut unique_ids = HashSet::new();
        
        // Add identifiers from the input
        for id in ids {
            unique_ids.insert((
                id.id.clone(),
                id.domain.clone(),
                id.symbol.clone(),
                id.exchange.clone(),
                id.description.clone(),
            ));
        }

        // Add existing identifiers from instruments being merged
        let existing_ids = self.find_identifiers_by_instruments(src_instr_dbids.clone(), Some(l_txn.txn())).await?;
        for id in existing_ids {
            unique_ids.insert(id.to_tuple());
        }

        l_txn.commit_if_owned().await?;
        Ok(unique_ids)
    }

    /// Adds identifiers to the database using bulk insert with conflict handling.
    /// 
    /// # Arguments
    /// * `ids` - HashSet of unique identifier tuples (id, domain, symbol, exchange, description)
    /// * `instrument_dbid` - Instrument ID to associate the identifiers with
    /// 
    /// # Returns
    /// * `Result<()>` - Success or error
    /// 
    /// # Errors
    /// * `anyhow::Error` - If the database operation fails
    async fn add_identifiers(
        &self,
        ids: HashSet<(String, String, String, String, String)>,
        instrument_dbid: i64,
        txn: Option<&DatabaseTransaction>,
    ) -> Result<()> {
        if ids.is_empty() {
            return Ok(());
        }

        let mut l_txn = LocalTxn::new(&self.conn, txn).await?;

        let id_models: Vec<crate::models::identifiers::ActiveModel> = ids
            .into_iter()
            .map(|(id, domain, symbol, exchange, description)| ((id, domain, symbol, exchange, description), instrument_dbid).into())
            .collect();

        Identifiers::insert_many(id_models)
            .exec(l_txn.txn())
            .await?;

        l_txn.commit_if_owned().await?;
        Ok(())
    }

    /// Finds all identifiers for the specified instrument IDs.
    /// 
    /// # Arguments
    /// * `instr_dbids` - Vector of instrument IDs to fetch identifiers for
    /// * `Result<Vec<Identifier>>` - Vector of Identifiers
    /// 
    /// # Errors
    /// * `anyhow::Error` - If the database query fails
    async fn find_identifiers_by_instruments(
        &self,
        instr_dbids: Vec<i64>,
        txn: Option<&DatabaseTransaction>,
    ) -> Result<Vec<Identifier>> {
        if instr_dbids.is_empty() {
            return Ok(Vec::new());
        }

        let mut l_txn = LocalTxn::new(&self.conn, txn).await?;

        let ids = Identifiers::find()
            .filter(IdCol::InstrumentDbid.is_in(instr_dbids))
            .all(l_txn.txn())
            .await?;

        l_txn.commit_if_owned().await?;
        Ok(ids)
    }


    /// Moves all transactions from source instruments to a target instrument.
    /// 
    /// Updates the instrument_dbid of all transactions that belong to the source
    /// instruments to point to the target instrument.
    /// 
    /// # Arguments
    /// * `src_instr_dbids` - Vector of instrument IDs to move transactions from
    /// * `tgt_instr_dbid` - Instrument ID to move transactions to
    /// * `txn` - Database transaction to use for the operations
    /// 
    /// # Returns
    /// * `Result<()>` - Success or error
    /// 
    /// # Errors
    /// * `anyhow::Error` - If any database operation fails
    async fn move_txs_to_instrument(
        &self,
        src_instr_dbids: Vec<i64>,
        tgt_instr_dbid: i64,
        txn: Option<&DatabaseTransaction>,
    ) -> Result<()> {
        if src_instr_dbids.is_empty() {
            return Ok(());
        }

        let mut l_txn = LocalTxn::new(&self.conn, txn).await?;

        // Move all transactions from source instruments to target instrument
        Transactions::update_many()
            .col_expr(TxCol::InstrumentDbid, Expr::val(tgt_instr_dbid).into())
            .filter(TxCol::InstrumentDbid.is_in(src_instr_dbids))
            .exec(l_txn.txn())
            .await?;

        l_txn.commit_if_owned().await?;
        Ok(())
    }

    /// Moves all prices from source instruments to a target instrument.
    /// 
    /// Updates the instrument_dbid of all prices that belong to the source
    /// instruments to point to the target instrument.
    /// 
    /// # Arguments
    /// * `src_instr_dbids` - Vector of instrument IDs to move prices from
    /// * `tgt_instr_dbid` - Instrument ID to move prices to
    /// * `txn` - Database transaction to use for the operations
    /// 
    /// # Returns
    /// * `Result<()>` - Success or error
    /// 
    /// # Errors
    /// * `anyhow::Error` - If any database operation fails
    async fn move_prices_to_instrument(
        &self,
        src_instr_dbids: Vec<i64>,
        tgt_instr_dbid: i64,
        txn: Option<&DatabaseTransaction>,
    ) -> Result<()> {
        if src_instr_dbids.is_empty() {
            return Ok(());
        }

        let mut l_txn = LocalTxn::new(&self.conn, txn).await?;

        // Move all prices from source instruments to target instrument
        Prices::update_many()
            .col_expr(PriceCol::InstrumentDbid, Expr::val(tgt_instr_dbid).into())
            .filter(PriceCol::InstrumentDbid.is_in(src_instr_dbids))
            .exec(l_txn.txn())
            .await?;

        l_txn.commit_if_owned().await?;
        Ok(())
    }

    /// Moves all derivatives that reference source instruments to point to a target instrument.
    /// 
    /// Updates the underlying_dbid of all derivatives that reference the source
    /// instruments to point to the target instrument.
    /// 
    /// # Arguments
    /// * `src_instr_dbids` - Vector of instrument IDs that derivatives currently reference
    /// * `tgt_instr_dbid` - Instrument ID to update derivatives to reference
    /// * `txn` - Database transaction to use for the operations
    /// 
    /// # Returns
    /// * `Result<()>` - Success or error
    /// 
    /// # Errors
    /// * `anyhow::Error` - If any database operation fails
    async fn move_derivatives_to_instrument(
        &self,
        src_instr_dbids: Vec<i64>,
        tgt_instr_dbid: i64,
        txn: Option<&DatabaseTransaction>,
    ) -> Result<()> {
        if src_instr_dbids.is_empty() {
            return Ok(());
        }

        let mut l_txn = LocalTxn::new(&self.conn, txn).await?;

        // Move all derivatives that reference source instruments to target instrument
        Derivatives::update_many()
            .col_expr(DerivativeCol::UnderlyingDbid, Expr::val(tgt_instr_dbid).into())
            .filter(DerivativeCol::UnderlyingDbid.is_in(src_instr_dbids))
            .exec(l_txn.txn())
            .await?;

        l_txn.commit_if_owned().await?;
        Ok(())
    }

    /// Creates a new identifier in the database.
    /// 
    /// # Arguments
    /// * `identifier` - Identifier model without id field populated
    /// 
    /// # Returns
    /// * `Result<i64>` - The id of the created identifier
    /// 
    /// # Errors
    /// * `anyhow::Error` - If the database operation fails
    pub async fn create_identifier(&self, identifier: Identifier) -> Result<i64> {
        let active_model = identifier.into_active_model();
        let result = Identifiers::insert(active_model)
            .exec(&*self.conn)
            .await?;
        Ok(result.last_insert_id)
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
  
    /// Deletes instruments and their associated identifiers.
    /// Identifiers or Derivative metadata owned by this instrument
    /// are assumed to be deleted by ON DELETE CASCADE.
    /// 
    /// # Arguments
    /// * `instr_dbids` - Vector of instrument IDs to delete
    /// * `txn` - Database transaction to use for the operations
    /// 
    /// # Returns
    /// * `Result<()>` - Success or error
    /// 
    /// # Errors
    /// * `anyhow::Error` - If any database operation fails
    async fn delete_instruments(
        &self,
        instr_dbids: Vec<i64>,
        txn: Option<&DatabaseTransaction>,
    ) -> Result<()> {
        if instr_dbids.is_empty() {
            return Ok(());
        }
        
        let mut l_txn = LocalTxn::new(&self.conn, txn).await?;

        // Delete the instruments themselves
        for instr_dbid in instr_dbids {
            Instruments::delete_by_id(instr_dbid)
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
}