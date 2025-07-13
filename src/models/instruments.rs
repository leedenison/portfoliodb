use sea_orm::entity::prelude::*;

#[derive(Clone, Debug, PartialEq, DeriveEntityModel)]
#[sea_orm(table_name = "instruments")]
pub struct Model {
    #[sea_orm(primary_key)]
    pub dbid: i64,
    pub r#type: String,           // Using raw identifier for 'type'
    pub currency: Option<String>, // ISO 4217 currency code (e.g., USD, EUR, GBP) - nullable
}

#[derive(Copy, Clone, Debug, EnumIter, DeriveRelation)]
pub enum Relation {
    #[sea_orm(has_many = "super::identifiers::Entity")]
    Identifiers,
    #[sea_orm(has_one = "super::derivatives::Entity")]
    Derivatives,
    #[sea_orm(has_many = "super::transactions::Entity")]
    Transactions,
    #[sea_orm(has_many = "super::prices::Entity")]
    Prices,
}

impl ActiveModelBehavior for ActiveModel {}
