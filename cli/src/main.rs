use anyhow::Result;
use clap::Parser;

mod api;
mod cli;
mod commands;
mod storage;
mod tls;

#[tokio::main(flavor = "multi_thread", worker_threads = 2)]
async fn main() -> Result<()> {
    tracing_subscriber::fmt()
        .with_writer(std::io::stderr)
        .with_env_filter(
            tracing_subscriber::EnvFilter::try_from_env("FERRVAULT_LOG")
                .unwrap_or_else(|_| tracing_subscriber::EnvFilter::new("warn")),
        )
        .init();

    let cli = cli::Cli::parse();
    commands::dispatch(cli).await
}
