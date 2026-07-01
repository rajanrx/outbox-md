# Changelog

All notable changes to this project are documented here. The format is based on
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and this project adheres
to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [0.12.0](https://github.com/rajanrx/outbox-md/compare/v0.11.1...v0.12.0) (2026-07-01)


### Features

* project root + doc subpath + per-project agent (auto-reply runs in each project's context) ([37493f5](https://github.com/rajanrx/outbox-md/commit/37493f5143b8311dc3570478334a3bfe620491ff))
* project root + doc subpath + per-project agent (spawn auto-reply in the project's context) ([875ae2d](https://github.com/rajanrx/outbox-md/commit/875ae2d38e192d75f6550938e44b1a217c8ae28e))


### Bug Fixes

* **registry:** resolve symlinks in docs containment check (PR [#59](https://github.com/rajanrx/outbox-md/issues/59) P1) ([298da46](https://github.com/rajanrx/outbox-md/commit/298da46f70165cf183ef2de0eb46677fcdf9de15))

## [0.11.1](https://github.com/rajanrx/outbox-md/compare/v0.11.0...v0.11.1) (2026-07-01)


### Bug Fixes

* **ui:** diff modal width — beat .modal-card's 420px cap on specificity ([1a5b26d](https://github.com/rajanrx/outbox-md/commit/1a5b26d434cbfe9b0da97256171c00349b095e99))
* **ui:** diff modal width (beat .modal-card 420px cap on specificity) ([e41aea1](https://github.com/rajanrx/outbox-md/commit/e41aea1aae390eaf1bbcb396c0ef2d073f903c2c))

## [0.11.0](https://github.com/rajanrx/outbox-md/compare/v0.10.1...v0.11.0) (2026-07-01)


### Features

* **ui:** GitHub-style diff viewer — word highlights, full-screen, side-by-side/inline ([3df3452](https://github.com/rajanrx/outbox-md/commit/3df3452c79332bc2abb58c810ef9ca77e7751a6e))
* **ui:** GitHub-style diff viewer — word-level highlights, full-screen modal, side-by-side/inline toggle ([a77f4bf](https://github.com/rajanrx/outbox-md/commit/a77f4bf10f13a18b544df840ff32fee7a1fbd826))

## [0.10.1](https://github.com/rajanrx/outbox-md/compare/v0.10.0...v0.10.1) (2026-07-01)


### Bug Fixes

* render suggestion diff for replied comments (gate on live proposed suggestion, not status==addressed) ([f0096e2](https://github.com/rajanrx/outbox-md/commit/f0096e2d02701c2edb73215aa0a37ed1c6538163))
* render the suggestion diff for replied comments (not just addressed) ([d993956](https://github.com/rajanrx/outbox-md/commit/d993956b82d4aee82847044a8cd6b7a539a9cb0c))
* **ui:** guard suggestion render against a late fetch after terminal (PR [#53](https://github.com/rajanrx/outbox-md/issues/53) P2) ([252cf36](https://github.com/rajanrx/outbox-md/commit/252cf36ff7449b1cffc7ed0dd9b0a8dd536e2e1f))

## [0.10.0](https://github.com/rajanrx/outbox-md/compare/v0.9.0...v0.10.0) (2026-07-01)


### Features

* 'AI processing' state — self-expiring hint (mark_processing MCP tool + HTTP), live badge ([0d084ec](https://github.com/rajanrx/outbox-md/commit/0d084eccac7b3e98be0647515d70925ab14cdc10))
* anchor re-mapping across edits (R1 spike core) ([5b946a5](https://github.com/rajanrx/outbox-md/commit/5b946a54c19413214561de02d8d9d60c0758241b))
* **api:** thread, human reply, and owner-only resolve endpoints ([e539050](https://github.com/rajanrx/outbox-md/commit/e539050c6db8e11334a6bf1c9844ae049f8fc0ac))
* approval gate, governance webhooks, and reply re-open (backend) ([dc9ef27](https://github.com/rajanrx/outbox-md/commit/dc9ef27a1660dcf65d92e25fe6a78e1094967169))
* comment, suggestion, and thread-message persistence ([9bb9f67](https://github.com/rajanrx/outbox-md/commit/9bb9f67023a8bbce7f8b19dee055f3225f9ca6d0))
* **comments:** margin comments, threads, resolve, and suggestion diff panel ([e376c73](https://github.com/rajanrx/outbox-md/commit/e376c73b1b02be8728fe2d8ed532e0ac4bbc632b))
* core domain types and id helper ([4631e36](https://github.com/rajanrx/outbox-md/commit/4631e36fddae0e4236a3e83e302831c06371ddd7))
* **council:** AI Council server slice — candidates, submit_review, pick ([bb2af8e](https://github.com/rajanrx/outbox-md/commit/bb2af8e9190cb6a30f5830366080d94fdf866c35))
* document and version persistence ([8992bb9](https://github.com/rajanrx/outbox-md/commit/8992bb9deb331cc188f042b2d4c31941e0c9e5a7))
* go module and /healthz endpoint ([b7b0b68](https://github.com/rajanrx/outbox-md/commit/b7b0b6849d1a4c28b63374f3d97bf3a0391af5a5))
* http json api for the browser ui + dev simulate-agent endpoints ([01b1de3](https://github.com/rajanrx/outbox-md/commit/01b1de32d50806a0436c72b5cacaad631f7abb8a))
* in-process --auto-reply (opt-in; folds the runner into the server) ([b6ed38a](https://github.com/rajanrx/outbox-md/commit/b6ed38a692d612099354964f0a1e9418da174ca4))
* in-process --auto-reply (opt-in; folds the runner into the server) ([163d772](https://github.com/rajanrx/outbox-md/commit/163d7729a7065580d61fe06ad132a490dd9a5cf6))
* live comment updates over SSE + event-driven architecture docs ([aad67ec](https://github.com/rajanrx/outbox-md/commit/aad67ec192e0a635478bb8f1e882439048693298))
* mcp server — 5 tools over official go sdk (read_doc, list_open_comments, claim, propose, reply) ([4d30b46](https://github.com/rajanrx/outbox-md/commit/4d30b466c832c9ef6dac34235d9165f50132823a))
* multi-folder sources whitelist + folder view from pending suggestions (drop go-git) ([02817a8](https://github.com/rajanrx/outbox-md/commit/02817a8b4ff69ebf56b7bd7f8e64a030fc2e17fb))
* multi-folder sources whitelist + folder view from pending suggestions (drop go-git) ([02a7062](https://github.com/rajanrx/outbox-md/commit/02a7062f5db2d0c97e7303511812d6866521303c))
* multi-project registry + switcher (MVP) ([5a503cb](https://github.com/rajanrx/outbox-md/commit/5a503cb997ba8e62dd41a27578cdd3b8c7dd9213))
* multi-project registry + switcher (MVP) ([27c30dd](https://github.com/rajanrx/outbox-md/commit/27c30ddb69898dfff368bc992a73a32f26a3fd80))
* one-command CLI onboarding (init/up/serve + install.sh + release binaries) ([f93b26b](https://github.com/rajanrx/outbox-md/commit/f93b26b0bb6263676c6d53a50e88ffef55ee6293))
* one-command CLI onboarding (init/up/serve + install.sh + release binaries) ([41e0529](https://github.com/rajanrx/outbox-md/commit/41e05298cc6f7a046c97774ae0c82bfb8a56379c))
* **ops:** turnkey deploy layer — Makefile, service units, deploy docs ([70ea8b8](https://github.com/rajanrx/outbox-md/commit/70ea8b8685b164d14b55890d89f10c6895948e62))
* **ops:** turnkey deploy layer — Makefile, service units, deploy docs ([0a4aff4](https://github.com/rajanrx/outbox-md/commit/0a4aff42bf1cb8ad6c139d9e0530d0ecbfa2e760))
* outbox init auto-wires Gemini/Cursor/Windsurf/Codex/Claude Desktop ([7f0d4cc](https://github.com/rajanrx/outbox-md/commit/7f0d4cca58cfe3b34852654f041e78ccfcf0ff3e))
* outbox init auto-wires Gemini/Cursor/Windsurf/Codex/Claude Desktop ([aa208ea](https://github.com/rajanrx/outbox-md/commit/aa208ead6a1ecb5b2508eddaa5edb531cf98d4b0))
* Phase 0 foundations + v1-core walking skeleton (anchor spike proven) ([935c7ef](https://github.com/rajanrx/outbox-md/commit/935c7ef008b637213caac715723e537cdbdd4710))
* **processing:** expose mark_processing via MCP tool and HTTP endpoint ([3c2c20c](https://github.com/rajanrx/outbox-md/commit/3c2c20cc8e253c58ad0181da18fca85b725ed228))
* **processing:** live 'AI processing' badge in the web UI ([2ae49be](https://github.com/rajanrx/outbox-md/commit/2ae49bee0e86480ed1cf246436f3d167f117d4e4))
* **processing:** raise default TTL to 180s ([1ffebd5](https://github.com/rajanrx/outbox-md/commit/1ffebd545cd6730c6231062e07d75901f32d9198))
* **processing:** self-expiring processing hint on comments (domain/store/service/webhook) ([877ae0f](https://github.com/rajanrx/outbox-md/commit/877ae0fbf0a2214c46d5bf22b6e5461dd65ea40b))
* **reader:** assemble source anchor from a rendered selection ([7207db2](https://github.com/rajanrx/outbox-md/commit/7207db254a1195f6feea7f2f73bcfa3675a8515f))
* **reader:** document sidebar + compose the full review UI ([619896b](https://github.com/rajanrx/outbox-md/commit/619896bf2ca7a58533b23448a65094b26f2570a0))
* **reader:** rendered markdown reader with gfm, highlighting, mermaid ([4ec5d94](https://github.com/rajanrx/outbox-md/commit/4ec5d94c9a6e21c103d6a1df9cbfa9d489340bff))
* **reader:** rendered→source offset mapping (anchor spike core) ([0cc546f](https://github.com/rajanrx/outbox-md/commit/0cc546f455c49849e590f23ea1bc18d09c0b1c7a))
* **reader:** source-position plugin + block-scoped selection offsets ([3e69618](https://github.com/rajanrx/outbox-md/commit/3e69618b78ddb25905e00361dd4a7b1a8758c63f))
* **runner:** ack webhook receipt so the 'AI processing' badge appears instantly ([cd1c657](https://github.com/rajanrx/outbox-md/commit/cd1c657c710fc4fb867e798bcbf0d64e17c78a2e))
* **runner:** ack webhook receipt to show the processing badge instantly ([9237390](https://github.com/rajanrx/outbox-md/commit/9237390b776d2d07c119f84a2e2b4218fa7140dd))
* scaffold vite react-ts frontend ([cdac2e2](https://github.com/rajanrx/outbox-md/commit/cdac2e2aed42d9ff58745d47cfe5a053401c72f6))
* self-update — auto_update (default true) + outbox upgrade + Watchtower ([21d301b](https://github.com/rajanrx/outbox-md/commit/21d301bfed712c93e425a817a425570eecef8c88))
* self-update — auto_update (default true) + outbox upgrade + Watchtower ([a5fcb5a](https://github.com/rajanrx/outbox-md/commit/a5fcb5af25f614c889fdc07b5dd83473ad1eb52b))
* service loop with accept + re-anchoring (hypothesis proven) ([a2e0ece](https://github.com/rajanrx/outbox-md/commit/a2e0ecec38da1df72debae9909958e917c227172))
* skeleton frontend (textarea editor, outbox, suggestion, accept) ([031e208](https://github.com/rajanrx/outbox-md/commit/031e208e9af5bbecfb2a5fd0b33499d52f9a1a9a))
* sqlite store open + schema migration ([ddf55ce](https://github.com/rajanrx/outbox-md/commit/ddf55cec505f0de1609700aadd93958369cc2571))
* **suggestion:** backend reject endpoint (reopens the comment) ([bc6c58e](https://github.com/rajanrx/outbox-md/commit/bc6c58e614cc0747bf664bc44557ca4bcd4d75af))
* **ui:** diff excerpt + modal with git folder-diff view ([99dc46e](https://github.com/rajanrx/outbox-md/commit/99dc46e493bc55e83d2ab89c3c0610caf9120198))
* **ui:** premium 'Manuscript Desk' redesign — IDE chrome, file tree, collapsible panels ([36749e6](https://github.com/rajanrx/outbox-md/commit/36749e655935512ea725ffb41391995847b4c97e))
* **ui:** suggestion diff excerpt + modal with git folder-diff view ([6bbb63b](https://github.com/rajanrx/outbox-md/commit/6bbb63b6c23b24841049f5a56bec7c5a82dbf692))
* **web:** approval confirmation modal + comments-resolved gate ([13f59a4](https://github.com/rajanrx/outbox-md/commit/13f59a4d57b902d120208bee022ccaa4ad2ec938))
* wire server — embed spa, import md folder, mount api + mcp over http ([5f64ad1](https://github.com/rajanrx/outbox-md/commit/5f64ad1158d9fb37264a0759fa2aff2fbba688db))


### Bug Fixes

* accept-level transaction + compensation; preserve file mode/ownership on atomic write ([47f6e1c](https://github.com/rajanrx/outbox-md/commit/47f6e1c1ae9645c6905836a0aaa0a07b5874d4df))
* **api:** enforce sources whitelist at serve time, not just import (PR [#35](https://github.com/rajanrx/outbox-md/issues/35) P1) ([9c76ded](https://github.com/rajanrx/outbox-md/commit/9c76ded1d5a01657c3cae5c1d2374812e5541195))
* **api:** enforce sources whitelist on the dev agent endpoints (PR [#35](https://github.com/rajanrx/outbox-md/issues/35) P2) ([3da0708](https://github.com/rajanrx/outbox-md/commit/3da070832268642fde2b82947be49699cd595942))
* **api:** guard all doc/comment-scoped routes + align glob import with Serves (PR [#35](https://github.com/rajanrx/outbox-md/issues/35)) ([bac886e](https://github.com/rajanrx/outbox-md/commit/bac886efb9db7904ac0aabdbdc6bf41dae68ccb7))
* **api:** per-project runtime sources guard in multi mode (post-[#42](https://github.com/rajanrx/outbox-md/issues/42) P1) ([476eee6](https://github.com/rajanrx/outbox-md/commit/476eee63f84720e55047fab764244f0b6fdaa5d4))
* **api:** per-project runtime sources guard in multi mode (PR [#42](https://github.com/rajanrx/outbox-md/issues/42) P2) ([61b0d56](https://github.com/rajanrx/outbox-md/commit/61b0d56b69c56807f2dec03780e69d3a74d90bb0))
* **ci:** build web UI before cross-compiling release binaries (PR [#37](https://github.com/rajanrx/outbox-md/issues/37) P1) ([1b97367](https://github.com/rajanrx/outbox-md/commit/1b97367b44284f9e4a8d7ee10807df69a4beed05))
* **config:** apply env overrides when no outbox.yaml exists ([5776d33](https://github.com/rajanrx/outbox-md/commit/5776d33d2c2188cddaf9101fd73129e18541815f))
* **config:** apply env overrides when no outbox.yaml exists (webhooks silently disabled) ([7508928](https://github.com/rajanrx/outbox-md/commit/7508928699119b1d6a04b8e6424c7baed2f64bc0))
* **council:** terminal-state guard on pick, validate lens, split GET errors, propagate writes ([dd8d5db](https://github.com/rajanrx/outbox-md/commit/dd8d5dbdff3761a0851e51b60e8ecb85956ac12f))
* **docker:** cross-compile multi-arch (no QEMU hang) + lead README with published image ([0d62bbf](https://github.com/rajanrx/outbox-md/commit/0d62bbf4e3f9ef7bb2dc622852898e247a81a709))
* **docker:** cross-compile multi-arch (unhang the publish) + surface published image in README ([74347bf](https://github.com/rajanrx/outbox-md/commit/74347bf612ddabc20be61c35b150bedfa90565ce))
* empty doc no longer crashes the UI; default compose to docs/specs ([684a8eb](https://github.com/rajanrx/outbox-md/commit/684a8eb1b67dc92a83f6096b1a766e82181c6924))
* **git:** recover inside the diff goroutine (P2) + minor hardening ([14f2fe0](https://github.com/rajanrx/outbox-md/commit/14f2fe0d93e1d6d0ae0680c0b013d892ec1fb28a))
* **git:** recover inside the diff goroutine + minor hardening ([bc42d48](https://github.com/rajanrx/outbox-md/commit/bc42d4833e8c30c18f4df6f75f92389d6f08ae00))
* **git:** reject symlinks (P1 leak) + omit skipped files, don't render as add/delete (P2) ([0a3e54b](https://github.com/rajanrx/outbox-md/commit/0a3e54b32f3b719250c1e8fe04a5da9f429e7387))
* harden file-write path against traversal (safeJoin guard) ([2543ce4](https://github.com/rajanrx/outbox-md/commit/2543ce43e4c705af0118644acdce1f6c373aa6e2))
* make losing-accept requeue conditional to avoid lifecycle corruption ([125da54](https://github.com/rajanrx/outbox-md/commit/125da542197b289b8f351dfdb963f6daeb3711ff))
* **mcpclients:** Codex uses native HTTP url, not the mcp-remote bridge (PR [#46](https://github.com/rajanrx/outbox-md/issues/46) P2) ([daa1094](https://github.com/rajanrx/outbox-md/commit/daa1094a4d519369d511ef0f31869f680571b4a7))
* **mcpclients:** use a real TOML parser for Codex config merge (PR [#46](https://github.com/rajanrx/outbox-md/issues/46) P1) ([95c075e](https://github.com/rajanrx/outbox-md/commit/95c075e7bb5d5b9bffe06f95458109da5d0eb8ca))
* **mcp:** enforce sources whitelist on the MCP surface too (PR [#35](https://github.com/rajanrx/outbox-md/issues/35) P1) ([950ce0f](https://github.com/rajanrx/outbox-md/commit/950ce0f2b1f9c79e354a0e139417f64c9f415ef5))
* **mcp:** gate MCP write handlers on the sources whitelist too ([2754027](https://github.com/rajanrx/outbox-md/commit/2754027b89a33aeb69ab9b1e5c278efa49f6a623))
* **reader:** rune-offset anchors, block-only source-pos, mermaid whole-block, liveness polling ([1acdcfb](https://github.com/rajanrx/outbox-md/commit/1acdcfbb04a2ae93acf434a20857d63cc015c4d6))
* refresh on SSE reconnect, accurate hub comment, README anchor, disconnect test ([42cfab8](https://github.com/rajanrx/outbox-md/commit/42cfab84ba5881368fc9a97aa12f4cd4627b58cd))
* reject stale/duplicate accepts and write file before advancing version ([0665a46](https://github.com/rajanrx/outbox-md/commit/0665a469b587984bdabec7ec4b355cd01a0a206b))
* **runner:** default-deny unsigned, body size cap, py sig guard + README ref ([b463fd7](https://github.com/rajanrx/outbox-md/commit/b463fd79b17524726b70658f2e0833fda8f0cc7a))
* serialize concurrent accepts with compare-and-swap on current version ([83f1f39](https://github.com/rajanrx/outbox-md/commit/83f1f39a310dbb598861219b5070ad24521f06eb))
* sink-aware event short-circuit, drain webhook body, dedupe excerpt ([41f7f3f](https://github.com/rajanrx/outbox-md/commit/41f7f3f5c2527ff9d57a32003c6ac1f9d9412dbb))
* **store:** gate folder view on comment status = addressed (PR [#35](https://github.com/rajanrx/outbox-md/issues/35) P2) ([7056d1a](https://github.com/rajanrx/outbox-md/commit/7056d1a73f69c1a9983da2c6aef89b1175b04834))
* **ui:** agent replies/suggestions update the browser live (SSE), no refresh ([ea9e499](https://github.com/rajanrx/outbox-md/commit/ea9e4999ba2bc2d55459039f941f8944149cbd5d))
* **ui:** open comment thread re-renders live on SSE events (no refresh) ([6aca544](https://github.com/rajanrx/outbox-md/commit/6aca544537a5378fd0214202c95ce70f59b46b30))
* **ui:** push agent replies/suggestions over SSE so the browser updates live ([8f697ee](https://github.com/rajanrx/outbox-md/commit/8f697ee6ef29b0e1f8dc070f88e3ea2bc48b1a8b))
* **ui:** re-fetch the open thread on SSE events (agent reply appears without refresh) ([09b331f](https://github.com/rajanrx/outbox-md/commit/09b331f2652faa6b5f1d6aaef3bf7bf70f489e3e))
* **update:** opt-out before network I/O + test selfReplace/latestRelease + Watchtower name ([8e212a9](https://github.com/rajanrx/outbox-md/commit/8e212a99dbfa0a27c1fb6914d439198ecdf72914))

## [0.9.0](https://github.com/rajanrx/outbox-md/compare/outbox-md-v0.8.0...outbox-md-v0.9.0) (2026-07-01)


### Features

* outbox init auto-wires Gemini/Cursor/Windsurf/Codex/Claude Desktop ([7f0d4cc](https://github.com/rajanrx/outbox-md/commit/7f0d4cca58cfe3b34852654f041e78ccfcf0ff3e))
* outbox init auto-wires Gemini/Cursor/Windsurf/Codex/Claude Desktop ([aa208ea](https://github.com/rajanrx/outbox-md/commit/aa208ead6a1ecb5b2508eddaa5edb531cf98d4b0))


### Bug Fixes

* **mcpclients:** Codex uses native HTTP url, not the mcp-remote bridge (PR [#46](https://github.com/rajanrx/outbox-md/issues/46) P2) ([daa1094](https://github.com/rajanrx/outbox-md/commit/daa1094a4d519369d511ef0f31869f680571b4a7))
* **mcpclients:** use a real TOML parser for Codex config merge (PR [#46](https://github.com/rajanrx/outbox-md/issues/46) P1) ([95c075e](https://github.com/rajanrx/outbox-md/commit/95c075e7bb5d5b9bffe06f95458109da5d0eb8ca))

## [0.8.0](https://github.com/rajanrx/outbox-md/compare/outbox-md-v0.7.0...outbox-md-v0.8.0) (2026-07-01)


### Features

* multi-project registry + switcher (MVP) ([5a503cb](https://github.com/rajanrx/outbox-md/commit/5a503cb997ba8e62dd41a27578cdd3b8c7dd9213))
* multi-project registry + switcher (MVP) ([27c30dd](https://github.com/rajanrx/outbox-md/commit/27c30ddb69898dfff368bc992a73a32f26a3fd80))


### Bug Fixes

* **api:** per-project runtime sources guard in multi mode (post-[#42](https://github.com/rajanrx/outbox-md/issues/42) P1) ([476eee6](https://github.com/rajanrx/outbox-md/commit/476eee63f84720e55047fab764244f0b6fdaa5d4))
* **api:** per-project runtime sources guard in multi mode (PR [#42](https://github.com/rajanrx/outbox-md/issues/42) P2) ([61b0d56](https://github.com/rajanrx/outbox-md/commit/61b0d56b69c56807f2dec03780e69d3a74d90bb0))

## [0.7.0](https://github.com/rajanrx/outbox-md/compare/outbox-md-v0.6.0...outbox-md-v0.7.0) (2026-07-01)


### Features

* one-command CLI onboarding (init/up/serve + install.sh + release binaries) ([f93b26b](https://github.com/rajanrx/outbox-md/commit/f93b26b0bb6263676c6d53a50e88ffef55ee6293))
* one-command CLI onboarding (init/up/serve + install.sh + release binaries) ([41e0529](https://github.com/rajanrx/outbox-md/commit/41e05298cc6f7a046c97774ae0c82bfb8a56379c))
* self-update — auto_update (default true) + outbox upgrade + Watchtower ([21d301b](https://github.com/rajanrx/outbox-md/commit/21d301bfed712c93e425a817a425570eecef8c88))
* self-update — auto_update (default true) + outbox upgrade + Watchtower ([a5fcb5a](https://github.com/rajanrx/outbox-md/commit/a5fcb5af25f614c889fdc07b5dd83473ad1eb52b))


### Bug Fixes

* **ci:** build web UI before cross-compiling release binaries (PR [#37](https://github.com/rajanrx/outbox-md/issues/37) P1) ([1b97367](https://github.com/rajanrx/outbox-md/commit/1b97367b44284f9e4a8d7ee10807df69a4beed05))
* **update:** opt-out before network I/O + test selfReplace/latestRelease + Watchtower name ([8e212a9](https://github.com/rajanrx/outbox-md/commit/8e212a99dbfa0a27c1fb6914d439198ecdf72914))

## [0.6.0](https://github.com/rajanrx/outbox-md/compare/outbox-md-v0.5.0...outbox-md-v0.6.0) (2026-07-01)


### Features

* multi-folder sources whitelist + folder view from pending suggestions (drop go-git) ([02817a8](https://github.com/rajanrx/outbox-md/commit/02817a8b4ff69ebf56b7bd7f8e64a030fc2e17fb))
* multi-folder sources whitelist + folder view from pending suggestions (drop go-git) ([02a7062](https://github.com/rajanrx/outbox-md/commit/02a7062f5db2d0c97e7303511812d6866521303c))


### Bug Fixes

* **api:** enforce sources whitelist at serve time, not just import (PR [#35](https://github.com/rajanrx/outbox-md/issues/35) P1) ([9c76ded](https://github.com/rajanrx/outbox-md/commit/9c76ded1d5a01657c3cae5c1d2374812e5541195))
* **api:** enforce sources whitelist on the dev agent endpoints (PR [#35](https://github.com/rajanrx/outbox-md/issues/35) P2) ([3da0708](https://github.com/rajanrx/outbox-md/commit/3da070832268642fde2b82947be49699cd595942))
* **api:** guard all doc/comment-scoped routes + align glob import with Serves (PR [#35](https://github.com/rajanrx/outbox-md/issues/35)) ([bac886e](https://github.com/rajanrx/outbox-md/commit/bac886efb9db7904ac0aabdbdc6bf41dae68ccb7))
* **mcp:** enforce sources whitelist on the MCP surface too (PR [#35](https://github.com/rajanrx/outbox-md/issues/35) P1) ([950ce0f](https://github.com/rajanrx/outbox-md/commit/950ce0f2b1f9c79e354a0e139417f64c9f415ef5))
* **mcp:** gate MCP write handlers on the sources whitelist too ([2754027](https://github.com/rajanrx/outbox-md/commit/2754027b89a33aeb69ab9b1e5c278efa49f6a623))
* **store:** gate folder view on comment status = addressed (PR [#35](https://github.com/rajanrx/outbox-md/issues/35) P2) ([7056d1a](https://github.com/rajanrx/outbox-md/commit/7056d1a73f69c1a9983da2c6aef89b1175b04834))

## [0.5.0](https://github.com/rajanrx/outbox-md/compare/outbox-md-v0.4.0...outbox-md-v0.5.0) (2026-07-01)


### Features

* **ui:** diff excerpt + modal with git folder-diff view ([99dc46e](https://github.com/rajanrx/outbox-md/commit/99dc46e493bc55e83d2ab89c3c0610caf9120198))
* **ui:** suggestion diff excerpt + modal with git folder-diff view ([6bbb63b](https://github.com/rajanrx/outbox-md/commit/6bbb63b6c23b24841049f5a56bec7c5a82dbf692))


### Bug Fixes

* **git:** recover inside the diff goroutine (P2) + minor hardening ([14f2fe0](https://github.com/rajanrx/outbox-md/commit/14f2fe0d93e1d6d0ae0680c0b013d892ec1fb28a))
* **git:** recover inside the diff goroutine + minor hardening ([bc42d48](https://github.com/rajanrx/outbox-md/commit/bc42d4833e8c30c18f4df6f75f92389d6f08ae00))
* **git:** reject symlinks (P1 leak) + omit skipped files, don't render as add/delete (P2) ([0a3e54b](https://github.com/rajanrx/outbox-md/commit/0a3e54b32f3b719250c1e8fe04a5da9f429e7387))

## [0.4.0](https://github.com/rajanrx/outbox-md/compare/outbox-md-v0.3.0...outbox-md-v0.4.0) (2026-07-01)


### Features

* **ops:** turnkey deploy layer — Makefile, service units, deploy docs ([70ea8b8](https://github.com/rajanrx/outbox-md/commit/70ea8b8685b164d14b55890d89f10c6895948e62))
* **ops:** turnkey deploy layer — Makefile, service units, deploy docs ([0a4aff4](https://github.com/rajanrx/outbox-md/commit/0a4aff42bf1cb8ad6c139d9e0530d0ecbfa2e760))

## [0.3.0](https://github.com/rajanrx/outbox-md/compare/outbox-md-v0.2.0...outbox-md-v0.3.0) (2026-07-01)


### Features

* **runner:** ack webhook receipt so the 'AI processing' badge appears instantly ([cd1c657](https://github.com/rajanrx/outbox-md/commit/cd1c657c710fc4fb867e798bcbf0d64e17c78a2e))
* **runner:** ack webhook receipt to show the processing badge instantly ([9237390](https://github.com/rajanrx/outbox-md/commit/9237390b776d2d07c119f84a2e2b4218fa7140dd))

## [0.2.0](https://github.com/rajanrx/outbox-md/compare/outbox-md-v0.1.1...outbox-md-v0.2.0) (2026-07-01)


### Features

* 'AI processing' state — self-expiring hint (mark_processing MCP tool + HTTP), live badge ([0d084ec](https://github.com/rajanrx/outbox-md/commit/0d084eccac7b3e98be0647515d70925ab14cdc10))
* anchor re-mapping across edits (R1 spike core) ([5b946a5](https://github.com/rajanrx/outbox-md/commit/5b946a54c19413214561de02d8d9d60c0758241b))
* **api:** thread, human reply, and owner-only resolve endpoints ([e539050](https://github.com/rajanrx/outbox-md/commit/e539050c6db8e11334a6bf1c9844ae049f8fc0ac))
* approval gate, governance webhooks, and reply re-open (backend) ([dc9ef27](https://github.com/rajanrx/outbox-md/commit/dc9ef27a1660dcf65d92e25fe6a78e1094967169))
* comment, suggestion, and thread-message persistence ([9bb9f67](https://github.com/rajanrx/outbox-md/commit/9bb9f67023a8bbce7f8b19dee055f3225f9ca6d0))
* **comments:** margin comments, threads, resolve, and suggestion diff panel ([e376c73](https://github.com/rajanrx/outbox-md/commit/e376c73b1b02be8728fe2d8ed532e0ac4bbc632b))
* core domain types and id helper ([4631e36](https://github.com/rajanrx/outbox-md/commit/4631e36fddae0e4236a3e83e302831c06371ddd7))
* **council:** AI Council server slice — candidates, submit_review, pick ([bb2af8e](https://github.com/rajanrx/outbox-md/commit/bb2af8e9190cb6a30f5830366080d94fdf866c35))
* document and version persistence ([8992bb9](https://github.com/rajanrx/outbox-md/commit/8992bb9deb331cc188f042b2d4c31941e0c9e5a7))
* go module and /healthz endpoint ([b7b0b68](https://github.com/rajanrx/outbox-md/commit/b7b0b6849d1a4c28b63374f3d97bf3a0391af5a5))
* http json api for the browser ui + dev simulate-agent endpoints ([01b1de3](https://github.com/rajanrx/outbox-md/commit/01b1de32d50806a0436c72b5cacaad631f7abb8a))
* live comment updates over SSE + event-driven architecture docs ([aad67ec](https://github.com/rajanrx/outbox-md/commit/aad67ec192e0a635478bb8f1e882439048693298))
* mcp server — 5 tools over official go sdk (read_doc, list_open_comments, claim, propose, reply) ([4d30b46](https://github.com/rajanrx/outbox-md/commit/4d30b466c832c9ef6dac34235d9165f50132823a))
* Phase 0 foundations + v1-core walking skeleton (anchor spike proven) ([935c7ef](https://github.com/rajanrx/outbox-md/commit/935c7ef008b637213caac715723e537cdbdd4710))
* **processing:** expose mark_processing via MCP tool and HTTP endpoint ([3c2c20c](https://github.com/rajanrx/outbox-md/commit/3c2c20cc8e253c58ad0181da18fca85b725ed228))
* **processing:** live 'AI processing' badge in the web UI ([2ae49be](https://github.com/rajanrx/outbox-md/commit/2ae49bee0e86480ed1cf246436f3d167f117d4e4))
* **processing:** raise default TTL to 180s ([1ffebd5](https://github.com/rajanrx/outbox-md/commit/1ffebd545cd6730c6231062e07d75901f32d9198))
* **processing:** self-expiring processing hint on comments (domain/store/service/webhook) ([877ae0f](https://github.com/rajanrx/outbox-md/commit/877ae0fbf0a2214c46d5bf22b6e5461dd65ea40b))
* **reader:** assemble source anchor from a rendered selection ([7207db2](https://github.com/rajanrx/outbox-md/commit/7207db254a1195f6feea7f2f73bcfa3675a8515f))
* **reader:** document sidebar + compose the full review UI ([619896b](https://github.com/rajanrx/outbox-md/commit/619896bf2ca7a58533b23448a65094b26f2570a0))
* **reader:** rendered markdown reader with gfm, highlighting, mermaid ([4ec5d94](https://github.com/rajanrx/outbox-md/commit/4ec5d94c9a6e21c103d6a1df9cbfa9d489340bff))
* **reader:** rendered→source offset mapping (anchor spike core) ([0cc546f](https://github.com/rajanrx/outbox-md/commit/0cc546f455c49849e590f23ea1bc18d09c0b1c7a))
* **reader:** source-position plugin + block-scoped selection offsets ([3e69618](https://github.com/rajanrx/outbox-md/commit/3e69618b78ddb25905e00361dd4a7b1a8758c63f))
* scaffold vite react-ts frontend ([cdac2e2](https://github.com/rajanrx/outbox-md/commit/cdac2e2aed42d9ff58745d47cfe5a053401c72f6))
* service loop with accept + re-anchoring (hypothesis proven) ([a2e0ece](https://github.com/rajanrx/outbox-md/commit/a2e0ecec38da1df72debae9909958e917c227172))
* skeleton frontend (textarea editor, outbox, suggestion, accept) ([031e208](https://github.com/rajanrx/outbox-md/commit/031e208e9af5bbecfb2a5fd0b33499d52f9a1a9a))
* sqlite store open + schema migration ([ddf55ce](https://github.com/rajanrx/outbox-md/commit/ddf55cec505f0de1609700aadd93958369cc2571))
* **suggestion:** backend reject endpoint (reopens the comment) ([bc6c58e](https://github.com/rajanrx/outbox-md/commit/bc6c58e614cc0747bf664bc44557ca4bcd4d75af))
* **ui:** premium 'Manuscript Desk' redesign — IDE chrome, file tree, collapsible panels ([36749e6](https://github.com/rajanrx/outbox-md/commit/36749e655935512ea725ffb41391995847b4c97e))
* **web:** approval confirmation modal + comments-resolved gate ([13f59a4](https://github.com/rajanrx/outbox-md/commit/13f59a4d57b902d120208bee022ccaa4ad2ec938))
* wire server — embed spa, import md folder, mount api + mcp over http ([5f64ad1](https://github.com/rajanrx/outbox-md/commit/5f64ad1158d9fb37264a0759fa2aff2fbba688db))


### Bug Fixes

* accept-level transaction + compensation; preserve file mode/ownership on atomic write ([47f6e1c](https://github.com/rajanrx/outbox-md/commit/47f6e1c1ae9645c6905836a0aaa0a07b5874d4df))
* **config:** apply env overrides when no outbox.yaml exists ([5776d33](https://github.com/rajanrx/outbox-md/commit/5776d33d2c2188cddaf9101fd73129e18541815f))
* **config:** apply env overrides when no outbox.yaml exists (webhooks silently disabled) ([7508928](https://github.com/rajanrx/outbox-md/commit/7508928699119b1d6a04b8e6424c7baed2f64bc0))
* **council:** terminal-state guard on pick, validate lens, split GET errors, propagate writes ([dd8d5db](https://github.com/rajanrx/outbox-md/commit/dd8d5dbdff3761a0851e51b60e8ecb85956ac12f))
* **docker:** cross-compile multi-arch (no QEMU hang) + lead README with published image ([0d62bbf](https://github.com/rajanrx/outbox-md/commit/0d62bbf4e3f9ef7bb2dc622852898e247a81a709))
* **docker:** cross-compile multi-arch (unhang the publish) + surface published image in README ([74347bf](https://github.com/rajanrx/outbox-md/commit/74347bf612ddabc20be61c35b150bedfa90565ce))
* empty doc no longer crashes the UI; default compose to docs/specs ([684a8eb](https://github.com/rajanrx/outbox-md/commit/684a8eb1b67dc92a83f6096b1a766e82181c6924))
* harden file-write path against traversal (safeJoin guard) ([2543ce4](https://github.com/rajanrx/outbox-md/commit/2543ce43e4c705af0118644acdce1f6c373aa6e2))
* make losing-accept requeue conditional to avoid lifecycle corruption ([125da54](https://github.com/rajanrx/outbox-md/commit/125da542197b289b8f351dfdb963f6daeb3711ff))
* **reader:** rune-offset anchors, block-only source-pos, mermaid whole-block, liveness polling ([1acdcfb](https://github.com/rajanrx/outbox-md/commit/1acdcfbb04a2ae93acf434a20857d63cc015c4d6))
* refresh on SSE reconnect, accurate hub comment, README anchor, disconnect test ([42cfab8](https://github.com/rajanrx/outbox-md/commit/42cfab84ba5881368fc9a97aa12f4cd4627b58cd))
* reject stale/duplicate accepts and write file before advancing version ([0665a46](https://github.com/rajanrx/outbox-md/commit/0665a469b587984bdabec7ec4b355cd01a0a206b))
* **runner:** default-deny unsigned, body size cap, py sig guard + README ref ([b463fd7](https://github.com/rajanrx/outbox-md/commit/b463fd79b17524726b70658f2e0833fda8f0cc7a))
* serialize concurrent accepts with compare-and-swap on current version ([83f1f39](https://github.com/rajanrx/outbox-md/commit/83f1f39a310dbb598861219b5070ad24521f06eb))
* sink-aware event short-circuit, drain webhook body, dedupe excerpt ([41f7f3f](https://github.com/rajanrx/outbox-md/commit/41f7f3f5c2527ff9d57a32003c6ac1f9d9412dbb))
* **ui:** agent replies/suggestions update the browser live (SSE), no refresh ([ea9e499](https://github.com/rajanrx/outbox-md/commit/ea9e4999ba2bc2d55459039f941f8944149cbd5d))
* **ui:** open comment thread re-renders live on SSE events (no refresh) ([6aca544](https://github.com/rajanrx/outbox-md/commit/6aca544537a5378fd0214202c95ce70f59b46b30))
* **ui:** push agent replies/suggestions over SSE so the browser updates live ([8f697ee](https://github.com/rajanrx/outbox-md/commit/8f697ee6ef29b0e1f8dc070f88e3ea2bc48b1a8b))
* **ui:** re-fetch the open thread on SSE events (agent reply appears without refresh) ([09b331f](https://github.com/rajanrx/outbox-md/commit/09b331f2652faa6b5f1d6aaef3bf7bf70f489e3e))

## [Unreleased]

### Added
- Walking skeleton: annotate → outbox → agent proposes → accept re-anchors and rewrites the file.
- MCP server (5 tools) over Streamable HTTP; HTTP/JSON API and React frontend; single Docker container.
