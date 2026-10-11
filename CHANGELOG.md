# Changelog

## [1.56.0](https://github.com/anatolykoptev/go-engine/compare/v1.55.0...v1.56.0) (2026-10-11)


### Added

* **llm:** verify fact quotes against cited source text ([#96](https://github.com/anatolykoptev/go-engine/issues/96)) ([1bcfdbe](https://github.com/anatolykoptev/go-engine/commit/1bcfdbea2c0a6119ba4f00776049a2a7832998a7))

## [1.55.0](https://github.com/anatolykoptev/go-engine/compare/v1.54.0...v1.55.0) (2026-10-10)


### Added

* **oxbrowser:** send X-Internal-Secret to ox-browser via go-kit svcauth (ox-browser[#173](https://github.com/anatolykoptev/go-engine/issues/173)) ([#93](https://github.com/anatolykoptev/go-engine/issues/93)) ([588f435](https://github.com/anatolykoptev/go-engine/commit/588f435fdfbe2a8c540bb29bf0d11a408496d6d4))

## [1.54.0](https://github.com/anatolykoptev/go-engine/compare/v1.53.2...v1.54.0) (2026-10-09)


### Added

* **pipeline:** learnings fields on SearchOutput (covered_aspects, iterated_queries) ([#91](https://github.com/anatolykoptev/go-engine/issues/91)) ([fc50e46](https://github.com/anatolykoptev/go-engine/commit/fc50e464d84a64fd7a856742c22e6c6ca05b702f))

## [1.53.2](https://github.com/anatolykoptev/go-engine/compare/v1.53.1...v1.53.2) (2026-10-09)


### Fixed

* classify gated SERPs as captcha on ox-escalation ([#317](https://github.com/anatolykoptev/go-engine/issues/317)) ([#89](https://github.com/anatolykoptev/go-engine/issues/89)) ([3b4aab7](https://github.com/anatolykoptev/go-engine/commit/3b4aab7671096e35ae116327a7fd5cf388dc59d5))

## [1.53.1](https://github.com/anatolykoptev/go-engine/compare/v1.53.0...v1.53.1) (2026-10-09)


### Fixed

* drop manual go_search_ prefix from metric constants ([#87](https://github.com/anatolykoptev/go-engine/issues/87)) ([efda53e](https://github.com/anatolykoptev/go-engine/commit/efda53eb6ac927001f336ad58b042b36cc84ebd6))

## [1.53.0](https://github.com/anatolykoptev/go-engine/compare/v1.52.2...v1.53.0) (2026-07-29)


### Added

* **search:** configurable Marginalia key with a fail-closed daily budget ([#81](https://github.com/anatolykoptev/go-engine/issues/81)) ([9efd9d8](https://github.com/anatolykoptev/go-engine/commit/9efd9d8e14407941be70fc6f887b2f587cbaefd2))

## [1.52.2](https://github.com/anatolykoptev/go-engine/compare/v1.52.1...v1.52.2) (2026-07-29)


### Fixed

* migrate ox-browser fallback off deprecated /fetch-smart to raw /fetch ([#79](https://github.com/anatolykoptev/go-engine/issues/79)) ([6395037](https://github.com/anatolykoptev/go-engine/commit/639503740db612332ff4e41d46f7c96b217002a1))

## [1.52.1](https://github.com/anatolykoptev/go-engine/compare/v1.52.0...v1.52.1) (2026-07-26)


### Changed

* derive browser UA from go-stealth identity instead of hardcoded pool ([#69](https://github.com/anatolykoptev/go-engine/issues/69)) ([b1bce9e](https://github.com/anatolykoptev/go-engine/commit/b1bce9e41a44ee752d61065805a5a5ffd40bec30))

## [1.52.0](https://github.com/anatolykoptev/go-engine/compare/v1.51.5...v1.52.0) (2026-07-26)


### Added

* **gosearch:** add SearchWithOpts + SearchOpts with source param ([#66](https://github.com/anatolykoptev/go-engine/issues/66)) ([05140dd](https://github.com/anatolykoptev/go-engine/commit/05140ddf77c99c60d3ef1fdd83f67725b07cfde8))


### Dependencies

* bump go-stealth to v1.19.1 ([#68](https://github.com/anatolykoptev/go-engine/issues/68)) ([2d3091c](https://github.com/anatolykoptev/go-engine/commit/2d3091c314b706507855362ac1a90b38e8105d86))
