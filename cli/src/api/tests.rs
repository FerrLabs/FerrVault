use std::sync::Arc;

use chrono::{Duration, Utc};
use serde_json::json;

use super::*;
use crate::oauth::idp::IdpClient;
use crate::session::{MemorySessionStore, Session};
use crate::testing::{MockResponse, MockServer};

fn me_body() -> serde_json::Value {
    json!({"token_id":"t","vault_id":"v","vault_slug":"prod","role":"admin","token_label":"l","expires_at":null})
}

fn bearer(r: &crate::testing::Recorded) -> String {
    r.header("authorization").unwrap_or_default().to_owned()
}

async fn rotating_idp() -> MockServer {
    MockServer::start(|_| {
        MockResponse::json(
            200,
            &json!({"token":"new-access","refresh_token":"new-refresh","expires_in":900}),
        )
    })
    .await
}

fn session_client(
    api: &MockServer,
    idp: &MockServer,
    expires_in: i64,
    vault: Option<&str>,
) -> (ApiClient, Arc<MemorySessionStore>) {
    let store = Arc::new(MemorySessionStore::default());
    let session = Session {
        idp_url: idp.url.to_string(),
        access_token: Zeroizing::new("old-access".into()),
        refresh_token: Zeroizing::new("old-refresh".into()),
        expires_at: Utc::now() + Duration::seconds(expires_in),
        account: None,
        org: Some("acme".into()),
        vault: vault.map(str::to_owned),
        environment: vault.map(|_| "default".to_owned()),
    };
    let auth = SessionAuth::new(
        session,
        IdpClient::with_http(idp.url.clone(), crate::testing::http_client()),
        store.clone(),
    );
    let client = ApiClient::with_http(
        ApiConfig {
            base_url: api.url.clone(),
            auth: Auth::Session(Arc::new(auth)),
        },
        crate::testing::http_client(),
    );
    (client, store)
}

#[tokio::test]
async fn sat_requests_are_unchanged_bearer_without_org_header() {
    let api = MockServer::start(|_| MockResponse::json(200, &me_body())).await;
    let client = ApiClient::with_http(
        ApiConfig {
            base_url: api.url.clone(),
            auth: Auth::Sat(Zeroizing::new("fvsat_abc".into())),
        },
        crate::testing::http_client(),
    );
    assert_eq!(client.vault_slug().await.unwrap(), "prod");
    let sent = api.requests();
    assert_eq!(sent.len(), 1);
    assert_eq!(sent[0].path, "/operator/me");
    assert_eq!(bearer(&sent[0]), "Bearer fvsat_abc");
    assert!(sent[0].header("x-ferrvault-org").is_none());
}

#[tokio::test]
async fn sat_401_is_final_and_never_attempts_a_refresh() {
    let api = MockServer::start(|_| MockResponse::json(401, &json!({}))).await;
    let client = ApiClient::with_http(
        ApiConfig {
            base_url: api.url.clone(),
            auth: Auth::Sat(Zeroizing::new("fvsat_abc".into())),
        },
        crate::testing::http_client(),
    );
    let e = format!("{:#}", client.me().await.unwrap_err());
    assert!(e.contains("401"), "{e}");
    assert_eq!(api.requests().len(), 1);
}

#[tokio::test]
async fn session_requests_carry_the_jwt_and_org_header() {
    let api = MockServer::start(|_| MockResponse::json(200, &json!([]))).await;
    let idp = rotating_idp().await;
    let (client, _) = session_client(&api, &idp, 900, Some("prod"));
    client.inventory().await.unwrap();
    let sent = api.requests();
    assert_eq!(bearer(&sent[0]), "Bearer old-access");
    assert_eq!(sent[0].header("x-ferrvault-org"), Some("acme"));
    assert!(idp.requests().is_empty());
}

