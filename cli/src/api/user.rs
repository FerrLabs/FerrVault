use anyhow::Result;
use reqwest::Method;
use serde::{Deserialize, Serialize};

use super::{ApiClient, BulkReadHit, BulkReadResponse, Choice, InventoryItem, handle};
use crate::session::{Scope, SessionAuth};

const REVEAL_BATCH: usize = 100;

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

#[derive(Serialize)]
struct NamesBody<'a> {
    names: &'a [String],
}

#[derive(Serialize)]
struct CreateBody<'a> {
    name: &'a str,
    value: &'a str,
}

#[derive(Serialize)]
struct UpdateBody<'a> {
    value: &'a str,
}

fn secrets_route<'a>(scope: &'a Scope, tail: &[&'a str]) -> Vec<&'a str> {
    let mut route = vec![
        "vaults",
        scope.vault.as_str(),
        "environments",
        scope.environment.as_str(),
    ];
    route.extend_from_slice(tail);
    route
}

impl ApiClient {
    pub async fn user_me(&self) -> Result<UserMe> {
        self.get_json(&["me"]).await
    }

    pub async fn list_orgs(&self) -> Result<Vec<Choice>> {
        let me = self.user_me().await?;
        Ok(me
            .memberships
            .into_iter()
            .map(|m| Choice {
                slug: m.org_id,
                name: None,
                detail: m.role,
            })
            .collect())
    }

    pub async fn list_vaults(&self) -> Result<Vec<Choice>> {
        self.get_json(&["vaults"]).await
    }

    pub async fn list_environments(&self, vault: &str) -> Result<Vec<Choice>> {
        self.get_json(&["vaults", vault, "environments"]).await
    }

    pub(super) async fn user_inventory(&self, session: &SessionAuth) -> Result<Vec<InventoryItem>> {
        let scope = session.scope().await?;
        self.get_json(&secrets_route(&scope, &["secrets"])).await
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
                .get_json::<Vec<InventoryItem>>(&secrets_route(&scope, &["secrets"]))
                .await?
                .into_iter()
                .map(|i| i.name)
                .collect(),
        };
        let route = secrets_route(&scope, &["secrets:reveal"]);
        let mut hits: Vec<BulkReadHit> = Vec::with_capacity(wanted.len());
        for chunk in wanted.chunks(REVEAL_BATCH) {
            let resp = self
                .send(Method::POST, &route, &[], Some(&NamesBody { names: chunk }))
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
        let resp = self
            .send(
                Method::POST,
                &secrets_route(&scope, &["secrets"]),
                &[],
                Some(&CreateBody { name, value }),
            )
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
        let resp = self
            .send(
                Method::PUT,
                &secrets_route(&scope, &["secrets", name]),
                &[],
                Some(&UpdateBody { value }),
            )
            .await?;
        handle(resp).await
    }

    pub(super) async fn user_delete(&self, session: &SessionAuth, name: &str) -> Result<()> {
        let scope = session.scope().await?;
        let resp = self
            .send(
                Method::DELETE,
                &secrets_route(&scope, &["secrets", name]),
                &[],
                super::NO_BODY,
            )
            .await?;
        if resp.status().is_success() {
            return Ok(());
        }
        handle::<serde_json::Value>(resp).await.map(|_| ())
    }
}
