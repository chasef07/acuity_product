# Changelog

## [0.4.4](https://github.com/chasef07/acuity_product/compare/v0.4.3...v0.4.4) (2026-08-26)


### Bug Fixes

* **calling:** keep End available through outbound calls ([#198](https://github.com/chasef07/acuity_product/issues/198)) ([ab5c198](https://github.com/chasef07/acuity_product/commit/ab5c19811d5838a2dec3de37431d9ace3b9e5d79))
* **interaction:** consume native LiveKit call closeout ([#191](https://github.com/chasef07/acuity_product/issues/191)) ([42337e0](https://github.com/chasef07/acuity_product/commit/42337e0b2937e7fc8ce390fd5f6259c3f41c1dde))
* **observability:** ignore zero-count histogram samples ([#202](https://github.com/chasef07/acuity_product/issues/202)) ([ea73213](https://github.com/chasef07/acuity_product/commit/ea7321346d3c37b60beb6d419f24d236caf4abde))
* **workspace:** simplify activity timeline ([#206](https://github.com/chasef07/acuity_product/issues/206)) ([5bc11de](https://github.com/chasef07/acuity_product/commit/5bc11def7c3250db892842072dfe2c205e7f32be))


### Performance Improvements

* **worker:** coordinate idle provider command polling ([#203](https://github.com/chasef07/acuity_product/issues/203)) ([bedcae5](https://github.com/chasef07/acuity_product/commit/bedcae554f40575fbe4eb6fae819bf577136d9a9))

## [0.4.3](https://github.com/chasef07/acuity_product/compare/v0.4.2...v0.4.3) (2026-08-26)


### Bug Fixes

* **release:** restore available Node image ([#199](https://github.com/chasef07/acuity_product/issues/199)) ([5015994](https://github.com/chasef07/acuity_product/commit/501599473e4b1fe06ad1126a992a7a5351fd5c46))

## [0.4.2](https://github.com/chasef07/acuity_product/compare/v0.4.1...v0.4.2) (2026-08-26)


### Bug Fixes

* **calling:** require hangup evidence for bridged calls ([#194](https://github.com/chasef07/acuity_product/issues/194)) ([cf5aebe](https://github.com/chasef07/acuity_product/commit/cf5aebe9dd60d5f98135ffe5b058e6504bfaa571))

## [0.4.1](https://github.com/chasef07/acuity_product/compare/v0.4.0...v0.4.1) (2026-08-26)


### Bug Fixes

* **web-calling:** gate Answer on matching media invite ([#192](https://github.com/chasef07/acuity_product/issues/192)) ([d7cf3de](https://github.com/chasef07/acuity_product/commit/d7cf3de1f2d48f2747dae63411511740ce57a098))

## [0.4.0](https://github.com/chasef07/acuity_product/compare/v0.3.0...v0.4.0) (2026-08-22)


### Features

* add specialty demo Locations ([#186](https://github.com/chasef07/acuity_product/issues/186)) ([fab5da9](https://github.com/chasef07/acuity_product/commit/fab5da979eec57c688a8ee2165742c0222ed0c86))

## [0.3.0](https://github.com/chasef07/acuity_product/compare/v0.2.0...v0.3.0) (2026-08-22)


### Features

* **workspace:** refine visual hierarchy ([#183](https://github.com/chasef07/acuity_product/issues/183)) ([550751b](https://github.com/chasef07/acuity_product/commit/550751be8af9fa789e6013c0fdda69bad48e30d2))


### Bug Fixes

* **e2e:** align Slice 5 with compact Task rows ([#184](https://github.com/chasef07/acuity_product/issues/184)) ([945c72d](https://github.com/chasef07/acuity_product/commit/945c72de15e5d1c736e266957e765b8efe165474))

## [0.2.0](https://github.com/chasef07/acuity_product/compare/v0.1.4...v0.2.0) (2026-08-20)


### Features

* **messaging:** acknowledge AI-created tasks automatically ([#176](https://github.com/chasef07/acuity_product/issues/176)) ([94f95da](https://github.com/chasef07/acuity_product/commit/94f95dad726d67e9fc72a0d66f6d64591669efa8))


### Bug Fixes

* **database:** authorize worker recovery row locks ([#182](https://github.com/chasef07/acuity_product/issues/182)) ([1278bdb](https://github.com/chasef07/acuity_product/commit/1278bdbd85b3f5dd41d39e57491c0318500e5e5a))

## [0.1.4](https://github.com/chasef07/acuity_product/compare/v0.1.3...v0.1.4) (2026-08-20)


### Bug Fixes

* **calling:** preserve completed recovery tasks on replay ([#178](https://github.com/chasef07/acuity_product/issues/178)) ([5f86015](https://github.com/chasef07/acuity_product/commit/5f860158d254aedb3c73bcff33029ee616d2506f))

## [0.1.3](https://github.com/chasef07/acuity_product/compare/v0.1.2...v0.1.3) (2026-08-20)


### Bug Fixes

* **calling:** accept late voicemail recording receipts ([#174](https://github.com/chasef07/acuity_product/issues/174)) ([26fc029](https://github.com/chasef07/acuity_product/commit/26fc029cbd93d7b71ce14f610e17e9a2c73be3bf))

## [0.1.2](https://github.com/chasef07/acuity_product/compare/v0.1.1...v0.1.2) (2026-08-19)


### Bug Fixes

* accept cleanup recording receipts with durable evidence ([#171](https://github.com/chasef07/acuity_product/issues/171)) ([006df51](https://github.com/chasef07/acuity_product/commit/006df51d18a32270103b3ecd278cb7c4943652be))
* **calling:** dial staff call legs concurrently ([#173](https://github.com/chasef07/acuity_product/issues/173)) ([ade2013](https://github.com/chasef07/acuity_product/commit/ade201332b57099858c1c8ae8b64404a7d5bdf2a))

## [0.1.1](https://github.com/chasef07/acuity_product/compare/v0.1.0...v0.1.1) (2026-08-18)


### Bug Fixes

* **calling:** prevent release provisioning deadlock ([#165](https://github.com/chasef07/acuity_product/issues/165)) ([d43271b](https://github.com/chasef07/acuity_product/commit/d43271bf815f7a62bdd2fad2f60e219a5a33836d))
* **calling:** reconcile stop ring-window outcomes ([#168](https://github.com/chasef07/acuity_product/issues/168)) ([cbad561](https://github.com/chasef07/acuity_product/commit/cbad5616d940f4abaa7d2c558cf4e9213d64fca9))
* **realtime:** avoid operator lock contention ([#166](https://github.com/chasef07/acuity_product/issues/166)) ([6db1409](https://github.com/chasef07/acuity_product/commit/6db1409eb2368390de283fbf526de26c3e53349b))

## Changelog

All notable changes to Acuity Product will be documented in this file.
