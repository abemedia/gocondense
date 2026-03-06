# Changelog

## [0.1.0](https://github.com/abemedia/gocondense/compare/v0.0.1...v0.1.0) (2026-03-06)


### Features

* **cli:** skip generated files and vendor directories during directory walks ([#63](https://github.com/abemedia/gocondense/issues/63)) ([b5ae71a](https://github.com/abemedia/gocondense/commit/b5ae71ae7325a5fab4233bac3a0dabf085729972))
* condense declaration groups ([#3](https://github.com/abemedia/gocondense/issues/3)) ([5b9a9b2](https://github.com/abemedia/gocondense/commit/5b9a9b2be24bfc9a6e80fbd081b37f82bb28450f))
* first-element rule for struct/map literals, trailing-arg call condensing, remove feature flags ([#58](https://github.com/abemedia/gocondense/issues/58)) ([065cbd9](https://github.com/abemedia/gocondense/commit/065cbd9bf0c6348bac95c9fdaea031b0130b0283))
* merge adjacent same-type function parameters ([#39](https://github.com/abemedia/gocondense/issues/39)) ([5fa4428](https://github.com/abemedia/gocondense/commit/5fa4428e9676bd52f6f2b63c92c1c6dd5407e06d))
* remove unnecessary parens ([#18](https://github.com/abemedia/gocondense/issues/18)) ([9e3abe8](https://github.com/abemedia/gocondense/commit/9e3abe8846d733fa4303d51361a325ebd7f7029a)), closes [#15](https://github.com/abemedia/gocondense/issues/15)
* simplify condense ([#13](https://github.com/abemedia/gocondense/issues/13)) ([f50c068](https://github.com/abemedia/gocondense/commit/f50c0684eeafdbf66251c20b2794bac877eb2b34))
* strip leading/trailing blank lines from block statements ([#44](https://github.com/abemedia/gocondense/issues/44)) ([8fb51b1](https://github.com/abemedia/gocondense/commit/8fb51b1545a642f36696cc18bd96c30258931ea5))
* trim leading/trailing blank lines inside blocks ([#56](https://github.com/abemedia/gocondense/issues/56)) ([980ab9e](https://github.com/abemedia/gocondense/commit/980ab9e0d01ba7b557b84dfbf8a63543e77ac0a7))


### Bug Fixes

* **cli:** return exit code 2 on errors ([#54](https://github.com/abemedia/gocondense/issues/54)) ([ccec651](https://github.com/abemedia/gocondense/commit/ccec6514472a7033d87fc4b43f5a735623fd8fc4))
* corrupts files, strips generic types from declaration ([#9](https://github.com/abemedia/gocondense/issues/9)) ([c7789c4](https://github.com/abemedia/gocondense/commit/c7789c430f667e8c1aa61ad094ee912270671ea0))
* line length calculation ignoring indentation and surrounding context ([#51](https://github.com/abemedia/gocondense/issues/51)) ([b9285ec](https://github.com/abemedia/gocondense/commit/b9285ec1c1536b1e4fbc3b0f52b839c5c198dd0d))
