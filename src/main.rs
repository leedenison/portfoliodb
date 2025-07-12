use clap::Parser;
use tonic::transport::Server;
use tracing::{info, Level};

use portfoliodb::portfolio_db_server::PortfolioDbServer;
use portfoliodb::rpc::PortfolioDBService;

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
    tracing_subscriber::fmt()
        .with_max_level(Level::INFO)
        .init();

    let args = Args::parse();
    let addr = format!("[::]:{}", args.port).parse()?;
    
    info!("Starting PortfolioDB server on port {}", args.port);
    
    // Create and start the server
    let service = PortfolioDBService::new(args.database_url.clone());
    
    Server::builder()
        .add_service(PortfolioDbServer::new(service))
        .serve(addr)
        .await?;
    
    Ok(())
} 