# Changelog

## [1.2.4](https://github.com/namecheap/terraform-provider-jenkins/compare/v1.2.3...v1.2.4) (2026-08-05)


### Bug Fixes

* **folder:** normalize nil permissions slice to empty Set ([#192](https://github.com/namecheap/terraform-provider-jenkins/issues/192)) ([50a4ae5](https://github.com/namecheap/terraform-provider-jenkins/commit/50a4ae5d484ac546b202f0a45582858cc516fef9))

## [1.2.3](https://github.com/namecheap/terraform-provider-jenkins/compare/v1.2.2...v1.2.3) (2026-08-04)


### Bug Fixes

* **folder:** preserve empty-permissions security block on read ([#190](https://github.com/namecheap/terraform-provider-jenkins/issues/190)) ([8757b5d](https://github.com/namecheap/terraform-provider-jenkins/commit/8757b5d2d5e637deab86ec8183fcbdfe61849096))

## [1.2.2](https://github.com/namecheap/terraform-provider-jenkins/compare/v1.2.1...v1.2.2) (2026-08-04)


### Bug Fixes

* **folder:** use a Set for security.permissions to avoid apply-time consistency errors ([#188](https://github.com/namecheap/terraform-provider-jenkins/issues/188)) ([bf64936](https://github.com/namecheap/terraform-provider-jenkins/commit/bf649365a75873c628bbd7b6824b2707fc9e2dec))

## [1.2.1](https://github.com/namecheap/terraform-provider-jenkins/compare/v1.2.0...v1.2.1) (2026-07-27)


### Bug Fixes

* **deps:** update dependencies to latest patch versions ([#183](https://github.com/namecheap/terraform-provider-jenkins/issues/183)) ([d4213f3](https://github.com/namecheap/terraform-provider-jenkins/commit/d4213f306df770ea4dcfc81ff8086420a6c98be3))
