use anyhow::Result;

use crate::cli::{Cli, Command};

mod connection;
mod delete;
mod exec;
mod get;
mod list;
mod login;
mod logout;
mod select;
mod set;
mod whoami;

pub async fn dispatch(cli: Cli) -> Result<()> {
    match &cli.command {
        Command::Login(args) => return login::run(args, &cli).await,
        Command::Logout => return logout::run(&cli).await,
        _ => {}
    }

    let client = connection::build_client(&cli)?;
    match cli.command {
        Command::Login(_) | Command::Logout => unreachable!(),
        Command::Whoami { json } => whoami::run(&client, json).await,
        Command::List { json } => list::run(&client, json).await,
        Command::Get {
            name,
            all,
            names,
            format,
        } => get::run(&client, name, all, names, format).await,
        Command::Exec {
            names,
            all,
            only,
            argv,
        } => exec::run(&client, names, all, only, argv).await,
        Command::Set {
            name,
            value,
            stdin,
            from_file,
            update,
        } => set::run(&client, name, value, stdin, from_file, update).await,
        Command::Delete { name, yes } => delete::run(&client, name, yes).await,
    }
}