#[tokio::test]
async fn expired_session_refreshes_before_the_request_and_persists() {
    let api = MockServer::start(|_| MockResponse::json(200, &json!([]))).await;
    let idp = rotating_idp().await;
    let (client, store) = session_client(&api, &idp, -10, Some("prod"));
    client.inventory().await.unwrap();
    assert_eq!(bearer(&api.requests()[0]), "Bearer new-access");
    assert_eq!(store.saved().unwrap().refresh_token.as_str(), "new-refresh");
}

#[tokio::test]
async fn a_401_refreshes_once_and_replays_the_request() {
    let api = MockServer::start(|r| {
        if bearer(r) == "Bearer new-access" {
            MockResponse::json(200, &json!([]))
        } else {
            MockResponse::json(401, &json!({}))
        }
    })
    .await;
    let idp = rotating_idp().await;
    let (client, store) = session_client(&api, &idp, 900, Some("prod"));
    client.inventory().await.unwrap();
    assert_eq!(api.requests().len(), 2);
    assert_eq!(idp.requests().len(), 1);
    assert_eq!(store.saved().unwrap().access_token.as_str(), "new-access");
}

#[tokio::test]
async fn a_401_after_refresh_is_reported_without_looping() {
    let api = MockServer::start(|_| MockResponse::json(401, &json!({}))).await;
    let idp = rotating_idp().await;
    let (client, _) = session_client(&api, &idp, 900, Some("prod"));
    assert!(
        client
            .inventory()
            .await
            .unwrap_err()
            .to_string()
            .contains("401")
    );
    assert_eq!(api.requests().len(), 2);
    assert_eq!(idp.requests().len(), 1);
}

#[tokio::test]
async fn session_without_a_vault_asks_for_one() {
    let api = MockServer::start(|_| MockResponse::json(200, &json!([]))).await;
    let idp = rotating_idp().await;
    let (client, _) = session_client(&api, &idp, 900, None);
    let err = client.vault_slug().await.unwrap_err().to_string();
    assert!(err.contains("--vault"), "{err}");
    assert!(api.requests().is_empty());
}

fn secret_json(name: &str) -> serde_json::Value {
    json!({"name": name, "value": format!("v-{name}"), "current_version": 3, "updated_at": "2026-11-25T10:00:00Z"})
}

const SECRETS: &str = "/vaults/prod/environments/default/secrets";

#[tokio::test]
async fn session_inventory_reads_the_environment_secret_list() {
    let api = MockServer::start(|_| MockResponse::json(200, &json!([secret_json("A")]))).await;
    let idp = rotating_idp().await;
    let (client, _) = session_client(&api, &idp, 900, Some("prod"));
    let items = client.inventory().await.unwrap();
    assert_eq!((items[0].name.as_str(), items[0].version), ("A", 3));
    assert_eq!(api.requests()[0].path, SECRETS);
}

#[tokio::test]
async fn session_reveal_of_names_is_one_batch_call() {
    let api = MockServer::start(|r| {
        let names = r.json()["names"].clone();
        let hits: Vec<_> = names
            .as_array()
            .unwrap()
            .iter()
            .map(|n| secret_json(n.as_str().unwrap()))
            .collect();
        MockResponse::json(200, &json!(hits))
    })
    .await;
    let idp = rotating_idp().await;
    let (client, _) = session_client(&api, &idp, 900, Some("prod"));
    let names = vec!["A".to_owned(), "B".to_owned()];
    let resp = client.reveal("prod", Some(&names)).await.unwrap();
    assert_eq!(resp.hits.len(), 2);
    assert_eq!(resp.hits[1].value, "v-B");
    let sent = api.requests();
    assert_eq!(sent.len(), 1);
    assert_eq!(sent[0].method_path(), format!("POST {SECRETS}:reveal"));
    assert_eq!(sent[0].json(), json!({"names": ["A", "B"]}));
}

