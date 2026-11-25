use anyhow::Result;
use reqwest::Method;
use serde::{Deserialize, Serialize};

use super::{ApiClient, BulkReadHit, BulkReadResponse, Choice, InventoryItem, handle};
use crate::session::{Scope, SessionAuth};

const REVEAL_BATCH: usize = 500;

#[derive(Debug, Clone, Deserialize, Serialize)]
pub struct Membership {
    pub org_id: String,
    pub role: Option<String>,
}

#[derive(Debug, Clone, Deserialize, Serialize)]
pub struct UserMe {
    pub user_id: String,
    pub handle: String,
    pub org_id: Option<String>,
    pub org_role: Option<String>,
    pub memberships: Vec<Membership>,
}

fn secrets_path(scope: &Scope) -> String {
    format!(
        "/vaults/{}/environments/{}/secrets",
        scope.vault, scope.environment
    )
}

impl ApiClient {
    pub async fn user_me(&self) -> Result<UserMe> {
        self.get_json("/me").await
    }

    pub async fn list_orgs(&self) -> Result<Vec<Choice>> {
        let me = self.user_me().await?;
        Ok(me
            .memberships
            .into_iter()
            .map(|m| Choice {
                slug: m.org_id,
                name: m.role,
            })
            .collect())
    }

    pub async fn list_vaults(&self) -> Result<Vec<Choice>> {
        self.get_json("/vaults").await
    }

    pub async fn list_environments(&self, vault: &str) -> Result<Vec<Choice>> {
        self.get_json(&format!("/vaults/{vault}/environments"))
            .await
    }

    pub(super) async fn user_inventory(&self, session: &SessionAuth) -> Result<Vec<InventoryItem>> {
        let scope = session.scope().await?;
        self.get_json(&secrets_path(&scope)).await
    }

    pub(super) async fn user_reveal(
        &self,
        session: &SessionAuth,
        names: Option<&[String]>,
    ) -> Result<BulkReadResponse> {
        let scope = session.scope().await?;
        let wanted: Vec<String> = match names {
            Some(n) => n.to_vec(),
            None => self
                .get_json::<Vec<InventoryItem>>(&secrets_path(&scope))
                .await?
                .into_iter()
                .map(|i| i.name)
                .collect(),
        };
        let path = format!("{}:reveal", secrets_path(&scope));
        let mut hits: Vec<BulkReadHit> = Vec::with_capacity(wanted.len());
        for chunk in wanted.chunks(REVEAL_BATCH) {
            let resp = self
                .send(
                    Method::POST,
                    &path,
                    &[],
                    Some(serde_json::json!({ "names": chunk })),
                )
                .await?;
            hits.extend(handle::<Vec<BulkReadHit>>(resp).await?);
        }
        Ok(BulkReadResponse {
            hits,
            missing: Vec::new(),
        })
    }

    pub(super) async fn user_create(
        &self,
        session: &SessionAuth,
        name: &str,
        value: &str,
    ) -> Result<serde_json::Value> {
        let scope = session.scope().await?;
        let body = serde_json::json!({ "name": name, "value": value });
        let resp = self
            .send(Method::POST, &secrets_path(&scope), &[], Some(body))
            .await?;
        handle(resp).await
    }

    pub(super) async fn user_update(
        &self,
        session: &SessionAuth,
        name: &str,
        value: &str,
    ) -> Result<serde_json::Value> {
        let scope = session.scope().await?;
        let body = serde_json::json!({ "value": value });
        let resp = self
            .send(
                Method::PUT,
                &format!("{}/{name}", secrets_path(&scope)),
                &[],
                Some(body),
            )
            .await?;
        handle(resp).await
    }

    pub(super) async fn user_delete(&self, session: &SessionAuth, name: &str) -> Result<()> {
        let scope = session.scope().await?;
        let resp = self
            .send(
                Method::DELETE,
                &format!("{}/{name}", secrets_path(&scope)),
                &[],
                None,
            )
            .await?;
        if resp.status().is_success() {
            return Ok(());
        }
        handle::<serde_json::Value>(resp).await.map(|_| ())
    }
}
