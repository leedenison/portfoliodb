use clap::Parser;
use tonic::transport::Server;
use tower_http::cors::{Any, CorsLayer};
use tracing::{Level, info};

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

    Server::builder()
        .layer(tonic_web::GrpcWebLayer::new())
        .layer(
            CorsLayer::new()
                .allow_origin(Any)
                .allow_methods(Any)
                .allow_headers(Any)
                .allow_credentials(true),
        )
        .add_service(PortfolioDbServer::new(portfoliodb::rpc::Service::new(
            args.database_url.clone(),
        )))
        .serve(addr)
        .await?;

    Ok(())
}
