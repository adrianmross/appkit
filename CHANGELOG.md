# Changelog

## [0.7.0](https://github.com/adrianmross/appkit/compare/v0.6.1...v0.7.0) (2026-11-25)


### Features

* **appkit:** Honor ?fields, ?dry_run and Idempotency-Key on Huma operations registered with humax.Get and humax.Mutate, and keep Huma's $schema field on object bodies ([#38](https://github.com/adrianmross/appkit/issues/38)) ([60f915d](https://github.com/adrianmross/appkit/commit/60f915d9bc7dab8fb9d3475853ec0bfae78c5f78))

## [0.6.1](https://github.com/adrianmross/appkit/compare/v0.6.0...v0.6.1) (2026-11-25)


### Bug Fixes

* **appkit:** Refuse an avatar whose base64 does not decode, and export RequireFresh so an app can ask for a recent sign-in before its own sensitive actions ([#36](https://github.com/adrianmross/appkit/issues/36)) ([f0cf4a3](https://github.com/adrianmross/appkit/commit/f0cf4a34d4316ee95e8a02cdfe180a8c384cb982))

## [0.6.0](https://github.com/adrianmross/appkit/compare/v0.5.0...v0.6.0) (2026-11-20)


### Features

* **appkit:** Give a new verified email its own account when Accounts.MultiTenant is set, record the sign-in a new one replaces in the audit log, and keep approve tokens to a signed-in human ([#35](https://github.com/adrianmross/appkit/issues/35)) ([86e7d94](https://github.com/adrianmross/appkit/commit/86e7d94b5dd3a223f13d0f3d43886a5845b5bbb8))


### Bug Fixes

* **appkit:** Name refresh_token among the grant types the token endpoint accepts when other apps can sign in ([#33](https://github.com/adrianmross/appkit/issues/33)) ([c459f5e](https://github.com/adrianmross/appkit/commit/c459f5eb23c68afa55cf6f4ac8db80e4c08c38e7))

## [0.5.0](https://github.com/adrianmross/appkit/compare/v0.4.0...v0.5.0) (2026-11-19)


### Features

* **appkit:** Add humax, a Huma v2 setup that answers with the same error envelope and strict request checks as httpx ([#29](https://github.com/adrianmross/appkit/issues/29)) ([5b2cd81](https://github.com/adrianmross/appkit/commit/5b2cd81af3e1045357298bb347ae5b0654a6375e))
* **appkit:** let other apps sign in through OAuth: client metadata URLs, dynamic registration, refresh tokens and the discovery documents ([#32](https://github.com/adrianmross/appkit/issues/32)) ([ae269b0](https://github.com/adrianmross/appkit/commit/ae269b08324f1a55e7d64acbfabbc1e2c55ab7a6))
* **appkit:** rate-limit the public sign-in routes per address ([#26](https://github.com/adrianmross/appkit/issues/26)) ([ab1e00f](https://github.com/adrianmross/appkit/commit/ab1e00f0ecc94ff1863408ed48a4f9f5089b679b))
* **appkit:** Sign in with a Better Auth instance's JWT, and let a visitor try the app in a throwaway demo sandbox ([#30](https://github.com/adrianmross/appkit/issues/30)) ([2f35baf](https://github.com/adrianmross/appkit/commit/2f35bafacc3e5c0997c2ce1799dfb57e7625f986))


### Bug Fixes

* **appkit:** Send a refused identity provider sign-in back to the sign-in page with a message, not a raw error ([#31](https://github.com/adrianmross/appkit/issues/31)) ([fa72668](https://github.com/adrianmross/appkit/commit/fa726681a2095392d694c6c793d7f3d09d76d207))

## [0.4.0](https://github.com/adrianmross/appkit/compare/v0.3.0...v0.4.0) (2026-11-15)


### Features

* **account:** sign in a CLI through OAuth, and the dashboard with a passkey or an OIDC provider ([#21](https://github.com/adrianmross/appkit/issues/21)) ([79db553](https://github.com/adrianmross/appkit/commit/79db553e57c4bea78dc8e9532184a41f1207c7ae))
* **ui:** offer the identity providers the server lists on the sign-in page ([#23](https://github.com/adrianmross/appkit/issues/23)) ([d23e383](https://github.com/adrianmross/appkit/commit/d23e3830349a363c87287c33da3ca2a536958f31))

## [0.3.0](https://github.com/adrianmross/appkit/compare/v0.2.4...v0.3.0) (2026-11-15)


### Features

* **httpx:** add Batch, PageSlice, GetOne, DeleteOne and BoolParam ([#20](https://github.com/adrianmross/appkit/issues/20)) ([035ae72](https://github.com/adrianmross/appkit/commit/035ae72aa3e5bd750e113f54c1fce4a5885e5773))
* **ui:** let an app declare itself export-only and supply its own sign-in commands, and name a settings row holding several controls ([#18](https://github.com/adrianmross/appkit/issues/18)) ([407bbc5](https://github.com/adrianmross/appkit/commit/407bbc53dfd07d3200fec6b28d3128b9e89257b4))

## [0.2.4](https://github.com/adrianmross/appkit/compare/v0.2.3...v0.2.4) (2026-11-14)


### Bug Fixes

* **ui:** name a settings row by its label only when the label is not already its control's ([#16](https://github.com/adrianmross/appkit/issues/16)) ([9d4c894](https://github.com/adrianmross/appkit/commit/9d4c8944df8ea416dd234bdf0d81bbe98244b5dd))

## [0.2.3](https://github.com/adrianmross/appkit/compare/v0.2.2...v0.2.3) (2026-11-14)


### Bug Fixes

* **ui:** accept quoted search values, tie settings labels to their controls, and add a cursor pager ([#14](https://github.com/adrianmross/appkit/issues/14)) ([61ed2d4](https://github.com/adrianmross/appkit/commit/61ed2d440ca9e1d64e7632901c624553eea1e51e))

## [0.2.2](https://github.com/adrianmross/appkit/compare/v0.2.1...v0.2.2) (2026-11-13)


### Bug Fixes

* **ui:** lay the sign-in failure art beside the message, name the app's records in the account hint, and export the tour types ([#11](https://github.com/adrianmross/appkit/issues/11)) ([18a0b02](https://github.com/adrianmross/appkit/commit/18a0b0257d69c78e164a86cb7548a96148aa86eb))
* **ui:** take what is particular to an app's tokens from the brand instead of naming an extension and an approve scope ([#13](https://github.com/adrianmross/appkit/issues/13)) ([7eacc89](https://github.com/adrianmross/appkit/commit/7eacc897085d9f440ffac8d3fdc159391fd57292))

## [0.2.1](https://github.com/adrianmross/appkit/compare/v0.2.0...v0.2.1) (2026-11-13)


### Bug Fixes

* take the lock inside an open transaction, project top-level arrays, and filter events by kind ([#9](https://github.com/adrianmross/appkit/issues/9)) ([a1cc13a](https://github.com/adrianmross/appkit/commit/a1cc13a586dc248ac2ffcefa6c0ede71a1f18b33))

## [0.2.0](https://github.com/adrianmross/appkit/compare/v0.1.0...v0.2.0) (2026-11-13)


### Features

* **account:** add users, tokens, sign-in sessions, the audit log and their routes ([#8](https://github.com/adrianmross/appkit/issues/8)) ([484120d](https://github.com/adrianmross/appkit/commit/484120d69d52e0410e2b661f7ef9274a942ca8d7))
* **ui:** attach the package to each release and export the shadcn components by file ([#6](https://github.com/adrianmross/appkit/issues/6)) ([912d889](https://github.com/adrianmross/appkit/commit/912d889932cb1b3989ef2a4082a89b9cdaece929))

## 0.1.0 (2026-11-13)


### Features

* add the events feed, the command line framework, CI and release automation ([#3](https://github.com/adrianmross/appkit/issues/3)) ([9434d6b](https://github.com/adrianmross/appkit/commit/9434d6b5b78bc40874e9674ceeef08bdcacce97c))
* add the platform packages and the platform migration set ([#1](https://github.com/adrianmross/appkit/issues/1)) ([c0b3489](https://github.com/adrianmross/appkit/commit/c0b34893f862b50768637aa531214e8be7d97233))
* **ui:** add the appkit-ui package ([#2](https://github.com/adrianmross/appkit/issues/2)) ([a0cf7b2](https://github.com/adrianmross/appkit/commit/a0cf7b2ea5ec979bdda409daba69d29ee2008329))


### Miscellaneous Chores

* start releases at 0.1.0 ([#5](https://github.com/adrianmross/appkit/issues/5)) ([d212d7c](https://github.com/adrianmross/appkit/commit/d212d7c01a62da826afff7e1c201cb31bb037d6c))