#[tokio::test]
async fn session_reveal_all_lists_first_and_chunks_of_one_hundred() {
    let names: Vec<String> = (0..250).map(|i| format!("S{i:03}")).collect();
    let listed = names.clone();
    let api = MockServer::start(move |r| {
        if r.path == SECRETS {
            let items: Vec<_> = listed.iter().map(|n| secret_json(n)).collect();
            return MockResponse::json(200, &json!(items));
        }
        let asked = r.json()["names"].as_array().unwrap().clone();
        assert!(asked.len() <= 100);
        let hits: Vec<_> = asked
            .iter()
            .map(|n| secret_json(n.as_str().unwrap()))
            .collect();
        MockResponse::json(200, &json!(hits))
    })
    .await;
    let idp = rotating_idp().await;
    let (client, _) = session_client(&api, &idp, 900, Some("prod"));
    let resp = client.reveal("prod", None).await.unwrap();
    assert_eq!(resp.hits.len(), 250);
    let sizes: Vec<usize> = api
        .requests_to(&format!("{SECRETS}:reveal"))
        .iter()
        .map(|r| r.json()["names"].as_array().unwrap().len())
        .collect();
    assert_eq!(sizes, [100, 100, 50]);
}

#[tokio::test]
async fn session_writes_use_the_environment_routes() {
    let api = MockServer::start(|r| {
        if r.method_path().starts_with("DELETE") {
            MockResponse::empty(204)
        } else {
            MockResponse::json(200, &json!({}))
        }
    })
    .await;
    let idp = rotating_idp().await;
    let (client, _) = session_client(&api, &idp, 900, Some("prod"));
    client.create_secret("prod", "NEW", "x").await.unwrap();
    client.update_secret("prod", "NEW", "y").await.unwrap();
    client.delete_secret("prod", "NEW").await.unwrap();
    let sent = api.requests();
    assert_eq!(sent[0].method_path(), format!("POST {SECRETS}"));
    assert_eq!(sent[0].json(), json!({"name": "NEW", "value": "x"}));
    assert_eq!(sent[1].method_path(), format!("PUT {SECRETS}/NEW"));
    assert_eq!(sent[1].json(), json!({"value": "y"}));
    assert_eq!(sent[2].method_path(), format!("DELETE {SECRETS}/NEW"));
    assert!(
        sent.iter()
            .all(|r| r.header("x-ferrvault-org") == Some("acme"))
    );
}

#[tokio::test]
async fn memberships_become_the_org_choices() {
    let api = MockServer::start(|_| {
        MockResponse::json(
            200,
            &json!({"user_id":"u","handle":"dev","org_id":null,"org_role":null,
                    "memberships":[{"org_id":"org-1","role":"owner"},{"org_id":"org-2","role":null}]}),
        )
    })
    .await;
    let idp = rotating_idp().await;
    let (client, _) = session_client(&api, &idp, 900, Some("prod"));
    let orgs = client.list_orgs().await.unwrap();
    assert_eq!(orgs.len(), 2);
    assert_eq!(orgs[0].slug, "org-1");
    assert_eq!(api.requests()[0].path, "/me");
}

#[tokio::test]
async fn session_without_an_environment_makes_no_secret_request() {
    let api = MockServer::start(|_| MockResponse::json(200, &json!([]))).await;
    let idp = rotating_idp().await;
    let (client, _) = session_client(&api, &idp, 900, None);
    assert!(client.inventory().await.is_err());
    assert!(client.reveal("x", None).await.is_err());
    assert!(api.requests().is_empty());
}

#[tokio::test]
async fn sat_requests_keep_using_operator_routes() {
    let api = MockServer::start(|r| match r.path.as_str() {
        "/operator/secrets/reveal" => MockResponse::json(
            200,
            &json!({"hits":[{"name":"A","value":"1","version":1,"fetched_at":"2026-11-25T10:00:00Z"}],"missing":["B"]}),
        ),
        _ => MockResponse::json(200, &json!([])),
    })
    .await;
    let client = ApiClient::with_http(
        ApiConfig {
            base_url: api.url.clone(),
            auth: Auth::Sat(Zeroizing::new("fvsat_abc".into())),
        },
        crate::testing::http_client(),
    );
    let resp = client
        .reveal("prod", Some(&["A".to_owned(), "B".to_owned()]))
        .await
        .unwrap();
    assert_eq!(resp.missing, ["B"]);
    client.inventory().await.unwrap();
    let sent = api.requests();
    assert_eq!(sent[0].json(), json!({"vault":"prod","names":["A","B"]}));
    assert_eq!(sent[1].path, "/operator/secrets/list");
}

