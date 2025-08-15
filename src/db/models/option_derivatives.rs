use chrono::{DateTime, Utc};
use sea_orm::entity::prelude::*;

#[derive(Clone, Debug, PartialEq, DeriveEntityModel)]
#[sea_orm(table_name = "option_derivatives")]
pub struct Model {
    #[sea_orm(primary_key)]
    pub dbid: i64,
    pub derivative_dbid: i64,
    pub expiration_date: DateTime<Utc>,
    pub put_call: String,
    pub strike_price: f64,
    pub option_style: Option<String>,
}

#[derive(Copy, Clone, Debug, EnumIter, DeriveRelation)]
pub enum Relation {
    #[sea_orm(
        belongs_to = "super::derivatives::Entity",
        from = "Column::DerivativeDbid",
        to = "super::derivatives::Column::Dbid"
    )]
    Derivative,
}

impl Related<super::derivatives::Entity> for Entity {
    fn to() -> RelationDef {
        Relation::Derivative.def()
    }
}

impl ActiveModelBehavior for ActiveModel {}
