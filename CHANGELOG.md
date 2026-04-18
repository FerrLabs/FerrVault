# Changelog

All notable changes to `ferrflow-operator` will be documented here.

The format is based on [Keep a Changelog](https://keepachangelog.com/). Releases are cut automatically from conventional commits by [FerrFlow](https://ferrflow.com).

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

Pre-release scaffolding. See [issue #1](https://github.com/FerrFlow-Org/FerrFlow-Operator/issues/1) for the roadmap.