const HOSTILE: &str = "../../staging/secrets/X?a=b#frag";

#[tokio::test]
async fn secret_names_stay_inside_their_path_segment() {
    let api = MockServer::start(|_| MockResponse::json(200, &json!({}))).await;
    let idp = rotating_idp().await;
    let (client, _) = session_client(&api, &idp, 900, Some("prod"));
    client.update_secret("prod", HOSTILE, "v").await.unwrap();
    client.delete_secret("prod", "a/b").await.ok();
    let sent = api.requests();
    assert_eq!(
        sent[0].path,
        format!("{SECRETS}/..%2F..%2Fstaging%2Fsecrets%2FX%3Fa=b%23frag")
    );
    assert_eq!(sent[1].path, format!("{SECRETS}/a%2Fb"));
}

#[tokio::test]
async fn dot_segments_are_refused_before_any_request() {
    let api = MockServer::start(|_| MockResponse::json(200, &json!({}))).await;
    let idp = rotating_idp().await;
    let (client, _) = session_client(&api, &idp, 900, Some("prod"));
    assert!(client.update_secret("prod", "..", "v").await.is_err());
    assert!(client.delete_secret("prod", ".").await.is_err());
    assert!(api.requests().is_empty());
}

#[tokio::test]
async fn sat_names_are_encoded_too() {
    let api = MockServer::start(|_| MockResponse::json(200, &json!({}))).await;
    let client = ApiClient::with_http(
        ApiConfig {
            base_url: api.url.clone(),
            auth: Auth::Sat(Zeroizing::new("fvsat_abc".into())),
        },
        crate::testing::http_client(),
    );
    client.update_secret("prod", HOSTILE, "v").await.unwrap();
    client.delete_secret("prod", "a b?").await.unwrap();
    let sent = api.requests();
    assert_eq!(
        sent[0].path,
        "/operator/secrets/..%2F..%2Fstaging%2Fsecrets%2FX%3Fa=b%23frag"
    );
    assert_eq!(sent[1].path, "/operator/secrets/a%20b%3F?vault=prod");
}

#[tokio::test]
async fn names_with_tab_lf_or_cr_are_refused_before_any_request() {
    let api = MockServer::start(|_| MockResponse::json(200, &json!({}))).await;
    let idp = rotating_idp().await;
    let (client, _) = session_client(&api, &idp, 900, Some("prod"));
    for bad in ["A\tB", "A\nB", "A\rB"] {
        let update = client.update_secret("prod", bad, "v").await.unwrap_err();
        assert!(update.to_string().contains("not a valid name"), "{bad:?}");
        assert!(
            client.update_secret("prod", bad, "v").await.is_err(),
            "{bad:?}"
        );
        assert!(client.delete_secret("prod", bad).await.is_err(), "{bad:?}");
    }
    assert!(api.requests().is_empty());
}

#[tokio::test]
async fn sat_names_with_control_characters_are_refused_too() {
    let api = MockServer::start(|_| MockResponse::json(200, &json!({}))).await;
    let client = ApiClient::with_http(
        ApiConfig {
            base_url: api.url.clone(),
            auth: Auth::Sat(Zeroizing::new("fvsat_abc".into())),
        },
        crate::testing::http_client(),
    );
    for bad in ["A\tB", "A\nB", "A\rB"] {
        assert!(
            client.update_secret("prod", bad, "v").await.is_err(),
            "{bad:?}"
        );
        assert!(client.delete_secret("prod", bad).await.is_err(), "{bad:?}");
    }
    assert!(api.requests().is_empty());
}
