# Changelog

All notable changes to `ferrvault-operator` will be documented here.

The format is based on [Keep a Changelog](https://keepachangelog.com/). Releases are cut automatically from conventional commits by [FerrFlow](https://ferrflow.com).

## [5.0.2] - 2026-06-26

### Bug Fixes

- fix(cli): use keyring-core + native platform stores (drop keyring meta-crate) (#165)

## [5.0.1] - 2026-06-16

### Bug Fixes

- fix: correct FerrVault SAT prefix to fvsat_ and drop dead DefaultAudience const (#156)

## [5.0.0] - 2026-06-16

### Breaking Changes

- refactor!: remove legacy ferrflow.io API and ferrflow naming (#154)

## [4.0.1] - 2026-06-16

### Refactoring

- refactor: rename internal ferrflow client package to ferrvault (#152)

## [4.0.0] - 2026-06-16

### Breaking Changes

- refactor!: rename ferrflow-operator packaging to ferrvault-operator (#149)

## [3.0.14] - 2026-06-15

### Bug Fixes

- fix(deps): update rust crate keyring to v4 (#110)

## [3.0.13] - 2026-06-15

### Bug Fixes

- fix(deps): update kubernetes monorepo to v0.36.2 (#146)

## [3.0.12] - 2026-06-15

### Bug Fixes

- fix(deps): update kubernetes monorepo to v0.36.2 (#143)

## [3.0.11] - 2026-06-13

### Bug Fixes

- fix(deps): update rust crate reqwest to 0.13 (#108)

## [3.0.10] - 2026-06-13

### Bug Fixes

- fix(deps): update module sigs.k8s.io/controller-runtime to v0.24.1 (#87)

## [3.0.9] - 2026-06-13

### Bug Fixes

- fix(deps): update kubernetes monorepo to v0.36.1 (#86)

## [3.0.8] - 2026-06-13

### Bug Fixes

- fix(operator): make organization optional for FerrVaultConnection except in ferrflow mode (#139)

## [3.0.7] - 2026-06-12

### Bug Fixes

- fix(operator): inject one shared TokenBroker into all reconcilers so the OIDC cache persists (#141)

## [3.0.6] - 2026-06-12

### Bug Fixes

- fix(operator): point ferrvault-mode connection sample at api.ferrvault.com (#138)

## [3.0.5] - 2026-06-12

### Bug Fixes

- fix(operator): default FerrVaultConnection mode to ferrvault instead of reusing FerrFlow default (#137)

## [3.0.4] - 2026-06-12

### Bug Fixes

- fix(operator): trim whitespace from tokenSecretRef value before sending as Bearer (#136)

## [3.0.3] - 2026-06-08

### Bug Fixes

- fix: correct ghcr namespace, security contact, API repo link, and connection URL (#129)

## [3.0.2] - 2026-06-03

### Bug Fixes

- fix(deps): update rust crate webpki-roots to v1 (#111)

## [3.0.1] - 2026-06-03

## [3.0.0] - 2026-05-27

### Breaking Changes

- refactor!: rename API group ferrvault.io → ferrvault.com (#113)

## [2.0.0] - 2026-05-27

### Breaking Changes

- feat(api)!: add ferrvault.io/v1alpha1 CRDs alongside ferrflow.io (#94)

## [1.1.1] - 2026-05-26

### Bug Fixes

- fix(cli): linux-arm64 cross build via Cross.toml + action filters cli-v* tags (#102)

## [1.1.0] - 2026-05-26

### Features

- feat(cli): introduce ferrvault consumer CLI + GitHub Action (#89)
- feat: wire operator against FerrVault SaaS via mode=ferrvault (#92)

### Bug Fixes

- fix(deps): update kubernetes monorepo to v0.36.0 (#65)
- fix(deps): update module sigs.k8s.io/controller-runtime to v0.23.3 (#62)
- fix(deps): update module github.com/prometheus/client_golang to v1.23.2 (#61)
- fix(deps): update kubernetes monorepo to v0.35.4 (#60)

## [1.0.1] - 2026-04-21

### Bug Fixes

- fix(ci): rebrand GHCR + GitHub URLs from ferrflow-org to ferrlabs (#48)

## [1.0.0] - 2026-04-20

### Breaking Changes

- feat(crd)!: per-key value transforms (prefix, suffix, rename, base64Decode, jsonExpand) (#47)

## [0.10.0] - 2026-04-20

### Features

- feat(operator): finalizers for orderly FerrFlowSecret + FerrFlowConnection deletion (#44)

## [0.9.0] - 2026-04-20

### Features

- feat(operator): watch referenced token Secret + Connection on FerrFlowSecret reconciler (#36) (#43)

## [0.8.0] - 2026-04-19

### Features

- feat(operator): OIDC workload-identity auth via FerrFlow exchange endpoint (#46)

## [0.7.0] - 2026-04-18

### Features

- feat(metrics): custom Prometheus metrics for reconcile loop (#34)

## [0.6.0] - 2026-04-18

### Features

- feat(client): bounded retry with exponential backoff and jitter (#33)

## [0.5.0] - 2026-04-18

### Features

- feat(operator): FerrFlowConnection reconciler with health probe (#31)

## [0.4.0] - 2026-04-18

### Features

- feat(operator): implement rolloutRestart on FerrFlowSecret (#30)

## [0.3.0] - 2026-04-18

### Features

- feat(operator): consume cluster identity and inject X-FerrFlow-Namespace (#29)

## [0.2.4] - 2026-04-18

### Bug Fixes

- fix(ci): skip Publish on rolling major tag and quote chart version (#26)

## [0.2.3] - 2026-04-18

### Bug Fixes

- fix(deps): commit go.sum and revert to go mod download (#24)

## [0.2.2] - 2026-04-17

### Bug Fixes

- fix(docker): run go mod tidy in builder since go.sum is not committed (#13)

## [0.2.1] - 2026-04-17

### Bug Fixes

- fix(ci): enable publish on tag push and add CodeQL security scan (#12)

## [0.2.0] - 2026-04-17

### Features

- feat: add Helm chart + Docker/chart publish workflow (#10)

## [0.1.0] - 2026-04-17

### Features

- feat: MVP reconciler with FerrFlowConnection and FerrFlowSecret CRDs (#4)

## [Unreleased]

Pre-release scaffolding. See [issue #1](https://github.com/FerrFlow-Org/FerrVault-Operator/issues/1) for the roadmap.
