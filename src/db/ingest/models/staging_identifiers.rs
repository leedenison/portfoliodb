use crate::db::models;
use crate::portfolio_db::Identifier;
use anyhow::Result;
use sea_orm::entity::prelude::*;
use sea_orm::{ConnectionTrait, JoinType, NotSet, QueryFilter, QuerySelect, Set};
use serde::{Deserialize, Serialize};

#[derive(Clone, Debug, PartialEq, DeriveEntityModel, Serialize, Deserialize)]
#[sea_orm(table_name = "staging_identifiers")]
pub struct Model {
    #[sea_orm(primary_key)]
    #[serde(default)]
    pub dbid: i64,
    pub batch_dbid: i64,
    pub instrument_dbid: i64,
    pub source: String,
    pub namespace: String,
    pub domain: String,
    pub identifier: String,
}

impl From<Model> for Identifier {
    fn from(staging_identifier: Model) -> Self {
        Identifier {
            id: staging_identifier.dbid.to_string(),
            namespace: staging_identifier.namespace,
            domain: staging_identifier.domain,
            identifier: staging_identifier.identifier,
        }
    }
}

#[derive(Copy, Clone, Debug, EnumIter)]
pub enum Relation {
    Batch,
    ExistingIdentifier,
}

impl RelationTrait for Relation {
    fn def(&self) -> RelationDef {
        match self {
            Self::Batch => Entity::belongs_to(super::batches::Entity)
                .from(Column::BatchDbid)
                .to(super::batches::Column::Dbid)
                .into(),
            Self::ExistingIdentifier => Entity::belongs_to(models::identifiers::Entity)
                .from(Column::Namespace)
                .to(models::identifiers::Column::Namespace)
                .from(Column::Domain)
                .to(models::identifiers::Column::Domain)
                .from(Column::Identifier)
                .to(models::identifiers::Column::Id)
                .into(),
        }
    }
}

impl Related<super::batches::Entity> for Entity {
    fn to() -> RelationDef {
        Relation::Batch.def()
    }
}

impl Related<models::identifiers::Entity> for Entity {
    fn to() -> RelationDef {
        Relation::ExistingIdentifier.def()
    }
}

impl Entity {
    /// Finds all identifiers in the staging table that do not have a matching identifier in the database.
    ///
    /// # Returns
    /// * `Ok(Select<Entity>)` - A select statement for the identifiers
    /// * `Err(anyhow::Error)` - An error if the query fails
    pub fn find_new(batch_dbid: i64) -> Select<Entity> {
        Entity::find()
            .join(JoinType::LeftJoin, Relation::ExistingIdentifier.def())
            .filter(Column::BatchDbid.eq(batch_dbid))
            .filter(
                Expr::col((
                    models::identifiers::Entity,
                    models::identifiers::Column::Dbid,
                ))
                .is_null(),
            )
    }

    pub async fn all_new<E>(
        exec: &E,
        batch_dbid: i64,
    ) -> Result<Vec<Model>>
    where
      E: ConnectionTrait,
    {
        Ok(Entity::find_new(batch_dbid).all(exec).await?)
    }
}

impl ActiveModelBehavior for ActiveModel {}

impl ActiveModel {
    pub fn with_batch_dbid(mut self, batch_dbid: i64) -> Self {
        self.batch_dbid = Set(batch_dbid);
        self
    }

    pub fn with_instrument_dbid(mut self, instrument_dbid: i64) -> Self {
        self.instrument_dbid = Set(instrument_dbid);
        self
    }

    pub fn with_source(mut self, source: String) -> Self {
        self.source = Set(source);
        self
    }
}

impl From<Identifier> for ActiveModel {
    fn from(identifier: Identifier) -> Self {
        let Identifier {
            namespace,
            domain,
            identifier: id,
            ..
        } = identifier;

        ActiveModel {
            dbid: NotSet,
            batch_dbid: Set(0),
            instrument_dbid: Set(0),
            source: NotSet,
            namespace: Set(namespace),
            domain: Set(domain),
            identifier: Set(id),
        }
    }
}

