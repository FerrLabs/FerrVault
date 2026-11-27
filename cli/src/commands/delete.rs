use std::io::{BufRead, IsTerminal, Write};

use anyhow::{Context, Result, anyhow};

use crate::api::ApiClient;

pub async fn run(client: &ApiClient, name: String, yes: bool) -> Result<()> {
    let vault = client.vault_slug().await?;
    let scope = client.describe_scope(&vault).await;

    if !yes {
        let stdin = std::io::stdin();
        if !stdin.is_terminal() {
            return Err(anyhow!(
                "delete refused: stdin is not a tty. Pass --yes to skip the confirmation prompt."
            ));
        }
        eprint!(
            "ferrvault: DELETE `{name}` from {scope} — this is a soft-delete (versions are kept). Type the secret name to confirm: "
        );
        std::io::stderr().flush().ok();
        let mut line = String::new();
        stdin
            .lock()
            .read_line(&mut line)
            .context("reading confirmation")?;
        if line.trim() != name {
            return Err(anyhow!("confirmation mismatch — aborted"));
        }
    }

    client
        .delete_secret(&vault, &name)
        .await
        .with_context(|| format!("deleting `{name}`"))?;
    eprintln!("ferrvault: deleted `{name}` from {scope}.");
    Ok(())
}
