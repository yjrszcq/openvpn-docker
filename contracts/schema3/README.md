# Schema 3 Shell Runtime Contract

[中文](README_CN.md)

This directory freezes the observable 3.2.0 Shell/Python runtime baseline that the Go rewrite starts from. It is an implementation input, not a promise that the intentionally redesigned v4 CLI or storage layout remains byte-for-byte compatible.

`behavior.json` records commands, defaults, state classes, persistent files, render cases, migration fixtures, and the disposition of changed interfaces. `test-inventory.json` classifies every existing test so the runtime cutover can retain external evidence and replace implementation-coupled assertions deliberately.

## Compatibility rule

Each v3 behavior has one disposition:

- `preserve`: retain the user-visible semantic behavior in v4.
- `redesign`: retain the use case, but verify the documented v4 interface and explain output/storage differences.
- `fold`: move the behavior into another v4 workflow.
- `retire`: intentionally remove a v3-only implementation surface.

The v3 command manual and current smoke tests remain the detailed source of truth until their corresponding Go contract exists. Phase 2 render/IPAM tests must compare against the cases recorded here. Phase 9 migration tests must use a real schema 3 image/fixture, rather than reconstructing schema 3 from schema 4 assumptions.

## Intentional v4 breaks already approved

- Environment-driven persistent configuration becomes strict declarative YAML.
- `init`, `start`, and server rendering move under `ovpn server`.
- Online `network plan/apply` is folded into offline `config plan/apply`.
- Existing-client selectors accept an explicit `--name` or `--id`; when neither option is present, a positional selector is treated as a name.
- `--dynamic`/`--ip` become `--ipv4 auto|dynamic|ADDRESS`.
- `client ip` becomes `client address`.
- `runtime version` moves to top-level `version`.
- Schema 3 CSV/JSONL authority becomes SQLite schema 4; PKI and artifacts remain files.
- General invalid input and dependency/lock failures gain stable sysexits-style codes instead of the v3 catch-all status 1.

Any additional deviation discovered during implementation must be documented in the checkpoint roadmap before the dependent phase is committed.
