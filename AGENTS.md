# Agent Instructions

## Scope

- These rules apply to all agent work in this repository.
- Requests to review, analyze, or explain are read-only unless the user also
  asks for changes.
- Read every focused guide whose route matches the task before editing files.
- More specific instructions override broader ones.
- Keep `CLAUDE.md` as a symlink to this file. Record new durable rules here or
  in the matching focused guide.

## Task Routes

| Task or path                                                                            | Read before editing                  |
| --------------------------------------------------------------------------------------- | ------------------------------------ |
| Features, bug fixes, tests, or test helpers                                             | `docs/agents/testing.md`             |
| SQLite, PostgreSQL, CockroachDB, DuckDB, ClickHouse, archive resync, or storage queries | `docs/agents/storage.md`             |
| Watchers, polling, sync scheduling, background work, or memory investigations           | `docs/agents/background-work.md`     |
| Build commands, toolchains, CI build tags, or dependencies                              | `docs/agents/build.md`               |
| S3 ingest, `S3Provider`, or `Source.S3Discovery`                                        | `docs/agents/s3-providers.md`        |
| Any frontend file                                                                       | `frontend/AGENTS.md`                 |
| Frontend controls, styling, or reusable components                                      | `frontend/AGENTS.md` and `DESIGN.md` |

The `README.md` and `Makefile` are the sources for project facts, setup, and
commands. Do not copy their catalogues into this file.

