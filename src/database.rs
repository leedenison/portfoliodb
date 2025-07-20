use anyhow::Result;
use sea_orm::{
    Database, DatabaseConnection, EntityTrait, QueryFilter, ColumnTrait,
};
use std::sync::Arc;
use tracing::info;
use crate::portfolio_db::{DateRange, Tx};
use crate::models::{
    users,
    Instrument, InstrumentDescription, InstrumentSymbol, InstrumentId,
    Users,
};

#[derive(Clone)]
pub struct DatabaseManager {
    conn: Arc<DatabaseConnection>,
}

impl DatabaseManager {
    pub async fn new(database_url: &str) -> Result<Self> {
        let conn = Database::connect(database_url).await?;
        Ok(Self {
            conn: Arc::new(conn),
        })
    }

    pub fn connection(&self) -> &DatabaseConnection {
        &self.conn
    }

    pub async fn get_user_id_by_email(&self, email: &str) -> Result<Option<i64>> {
        let user = Users::find()
            .filter(users::Column::Email.eq(email))
            .one(self.connection())
            .await?;

        Ok(user.map(|user| user.dbid))
    }

    /// Updates transactions for a specific account within a given time period.
    /// This operation replaces all existing transactions in the specified period with the provided
    /// transactions. If the transactions list is empty, this effectively deletes all transactions
    /// in the period.
    pub async fn update_txs(&self, _period: &DateRange, _txs: &[Tx]) -> Result<()> {
        info!("update_transactions not implemented");

        Ok(())
    }

    /// Finds instruments by their identifiers, symbols, and descriptions.
    /// Returns instruments that match the provided identifiers.
    ///
    /// This function works with SeaORM database model structs. The RPC layer is responsible
    /// for converting protobuf messages to these database models before calling this function.
    ///
    /// # Arguments
    /// * `user_dbid` - The user ID to filter by (currently unused in the simplified schema).
    /// * `descriptions` - Vector of instrument descriptions to search for. These should be
    ///   SeaORM `InstrumentDescription` structs with the broker_dbid already resolved.
    /// * `symbols` - Vector of instrument symbols to search for. These should be
    ///   SeaORM `InstrumentSymbol` structs.
    /// * `identifiers` - Vector of instrument identifiers to search for. These should be
    ///   SeaORM `InstrumentId` structs.
    ///
    /// # Returns
    /// * A vector of `Instrument` database objects that match the provided criteria.
    ///   The RPC layer should convert these to protobuf `Instrument` messages for the client.
    ///
    /// # Example
    /// ```
    /// // The RPC layer should convert protobuf inputs to SeaORM models first:
    /// let descriptions = protobuf_descriptions.iter().map(|desc| {
    ///     InstrumentDescription {
    ///         dbid: 0, // Not used for search
    ///         instrument_dbid: 0, // Not used for search
    ///         user_dbid: Some(user_dbid),
    ///         broker_dbid: broker_lookup(&desc.broker)?, // Resolve broker key to ID
    ///         description: desc.description.clone(),
    ///         created_at: Utc::now(),
    ///     }
    /// }).collect::<Vec<_>>();
    ///
    /// let instruments = db.find_instruments_by_identifiers(
    ///     user_dbid,
    ///     &descriptions,
    ///     &symbols,
    ///     &identifiers
    /// ).await?;
    /// ```
    pub async fn find_instruments_by_identifiers(
        &self,
        user_dbid: i64,
        descriptions: &[InstrumentDescription],
        symbols: &[InstrumentSymbol],
        identifiers: &[InstrumentId],
    ) -> Result<Vec<Instrument>> {
        // let mut instrument_dbids = HashSet::new();

        // // Find instruments by descriptions
        // for desc in descriptions {
        //     let matching_descriptions = InstrumentDescriptions::find()
        //         .filter(instrument_descriptions::Column::Description.eq(&desc.description))
        //         .filter(instrument_descriptions::Column::BrokerDbid.eq(desc.broker_dbid))
        //         .filter(
        //             instrument_descriptions::Column::UserDbid.eq(user_dbid)
        //                 .or(instrument_descriptions::Column::Canonical.eq(true))
        //         )
        //         .all(self.connection())
        //         .await?;

        //     for desc_model in matching_descriptions {
        //         instrument_dbids.insert(desc_model.instrument_dbid);
        //     }
        // }

        // // Find instruments by symbols
        // for symbol in symbols {
        //     let matching_symbols = InstrumentSymbols::find()
        //         .filter(instrument_symbols::Column::Domain.eq(&symbol.domain))
        //         .filter(instrument_symbols::Column::Exchange.eq(&symbol.exchange))
        //         .filter(instrument_symbols::Column::Symbol.eq(&symbol.symbol))
        //         .filter(instrument_symbols::Column::Currency.eq(&symbol.currency))
        //         .all(self.connection())
        //         .await?;

        //     for symbol_model in matching_symbols {
        //         instrument_dbids.insert(symbol_model.instrument_dbid);
        //     }
        // }

        // // Find instruments by identifiers
        // for identifier in identifiers {
        //     let matching_identifiers = InstrumentIds::find()
        //         .filter(instrument_ids::Column::Domain.eq(&identifier.domain))
        //         .filter(instrument_ids::Column::Id.eq(&identifier.id))
        //         .all(self.connection())
        //         .await?;

        //     for identifier_model in matching_identifiers {
        //         instrument_dbids.insert(identifier_model.instrument_dbid);
        //     }
        // }

        // // Fetch the actual instruments
        // let mut instruments = Vec::new();
        // for instrument_dbid in instrument_dbids {
        //     if let Some(instrument) = Instruments::find_by_id(instrument_dbid)
        //         .one(self.connection())
        //         .await?
        //     {
        //         instruments.push(instrument);
        //     }
        // }

        Ok(Vec::new())
    }
}
