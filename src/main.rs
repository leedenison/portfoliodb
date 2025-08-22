use clap::Parser;
use tonic::transport::Server;
use tower_http::cors::{CorsLayer, AllowOrigin};
use http::header::{ACCEPT, CONTENT_TYPE, ORIGIN, AUTHORIZATION, USER_AGENT, HeaderName};
use tracing::{Level, info};
use std::time::Duration;

use portfoliodb::portfolio_db_server::PortfolioDbServer;
use portfoliodb::auth::AuthLayer;
use portfoliodb::db::DatabaseManager;
use portfoliodb::id_resolvers::{OpenfigiResolver, SimpleResolver};

use sea_orm::DatabaseConnection;

#[derive(Parser)]
#[command(name = "portfoliodb")]
#[command(about = "PortfolioDB gRPC Server")]
struct Args {
    /// Port to listen on
    #[arg(short, long, default_value = "50001")]
    port: u16,
    /// Database URL to connect to
    #[arg(long, default_value = "postgres://localhost/portfoliodb")]
    database_url: String,
}

#[tokio::main]
async fn main() -> Result<(), Box<dyn std::error::Error>> {
    tracing_subscriber::fmt().with_max_level(Level::INFO).init();

    let args = Args::parse();
    let addr = format!("[::]:{}", args.port).parse()?;

    info!("Starting PortfolioDB server on port {}", args.port);

    let db_mgr = DatabaseManager::<DatabaseConnection>::new(&args.database_url).await?;
    let db = std::sync::Arc::new(db_mgr);

    let id_resolver = Box::new(SimpleResolver::new(db.clone(), Box::new(OpenfigiResolver::new())));
    
    let cors = CorsLayer::new()
        .allow_origin(AllowOrigin::mirror_request())
        .allow_methods([http::Method::GET, http::Method::POST, http::Method::OPTIONS])
        .allow_headers([
            ACCEPT,
            CONTENT_TYPE,
            ORIGIN,
            AUTHORIZATION,
            USER_AGENT,
            HeaderName::from_static("x-grpc-web"),
            HeaderName::from_static("grpc-timeout"),
            HeaderName::from_static("x-auth-request-user"),
            HeaderName::from_static("x-auth-request-email"),
            HeaderName::from_static("x-auth-request-uid"),
        ])
        .allow_credentials(true)
        .max_age(Duration::from_secs(600));

    // Create authentication layer
    let auth_layer = AuthLayer::new(db.clone());

    Server::builder()
        .accept_http1(true)
        .layer(auth_layer)
        .layer(cors)
        .layer(tonic_web::GrpcWebLayer::new())
        .add_service(PortfolioDbServer::new(portfoliodb::rpc::Service::new(db, id_resolver)))
        .serve(addr)
        .await?;

    Ok(())
}