Before reviewing CI runner security, read the
[Namespace runner policy](docs/agents/build.md#namespace-runner-policy).

## Roborev

- Never run `roborev review` unless the user asks for it.
- Never invoke a roborev skill, including `roborev-fix` or
  `roborev-design-review-branch`, unless the user asks for that skill.
- Other roborev commands may be used when they fit the task.

## Git and Delivery

1. Check the branch state before editing. Do not create or switch branches
   without the user's permission. A Codex-managed detached worktree may
   receive commits; leave branch creation to the user or app.
1. Commit every turn that changes tracked files. Do not make empty commits. If a
   turn is read-only or changes only ignored files, say that no commit was
   made.
1. Keep commits focused and use clear conventional commit messages.
1. Do not amend, squash, rebase, push, or pull unless the user asks.
1. Do not add generated-with lines, attribution blocks, validation footers, or
   command transcripts to commit messages.
1. Deliver changes through pull requests from feature branches. Never merge a
   pull request; merging is the user's decision.

## Safety

- Do not revert user work or unrelated local changes unless the user asks.
- Avoid destructive git commands unless the user asks.
- Never install over a live binary, run migrations against production, or write
  to live data directories without permission. Use isolated scratch data for
  branch builds and profiling.
- For login or OAuth, give the user the exact command instead of driving the
  interactive flow.
- SQLite is the persistent archive. Never delete, drop, truncate, or recreate it
  to handle data-version changes. Read `docs/agents/storage.md` before any
  archive, database, parser-resync, or storage change.

## Content and Publishing

- Keep private project names, hostnames, personal identities, infrastructure
  details, and absolute user paths out of code, tests, fixtures,
  documentation, commit messages, and pull request text. Run the private-data
  scrub before publishing.
- Write changelog entries in plain language for people who do not live in the
  codebase. Lead each entry with the outcome a user or operator will notice.
  Put implementation details after that outcome, and include them only when
  they help the reader act or understand a limit.
- Keep pull request titles and descriptions in sync with the current diff.
- Do not post pull request or issue comments unless the user asks.

## Documentation

- Write for the person trying to use or maintain AgentsView. Lead with the
  outcome, name who does what, use short sentences, and explain unfamiliar
  terms.
- Organize around reader questions. Put purpose and current capabilities first;
  separate limitations and future work. Use only the sections the topic needs.
- Give each bullet one main idea. Use numbered steps for sequences, paragraphs
  for rationale, and tables or diagrams when they clarify a comparison or
  flow.
- State rules directly. Preserve exact commands, field names, authorization
  checks, limits, and failure behavior when simplifying the wording.
- Give each fact an owning guide or reference and link to it elsewhere. Update
  that section instead of appending a narrative of the latest change. Indexes
  should route readers, not repeat implementation status.
- Describe current architecture separately from approved but unbuilt work,
  proposals, and historical decisions. Preserve rationale, approvals, and
  active exceptions with their removal conditions. Label superseded designs
  and keep them outside normal navigation.
- Keep the website, its Markdown companions, README, and documentation on
  message. Distinguish the latest release from newer `main` functionality.
  Follow [docs/README.md](docs/README.md) for the publishing layout and
  checks.
- Verify release notes against the release tags and source. Credit contributors
  from merged pull requests or commit history; do not infer contributions from
  names or issue participation alone.
- Regenerate the full screenshot set for every release and store it on the
  `docs-generated-assets` orphan branch. Add captures for new visible
  features, inspect the images, and preview the assembled website locally
  before opening the release documentation pull request. Follow
  `docs/screenshots/README.md`.

## Definition of Done

- Follow every focused guide matched by the task routes above.
- Run relevant checks before committing when practical. If a check cannot run,
  state that in the handoff.
- After changing Go code, run `go fmt ./...` and `go vet ./...` before
  committing.
- Preserve observable behavior and update documentation when behavior changes.

## Provider Format Provenance

When adding a provider, changing its format or usage/cost accounting, or
investigating a provider release, new artifact generation, parser bug, or usage
discrepancy, consult `docs/internal/session-format-sources.md` and reverify or
update its evidence entry in the same change. Grok remains temporarily excluded
only until its separately owned format-alignment work lands.

## Project Map

agentsview syncs local AI agent sessions into SQLite, serves a Svelte 5 web UI,
and can mirror data to PostgreSQL, DuckDB, or ClickHouse.

- `cmd/agentsview/`: CLI and server entry points
- `internal/db/`: SQLite archive and search
- `internal/postgres/`: PostgreSQL sync and read store
- `internal/duckdb/`: disposable DuckDB mirror and Quack reads
- `internal/clickhouse/`: ClickHouse remote mirror and read store
- `internal/parser/`: agent session parsers
- `internal/server/`: HTTP API and SSE
- `internal/sync/`: discovery, file watching, and sync
- `internal/vector/`: semantic and hybrid search
- `frontend/`: Svelte 5 application

## Conventions

- Prefer the standard library over new dependencies.
- Use `internal/stringutil.SafeTruncate` for byte-limited display text. Add
  truncation markers at the call site and reserve their bytes when the limit
  includes them. Use `internal/stringutil.TruncateRunes` for rune-count
  limits; its limit excludes the supplied suffix. Keep rune-count limits
  distinct from byte limits. Both helpers assume valid UTF-8 and do not
  sanitize malformed input.
- Do not use emojis in code or output.
- Format Markdown with `mdformat --wrap 80` when `mdformat` and
  `mdformat-tables` are available.

## Pull Requests

- Do not poll or watch GitHub Actions checks unless the developer explicitly
  requests it.
- Do not use `gh api` to watch CI jobs unless the user explicitly requests it.
- Write summary-only pull request descriptions. Do not add test plans,
  checklists, command transcripts, or sections named Tests, Testing,
  Verification, or Test plan.
- Explain what the code does now, why it changed, tradeoffs, limits, and where
  reviewers should look.

## SDD目录与标识

- PRD索引：`docs/prd-index.md`；仓内PRD模式使用唯一的`docs/prd.md`。
- 功能目录：`docs/specs/<Spec ID>/`。
- 阶段模板来源：当前数字员工包中各阶段 Skill 的 `references/` 目录；项目内不另建固定模板目录。
- 自查报告目录：`docs/specs/<Spec ID>/items/<item_id>/reports/`；报告必须包含 item_id、work_item_id、spec_id。
- Spec ID格式：`SPEC-<域>-<三位序号>`，例如`SPEC-AV-001`。
- Spec ID必须与目录名、`spec.md`元数据、Commit Trailer及MR描述一致。
- FR格式：`FR-xxx`；AC格式：`AC-xxx-y`；Task格式：`T-xxx`或`T-<DOMAIN>-xxx`。
- 工作项ID前缀：`srdcloud`；提交首行`%srdcloud-<数字>`、`spec.md`元数据工作项ID与门禁状态提交必须使用同一前缀，按项目工作项编号格式设置。
- 已分配的Spec ID、FR/AC ID和Task ID不得复用、静默改号或静默删除。

## SDD执行约束

- 阶段顺序：Requirement → Spec → Design → Tasks → Implement → Validate。
- Gate 0～3自查通过后分别创建阶段MR；Gate 0不要求AC清单。MR经人工评审通过且由用户人工触发后，才能进入下一阶段。
- 主Agent不得自动批准、合并MR，不得跨越人工Gate自动续跑。
- Phase 4一次只实施一个Task，不得并行实施多个Task。
- 当前Task单元测试通过后，才能将状态改为`Done`并立即创建一个本地Commit。
- 首次实现一个Task必须对应一个代码Commit；返工修复沿用原Task ID追加独立Commit，保留已评审历史。Task Commit不得Push、不得单独创建MR。
- 全部Task完成后必须执行全量单元测试，并逐条核对全部适用AC对应的测试真实存在、已执行且通过。
- 全部Task及全部AC验证通过后，才允许统一Push并创建一个Code MR。
- 单测失败、AC测试缺失/失败/错误/跳过/未执行时，禁止Push或创建Code MR。

### 门禁通过状态一致性

- 任一 SDD 阶段节点写为 `passed` 时，同一状态补丁必须显式写入 `issues: []`，并在写入后重新读取 `docs/sdd-state.yaml` 确认。
- 历史问题、已解决说明和旧评审意见保留在对应 Gate 报告、MR/评审证据或 links 中，不得留在 passed 节点的 issues 中。
- 发现任一 passed 节点 issues 非空时，不得提交状态、进入下一阶段或宣称完成；先按真实证据修复状态。

### 并行开发隔离

- work_item_id 相同不代表同一个执行实例；独立开发必须创建新的 item_id 和 source_branch。无法判断是接续还是独立实现时，使用 Ask Question 让用户选择。
- 同一仓库存在其他未完成 item 时，必须让用户选择独立 worktree/clone 并行或串行执行；并行时禁止在当前共享工作目录切换分支。
- 每次修改以及 Commit、Push、MR 前，核对仓库、worktree、item_id、work_item_id、user_id、source_branch 一致；不一致立即阻断，不自动切换到其他 item 的分支。
- 每个 Gate 检查及报告生成前按 item.git.remote fetch 并核对 item.git.target_branch，在报告中记录目标提交 OID；目标更新时先安全同步、评估影响，再检查和生成报告。工作区无法安全同步时保全现场并阻断门禁，不自动 stash/reset。Push/MR 前再 fetch 复核；目标再次更新则同步、重跑受影响门禁、更新报告和提交。提交前当前节点已是 `in_progress`，内容冲突期间保持该状态，不仅因冲突登记返工 `temp_node_id`。进入 `waiting/review` 后不主动同步；目标分支后来更新且需更新 MR 时，才先改回 `in_progress`、清空 `waiting` 后同步，保持主流程位置。另有独立修改请求涉及历史产物时仍走返工入口。
- 项目只保留一份 `docs/sdd-state.yaml`；各 worktree 是分支副本。目标分支需 merge 时，先保全并提交当前 item 状态，使用 `git merge --no-commit --no-ff`，进入未完成 merge 后调用包脚本 `merge-state-git`，核对合并结果包含双方全部 item，再暂存 state 并完成合并。不同 item 可语义合并，同一 item 双向修改必须阻断；禁止整文件选择 ours/theirs 或自动 pull 覆盖。

### UTF-8与文本完整性

- Agent 生成、读取或修改的 Markdown、YAML、JSON、状态 message、提交消息和临时文本统一按 UTF-8 处理。优先使用结构化文件编辑工具；中文不得经过编码不明确的 Shell 管道、重定向、命令替换或命令行字符串拼接。
- 含中文的状态 JSON 补丁优先写为 UTF-8 文件并通过 `sdd-state.mjs --patch-file <path>` 传入；仅在调用环境明确保证标准输入为 UTF-8 时使用 `--patch-file -`。Commit Message 写为 UTF-8 文件并使用 `git commit -F <message-file>`，禁止用 `echo` 或未声明编码的重定向传入中文。
- PRD、索引、Spec、Gate 报告、状态补丁和 Commit Message 每次写入后必须按 UTF-8 读回，核对关键中文与标识。发现 `�`、原文没有的连续问号、明显转码残片或写入前后不一致时立即阻断，不登记 ready、不通过 Gate、不 Commit/Push/MR，不猜测修复乱码。
- 需求来源本身异常时记录来源定位并回查权威源；来源正常但落盘异常时按写入失败处理。已有文件编码无法确认时只读识别并报告，禁止猜测编码后整文件覆盖。

### Ask Question交互要求

- 当流程需要用户补充信息、选择方案、确认是否继续、决定是否由AI修改，或人工触发下一阶段时，必须调用平台的Ask Question（`ask_question`或等价交互式提问）工具；不得只在普通回复中要求用户"回复继续"后结束。
- 适用于`NEEDS_CLARIFICATION`、`NEEDS_USER_ACTION`、`waiting/user_input`、`waiting/user_confirmation`、人工Gate通过后的下一阶段触发，以及`CHANGES_REQUESTED`后的修改授权。问题须说明当前阶段、已核实事实、待决定动作及影响，能列互斥选项时提供简短选项。
- Ask Question答案只代表用户意图；收到答案后仍须重新读取状态并核验MR、产物和门禁前置条件。无答案、超时或关闭问题均不视为同意。
- 外部评审或平台处理中且当前无需用户动作时不重复提问。环境没有Ask Question工具时，保留`waiting/user_input`或`waiting/user_confirmation`并明确报告降级，用一条清晰普通问题等待用户，不伪造工具调用。

### 修改请求与返工入口（所有编辑入口适用）

- 任何阶段收到修改请求，先判断产物归属和影响范围；涉及历史阶段产物时，先登记对应返工节点，再修改。修改请求本身不代表退出SDD，无需用户额外说"返工"。普通代码/文档编辑入口同样遵守，不能验收时直接修文件而不更新状态。
- 修改前读取本项目`docs/sdd-state.yaml`、真实差异及实际SOP，按明确item_id或可信会话关联定位任务；有多个候选、状态读取失败或脚本不可用时，停止写入并说明原因，不把它当作无SDD任务。
- 按职责选择节点：需求/PRD→G0，规格/业务规则/AC→G1，设计/ADR/接口契约→G2，任务/依赖/测试映射→G3，代码/测试/实现配置→G4，验收报告/验证证据→G5。自定义SOP使用实际node_id；多阶段修改从最早需要修改的阶段开始，不按扩展名简单归类。
- 属于当前节点的按当前阶段处理；修改历史阶段产物前，必须由主Agent调用项目绑定SDD包的`scripts/sdd-state.mjs`先read/update登记`temp_node_id`并核实成功，`current_node_id`保持不变。已有临时处理先核对范围，不能静默清除。Validate只负责验收产物，发现上游修订须交回主Agent调用相应Skill。
- 进入返工不改变已有节点状态；首次实际修改落盘后立即将修改节点置`in_progress`、受影响已执行下游置`blocked`，清除失效完成时间/等待，保留历史证据并登记产物。状态写入失败停止后续修改，保留现场并报告，不直接编辑状态YAML或最后才补登记。
- 重新执行受影响门禁；代码变更必须创建/更新Code MR，并核实本次版本人工批准。自查PASS、旧MR批准、Task Done或修改量小均不能代替当前版本审查。
- 无实际变更或本次全部受影响门禁通过后才结束临时处理；不得自动回滚文件或恢复失效passed。最终完成仍须全部节点passed并由finalize收尾。只读调查不触发失效，用户明确独立任务以其范围为准，但不能隐瞒共享产物对现有SDD任务的影响。
- 每次换阶段、Commit/Push/MR前及结束回复前核对实际修改与登记状态，遗漏先补齐再继续；不能只在聊天中声称进入返工。此处是Agent执行要求，不代表Git Hook已实现写文件前拦截。

## Git Hook与提交格式

- Hook目录：Git公共目录下 `codefree-sdd/hooks/`；由init安装脚本配置仓库local `core.hooksPath`，保留并组合原Hooks。
- 组合`pre-commit`先执行原Hook，再调用项目绑定包内`git-hooks/spec-format-check`。
- 组合`commit-msg`先执行原Hook，再调用项目绑定包内`git-hooks/commit-msg-check`；会话Agent切换不改绑。
- 禁止使用`--no-verify`绕过Hook，禁止强制Push。
- Commit首行：`%<工作项ID> <type>(<scope>): <description>`。
- `docs`分轨必须包含且只包含一个`Spec-Id`，禁止`Task-Id`。
- `feat/fix/perf`代码分轨必须包含且只包含一个`Spec-Id`和一个`Task-Id`。
- `chore/style/build/ci/test`无行为变更分轨必须包含`No-Behavior-Change:`，禁止`Spec-Id`和`Task-Id`。
- `revert/refactor`按是否包含`Task-Id`分别进入代码分轨或无行为变更分轨。
- `hotfix`必须包含`Spec-Id`、`Task-Id`和`Hotfix-Backfill:`；只有hotfix允许临时使用`SPEC-PENDING`。
- 多仓批次增加唯一`Change-Set:`。

文档Commit示例：

```text
%srdcloud-001 docs(spec): 新增CodeFree provider显示名修复规格

Spec-Id: SPEC-AV-001
```

代码Task Commit示例：

```text
%srdcloud-001 fix(parser): 修复CodeFree provider AgentLabel提取问题

Spec-Id: SPEC-AV-001
Task-Id: T-001
```

## SDD变更历史

| 日期 | 版本 | 变更内容 | 修订人 |
|---|---|---|---|
| 2026-11-10 | 1.0.0 | 初始SDD约束，接入sdd-dev-engineer@1.0.36 | srd18901883260 |
