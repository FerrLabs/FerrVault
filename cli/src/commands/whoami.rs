use anyhow::Result;

use crate::api::ApiClient;

pub async fn run(client: &ApiClient, as_json: bool) -> Result<()> {
    if let Some(summary) = client.session_summary().await {
        let user = client.user_me().await?;
        if as_json {
            let out = serde_json::json!({ "user": user, "session": summary });
            println!("{}", serde_json::to_string_pretty(&out)?);
        } else {
            println!("user        {}", user.handle);
            println!("org         {}", summary.org.as_deref().unwrap_or("-"));
            println!("org_role    {}", user.org_role.as_deref().unwrap_or("-"));
            println!("vault       {}", summary.vault.as_deref().unwrap_or("-"));
            println!(
                "environment {}",
                summary.environment.as_deref().unwrap_or("-")
            );
            println!("expires_at  {}", summary.expires_at.to_rfc3339());
        }
        return Ok(());
    }
    let me = client.me().await?;
    if as_json {
        println!("{}", serde_json::to_string_pretty(&me)?);
    } else {
        let expires = me
            .expires_at
            .map_or_else(|| "never".to_string(), |t| t.to_rfc3339());
        println!("token_id    {}", me.token_id);
        println!("vault       {} ({})", me.vault_slug, me.vault_id);
        println!("role        {:?}", me.role);
        println!("label       {}", me.token_label);
        println!("expires_at  {expires}");
    }
    Ok(())
}
