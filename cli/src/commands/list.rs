use anyhow::Result;

use crate::api::ApiClient;

pub async fn run(client: &ApiClient, as_json: bool) -> Result<()> {
    let items = client.inventory().await?;
    if as_json {
        println!("{}", serde_json::to_string_pretty(&items)?);
        return Ok(());
    }
    if items.is_empty() {
        eprintln!("ferrvault: vault is empty");
        return Ok(());
    }
    let name_w = items.iter().map(|i| i.name.len()).max().unwrap_or(4);
    println!("{:<name_w$}  v   updated_at", "NAME", name_w = name_w);
    for i in &items {
        println!(
            "{:<name_w$}  {:<3} {}",
            i.name,
            i.version,
            i.updated_at.to_rfc3339(),
            name_w = name_w
        );
    }
    Ok(())
}
