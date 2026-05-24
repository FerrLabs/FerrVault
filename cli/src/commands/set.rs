use std::io::Read;
use std::path::PathBuf;

use anyhow::{Context, Result, anyhow};
use zeroize::Zeroizing;

use crate::api::ApiClient;

pub async fn run(
    client: &ApiClient,
    name: String,
    value: Option<String>,
    stdin: bool,
    from_file: Option<PathBuf>,
    update: bool,
) -> Result<()> {
    let me = client.me().await?;

    let plaintext: Zeroizing<String> = match (value, stdin, from_file) {
        (Some(v), false, None) => Zeroizing::new(v),
        (None, true, None) => Zeroizing::new(read_stdin()?),
        (None, false, Some(path)) => Zeroizing::new(read_file(&path)?),
        (None, false, None) => {
            return Err(anyhow!(
                "no value: pass VALUE positional, --stdin, or --from-file <path>"
            ));
        }
        _ => {
            return Err(anyhow!(
                "--stdin, --from-file and VALUE are mutually exclusive"
            ));
        }
    };

    if plaintext.is_empty() {
        return Err(anyhow!("refusing to write an empty secret value"));
    }

    let result = if update {
        client
            .update_secret(&me.vault_slug, &name, plaintext.as_str())
            .await
    } else {
        match client
            .create_secret(&me.vault_slug, &name, plaintext.as_str())
            .await
        {
            Ok(v) => Ok(v),
            Err(e) => {
                let msg = e.to_string();
                if msg.contains("SECRET_EXISTS") || msg.contains("409") {
                    return Err(anyhow!(
                        "secret `{name}` already exists. Pass --update to rotate it."
                    ));
                }
                Err(e)
            }
        }
    };
    result.with_context(|| {
        if update {
            format!("rotating `{name}`")
        } else {
            format!("creating `{name}`")
        }
    })?;
    eprintln!(
        "ferrvault: {} `{name}` in vault `{}`.",
        if update { "rotated" } else { "created" },
        me.vault_slug
    );
    Ok(())
}

fn read_stdin() -> Result<String> {
    let mut buf = String::new();
    std::io::stdin()
        .read_to_string(&mut buf)
        .context("reading value from stdin")?;
    while buf.ends_with('\n') || buf.ends_with('\r') {
        buf.pop();
    }
    Ok(buf)
}

fn read_file(path: &std::path::Path) -> Result<String> {
    let bytes = std::fs::read(path).with_context(|| format!("reading {}", path.display()))?;
    String::from_utf8(bytes).context("file content is not valid UTF-8")
}
