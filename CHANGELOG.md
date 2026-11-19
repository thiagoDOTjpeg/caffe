# Changelog

## [1.1.0](https://github.com/thiagoDOTjpeg/caffe/compare/v1.0.1...v1.1.0) (2026-11-19)


### Features

* add headless process runtime, reject diamond plans, warn on branch conflicts and fail agents that never start ([bc74def](https://github.com/thiagoDOTjpeg/caffe/commit/bc74defbe0ce128f3f96b6518724e099b108db5a))

## [1.0.1](https://github.com/thiagoDOTjpeg/caffe/compare/v1.0.0...v1.0.1) (2026-11-19)


### Documentation

* bring every document up to date and translate the Windows kit guide ([cefaf68](https://github.com/thiagoDOTjpeg/caffe/commit/cefaf68813c222e2fc8dab279755481e14f2d23e))
* bring every document up to date and translate the Windows kit guide ([281033b](https://github.com/thiagoDOTjpeg/caffe/commit/281033b026faa24f1e216cf9c9d2c20b1ec264c9))

## 1.0.0 (2026-11-19)


### Features

* add integration decisions, merge preview and confirmed merge in Studio and caffe integrate ([2f33e15](https://github.com/thiagoDOTjpeg/caffe/commit/2f33e1552e627fd6f36c7bbbb3c6f74d9dab5fc4))
* **agents:** add agent execution manager and prompt rendering ([c66d9aa](https://github.com/thiagoDOTjpeg/caffe/commit/c66d9aa7a6e98e36cb58282ced887929e769555e))
* **cli:** add caffe command line ([fc2901e](https://github.com/thiagoDOTjpeg/caffe/commit/fc2901ef29dc9b4a1cc8946f02047434a25944ce))
* **config:** load and strictly validate the project configuration ([cfddc5e](https://github.com/thiagoDOTjpeg/caffe/commit/cfddc5e441fccc903f083c1cb4966189e90f9896))
* **contracts:** add domain types, event names, plan validation and report parsing ([f659d48](https://github.com/thiagoDOTjpeg/caffe/commit/f659d489a69fc8b5b52ddc8e479173ed692f1406))
* **core:** add scheduler, orchestrator, role policies, recovery and run report ([bcc9858](https://github.com/thiagoDOTjpeg/caffe/commit/bcc9858dfe6d46b00be63b5528c0f7fbbde1d8f0))
* **core:** hold worktree leases while agents write and wait for human leases ([650eec2](https://github.com/thiagoDOTjpeg/caffe/commit/650eec2c3b7a9b4564099c2b419eca08714c6595))
* **core:** keep leased worktrees on clean and say who holds them ([13cb5f0](https://github.com/thiagoDOTjpeg/caffe/commit/13cb5f030a6c73021b83e7cb20e3fbd544e5ce84))
* **dap:** add a DAP client with Delve over --client-addr and shared framing ([799153c](https://github.com/thiagoDOTjpeg/caffe/commit/799153c6e27589abd544dc21578894e7bdd68315))
* **git:** add worktree manager with safe cleanup ([2738890](https://github.com/thiagoDOTjpeg/caffe/commit/2738890c674ac6ee80d79fb1078642fa0b0adaa1))
* **herdr:** add Herdr CLI runtime adapter ([9fb7225](https://github.com/thiagoDOTjpeg/caffe/commit/9fb7225341c8c483bc0a4c0ae395d65bdde52ad4))
* **lsp:** add a dependency-free LSP client and server manager ([4ca9932](https://github.com/thiagoDOTjpeg/caffe/commit/4ca99326cea7f7f5b848a49489b04264f2090be8))
* **persistence:** add SQLite store with migrations, append-only event log and consistent snapshots ([6e3d59f](https://github.com/thiagoDOTjpeg/caffe/commit/6e3d59f9023876ce9bf29ad18a56a3cb4aa9ed1c))
* **persistence:** add worktree_leases table and lease store API ([0076c46](https://github.com/thiagoDOTjpeg/caffe/commit/0076c467c647163dd31e529ffd4b42c363db8624))
* **planner:** add agent-backed planner and plan parsing ([320d541](https://github.com/thiagoDOTjpeg/caffe/commit/320d541eb4e11ef9d0696b2dd9d6111dff3597b1))
* **prompts:** add role prompt templates ([71d3d1b](https://github.com/thiagoDOTjpeg/caffe/commit/71d3d1bfd75e5b6f0d141cce266e4f4fc194e90f))
* **runtime:** add runtime interface, attach capability, fake runtime and secret redaction ([cb8c8cc](https://github.com/thiagoDOTjpeg/caffe/commit/cb8c8cc79d29bb6607e0dd45e40cb39d2088204c))
* **service:** add application API, detached run launcher and change watcher ([eaff17a](https://github.com/thiagoDOTjpeg/caffe/commit/eaff17a5d29c4ce10e0e9dd8ebdd792493e97c29))
* **session:** add command sessions with process trees, ring buffer and log ([8adc66a](https://github.com/thiagoDOTjpeg/caffe/commit/8adc66a9272142cd26fca10cecc6f7aab6b3a18e))
* **studio:** add $EDITOR hand-off from the viewer ([59c20e8](https://github.com/thiagoDOTjpeg/caffe/commit/59c20e8a854e2ec08addba37d6c8183972aed50b))
* **studio:** add Agent Room cards from stored state ([89f55b8](https://github.com/thiagoDOTjpeg/caffe/commit/89f55b8cc49cf5402ee3700827886e3cca8e7349))
* **studio:** add attach hand-off to an agent's terminal ([7c844ff](https://github.com/thiagoDOTjpeg/caffe/commit/7c844fffdc2798f6ec28a0bf00305e5942cf6e5d))
* **studio:** add breakpoints and a debugger screen for Go tests through Delve ([c0712f9](https://github.com/thiagoDOTjpeg/caffe/commit/c0712f9d7f19d7c630e440da7076fa6011eccf94))
* **studio:** add buffer tabs and a split view ([9339abe](https://github.com/thiagoDOTjpeg/caffe/commit/9339abe2f936bf04789e4122b60892b70d4f3f69))
* **studio:** add built-in editor with worktree leases, safe save and manual commit ([38489a2](https://github.com/thiagoDOTjpeg/caffe/commit/38489a2784db785a0d21452b8f60c70bfa8e4e7e))
* **studio:** add Caffè Studio bootstrap, screens shell and key handling ([caae7e6](https://github.com/thiagoDOTjpeg/caffe/commit/caae7e604cfa2428ef5da8b4b3f06d0d4edd846f))
* **studio:** add confirm dialog and toasts ([21c1530](https://github.com/thiagoDOTjpeg/caffe/commit/21c15300c74c7770a7726c87f47f327fafb63dfc))
* **studio:** add diagnostics, symbols, definition and references through gopls ([b66fdc9](https://github.com/thiagoDOTjpeg/caffe/commit/b66fdc9b1dd5ea616819d0813bdb652e1b47cb77))
* **studio:** add empty, loading, error and disconnected states ([c9ce2bd](https://github.com/thiagoDOTjpeg/caffe/commit/c9ce2bd4be027bae84b3f18ba89ac6cab8109121))
* **studio:** add events panel with task and type filters and scroll-back ([61f5ede](https://github.com/thiagoDOTjpeg/caffe/commit/61f5edeb12ffebc2ec158b6b3db3123be38eccf4))
* **studio:** add Execution Inspector with report, prompt, output, artifacts and events ([9dd3333](https://github.com/thiagoDOTjpeg/caffe/commit/9dd333377d1b87defe15f3b2a48704e210bca86b))
* **studio:** add fuzzy file finder ([f868003](https://github.com/thiagoDOTjpeg/caffe/commit/f868003e26580fd1e46159e2ee9bdf9cea6a71fa))
* **studio:** add git status block to the explorer ([8c1447c](https://github.com/thiagoDOTjpeg/caffe/commit/8c1447cf840196c1722739183a412a4d6442b149))
* **studio:** add highlighted file viewer ([496041b](https://github.com/thiagoDOTjpeg/caffe/commit/496041bdb874c4041e5513b71dcb8c0a1ff2e1c8))
* **studio:** add layout, themes and action registry primitives ([176be7c](https://github.com/thiagoDOTjpeg/caffe/commit/176be7c28d5c99f911e538f8c49aed0f7fcdef0d))
* **studio:** add live agent output and runtime status ([eaa949e](https://github.com/thiagoDOTjpeg/caffe/commit/eaa949e57fea5eac47043bda8b5b3e1169d7fd41))
* **studio:** add live state with watcher, debounced refresh and stale marking ([cb38029](https://github.com/thiagoDOTjpeg/caffe/commit/cb3802964e1ad5c4912bb194f851a85eb9581d7e))
* **studio:** add Overview screen with tasks, agents, health and recent events ([78555a6](https://github.com/thiagoDOTjpeg/caffe/commit/78555a62f342cc9433f3f81fc08b0571f1d0a0c3))
* **studio:** add run controls: new, cancel, resume and clean ([b3ef05d](https://github.com/thiagoDOTjpeg/caffe/commit/b3ef05dd89e3cb9ad1497a2919237afb23739605))
* **studio:** add run report view ([0eddbb5](https://github.com/thiagoDOTjpeg/caffe/commit/0eddbb57389021709afb03c03c8a902e7ebebe76))
* **studio:** add Runs screen with filter and id-kept cursor ([5cd7cd9](https://github.com/thiagoDOTjpeg/caffe/commit/5cd7cd91ed588346b3749d349283c466de57ecea))
* **studio:** add Task Board with status columns and task details ([34d5395](https://github.com/thiagoDOTjpeg/caffe/commit/34d5395adfd10bf9e4face5c1ad2917b1a65f78d))
* **studio:** add task dependency graph and list views ([afbeb26](https://github.com/thiagoDOTjpeg/caffe/commit/afbeb26730903ab013c1876341f538a997dc5d69))
* **studio:** add task diff view, unified and side by side ([46cdd92](https://github.com/thiagoDOTjpeg/caffe/commit/46cdd92660f02e64bddb67549c6798b8add9009a))
* **studio:** add Terminal Manager with command sessions, validation runs, file links and shell hand-off ([ab831ca](https://github.com/thiagoDOTjpeg/caffe/commit/ab831caf7a20051d425925529a920a3dc6f82bf4))
* **studio:** add workspace content search ([b5a5202](https://github.com/thiagoDOTjpeg/caffe/commit/b5a5202ecf553f273c693f9ff8de047281d6d6fc))
* **studio:** add workspace file explorer ([f4632b8](https://github.com/thiagoDOTjpeg/caffe/commit/f4632b84b7ef57e6c192520c5565868b95e57d0c))
* **studio:** add workspace query and selector ([ed4308a](https://github.com/thiagoDOTjpeg/caffe/commit/ed4308ad99a2dfdec0a68f5f18013b494e88aa37))
* **studio:** reload the open file when it changes on disk ([9e44680](https://github.com/thiagoDOTjpeg/caffe/commit/9e446802683bbae99c06f6316ebda53c178d0af5))


### Bug Fixes

* **planner:** accept status and summary in the plan report ([14b3408](https://github.com/thiagoDOTjpeg/caffe/commit/14b34089f82a16a270a6b515c2afae51b766904b))
* **session:** set the log path under the session lock ([7e0dfeb](https://github.com/thiagoDOTjpeg/caffe/commit/7e0dfebd3ab5fb274612a2f51e363e891f4bf528))


### Refactoring

* remove code comments ([358c6cb](https://github.com/thiagoDOTjpeg/caffe/commit/358c6cb80361d3cc41f31cf6982013e903fb5424))


### Documentation

* add README, architecture, runtime integration, development and brand guides ([5f4bfb1](https://github.com/thiagoDOTjpeg/caffe/commit/5f4bfb1af544ce044c60b74d1ba4a0815c6d508d))
* align reserved studio settings ([825e4d9](https://github.com/thiagoDOTjpeg/caffe/commit/825e4d9ab9986e5b584049dc0a8bd8c1a1cce0af))
* **studio:** add Caffè Studio audit, specification, architecture, decisions, roadmap and backlog ([62f185e](https://github.com/thiagoDOTjpeg/caffe/commit/62f185ef19045160948a034b2f020cfe0a095217))
* **studio:** add the user guide and wire studio.editor ([2ae838a](https://github.com/thiagoDOTjpeg/caffe/commit/2ae838a1cd4db0f3981273a46ed7189bfd296eee))
* **studio:** document code intelligence keys ([c4688e7](https://github.com/thiagoDOTjpeg/caffe/commit/c4688e75c9eeb6995f25d7e1417656b4d0158a73))
* **studio:** mark W-05 done ([44df64c](https://github.com/thiagoDOTjpeg/caffe/commit/44df64c4a6afc797b6023875736810a8ac268d41))
* **studio:** record the Windows test kit results ([31d341b](https://github.com/thiagoDOTjpeg/caffe/commit/31d341bf4fcf1b87f9e596fb89a61949462eda2f))
