use std::path::PathBuf;

use clap::{Parser, Subcommand};

#[derive(Debug, Parser)]
#[command(
    name = "ferrvault",
    version,
    about = "Consumer CLI for FerrVault.",
    long_about = "Fetch secrets from your FerrVault tenant and inject them into a process without writing them to disk."
)]
pub struct Cli {
    #[arg(long, env = "FERRVAULT_URL", global = true)]
    pub api_url: Option<String>,

    #[arg(long, env = "FERRVAULT_TOKEN", global = true, hide_env_values = true)]
    pub token: Option<String>,

    #[arg(long, env = "FERRVAULT_CA_CERT", global = true)]
    pub ca_cert: Option<PathBuf>,

    #[arg(long, env = "FERRVAULT_CLIENT_CERT", global = true)]
    pub client_cert: Option<PathBuf>,

    #[arg(
        long,
        env = "FERRVAULT_CLIENT_KEY",
        global = true,
        requires = "client_cert"
    )]
    pub client_key: Option<PathBuf>,

    #[arg(long, env = "FERRVAULT_PIN_SHA256", global = true)]
    pub pin_sha256: Option<String>,

    #[arg(long, global = true)]
    pub insecure_skip_verify: bool,

    #[command(subcommand)]
    pub command: Command,
}

#[derive(Debug, Subcommand)]
pub enum Command {
    Login {
        #[arg(long)]
        url: Option<String>,
    },
    Logout,
    Whoami {
        #[arg(long)]
        json: bool,
    },
    Get {
        name: Option<String>,

        #[arg(long, conflicts_with = "name")]
        all: bool,

        #[arg(long, value_delimiter = ',', conflicts_with = "name")]
        names: Vec<String>,

        #[arg(long, default_value = "value")]
        format: OutputFormat,
    },
    List {
        #[arg(long)]
        json: bool,
    },
    Exec {
        #[arg(long, value_delimiter = ',')]
        names: Vec<String>,

        #[arg(long)]
        all: bool,

        #[arg(long, value_delimiter = ',')]
        only: Vec<String>,

        #[arg(trailing_var_arg = true, required = true)]
        argv: Vec<String>,
    },
    Set {
        name: String,

        value: Option<String>,

        #[arg(long, conflicts_with = "value")]
        stdin: bool,

        #[arg(long, conflicts_with_all = ["value", "stdin"])]
        from_file: Option<PathBuf>,

        #[arg(long)]
        update: bool,
    },
    Delete {
        name: String,

        #[arg(long, short)]
        yes: bool,
    },
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, clap::ValueEnum)]
pub enum OutputFormat {
    Value,
    Env,
    Json,
    Dotenv,
}
