use clap::Parser;
use tonic::transport::Server;
use tower_http::cors::{CorsLayer, AllowOrigin};
use http::header::{ACCEPT, CONTENT_TYPE, ORIGIN, AUTHORIZATION, USER_AGENT, HeaderName};
use http::Method;
use tracing::{Level, info};
use std::time::Duration;

use portfoliodb::portfolio_db_server::PortfolioDbServer;

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
    // Initialize logging
    tracing_subscriber::fmt().with_max_level(Level::INFO).init();

    let args = Args::parse();
    let addr = format!("[::]:{}", args.port).parse()?;

    info!("Starting PortfolioDB server on port {}", args.port);

    let cors = CorsLayer::new()
        .allow_origin(AllowOrigin::mirror_request()) // or specify a static origin if needed
        .allow_methods([http::Method::GET, http::Method::POST, http::Method::OPTIONS])
        .allow_headers([
            ACCEPT,
            CONTENT_TYPE,
            ORIGIN,
            AUTHORIZATION,
            USER_AGENT,
            // gRPC-Web specific headers
            HeaderName::from_static("x-grpc-web"),
            HeaderName::from_static("grpc-timeout"),
            HeaderName::from_static("x-auth-request-user"),
            HeaderName::from_static("x-auth-request-email"),
            HeaderName::from_static("x-auth-request-uid"),
        ])
        .allow_credentials(true)
        .max_age(Duration::from_secs(600));

    Server::builder()
        .accept_http1(true)
        .layer(tonic_web::GrpcWebLayer::new())
        .layer(cors)
        .add_service(PortfolioDbServer::new(portfoliodb::rpc::Service::new(
            args.database_url.clone(),
        )))
        .serve(addr)
        .await?;

    Ok(())
}
