## Context

codefree-o 是 opencode 的修改版，会话文件格式与 opencode 完全一致，但存在关键路径差异：

| 属性 | opencode | codefree-o |
|---|---|---|
| 根目录 | `~/.local/share/opencode` | `~/.codefree-o` |
| 文件存储 | `<root>/storage/session/` | `<root>/.local/share/storage/session/` |
| SQLite DB | `<root>/opencode.db` | `<root>/.local/share/codefree.db` |
| ID前缀 | `opencode:` | `codefree-o:` |
| 恢复命令 | `opencode --session <id>` | `codefree-o --session <id>` |

agentsview 对 opencode 的支持已非常成熟（1279 行解析器 + 约 15 处引擎 dispatch）。codefree-o 的文件格式一致，可复用绝大部分解析逻辑，仅需新增 agent 类型的注册、发现和 dispatch。

## Goals / Non-Goals

**Goals:**
- 注册 codefree-o 为独立 agent，会话在 UI 中可识别为 "codefree-o" 而非 "opencode"
- 支持两种存储模式：文件存储（`storage/session/`）和 SQLite 数据库
- 复用 opencode 的全部解析函数（格式相同，无需重复）
- 文件 watcher 能正确响应 codefree-o 目录下的变更
- 恢复按钮生成正确的 `codefree-o --session <id>` 命令

**Non-Goals:**
- 不改动现有 opencode 的路径和功能（保留 `storage/session`，不升级到 `session_diff`）
- 不重构 opencode 的解析器为通用共享层（避免 scope creep）
- 不添加 codefree-o 特有的定价或信号分析（与 opencode 一致即可）

## Decisions

### D1: 独立 Agent vs 别名

**选择**：独立 Agent（`AgentCodefreeO`）
**理由**：会话在 UI 中可区分来源，恢复命令使用正确的二进制名，配置独立。虽然引擎需要多处 dispatch 分支，但每个分支只需 ~3 行，维护成本可控。

### D2: 解析器复用策略

**选择**：codefree-o 的发现/解析函数直接调用 opencode 的解析器
**理由**：JSON 文件格式和 SQLite schema 完全相同。`ParseOpenCodeFile`、`ParseOpenCodeDB`、`ParseOpenCodeSession` 等函数不绑定 agent 类型，参数中也不含 "opencode" 字面量，可以直接调用。

```
processCodefreeO(file) {
    if (SQLite虚拟路径) → ParseOpenCodeSession(dbPath, sessionID, machine)
    if (codefree.db)   → ListOpenCodeSessionMeta + ParseOpenCodeSession
    else               → ParseOpenCodeFile(sessionPath, machine)
}
```

### D3: 源文件发现与路径解析

**选择**：创建 `internal/parser/codefree_o.go`，包含专用的发现和解析函数
**理由**：与 opencode 的路径差异（根目录、DB 路径、文件存储路径）需要不同的解析逻辑。新文件中的函数为：

| 函数 | 作用 |
|---|---|
| `ResolveCodefreeOSource(root)` | 检测文件模式 vs SQLite 模式 |
| `DiscoverCodefreeOSessions(root)` | 扫描文件存储目录 |
| `FindCodefreeOSourceFile(root, id)` | 按 ID 定位源文件 |
| `ResolveCodefreeOWatchRoots(root)` | 返回 watcher 监控目录 |

这些函数是 opencode 对应函数的镜像，仅路径常量不同。

### D4: 引擎 dispatch 策略

**选择**：在 engine.go 的每个 dispatch 点添加 `AgentCodefreeO` 分支
**理由**：engine.go 已有成熟的 agent 分支模式（如 Warp、Forge、Piebald 等 DB 型 agent 各有独立的方法）。codefree-o 遵循相同的模式，每个分支处调用新建的 `processCodefreeO` / `syncCodefreeO` 等方法。

涉及的具体 dispatch 点：
1. `classifyOpenCodePath` → 新增 `classifyCodefreeOPath`（硬编码 `opencode.db` → `codefree.db`）
2. `syncOpenCode` → 新增 `syncCodefreeO`
3. `processDiscoveredFile` switch → 新增 case
4. `shouldCacheSkip` → 新增条件
5. `syncSingle` 中的 opencode 检查 → 新增条件
6. `countDBBackedSessions` → 新增循环
7. `countDBBackedProgressTotal` → 新增 case
8. `SyncAll` 中 opencode 的同步块 → 新增 codefree-o 同步块
9. `discoveredFileMtime` → 新增条件

### D5: 文件 watcher 路径映射

**选择**：从 `classifyOpenCodePath` 中提取通用路径模式，为 codefree-o 创建类似函数
**理由**：codefree-o 的文件存储路径是 `<root>/.local/share/storage/session/`，与 opencode 的 `<root>/storage/session/` 不同。watcher 需要精确的 rel path 匹配来定位变更属于哪个 session。

## Risks / Trade-offs

- **[维护成本]** codefree-o 的 engine 方法是 opencode 的镜像。如果 opencode 的处理逻辑未来改变，codefree-o 的对应方法也需要同步更新。→ 接受，codefree-o 作为 fork 本就应与 opencode 保持步调一致
- **[内聚耦合]** codefree-o 的解析层直接调用 `ParseOpenCodeFile` 等函数，而不是通过抽象接口。如果 opencode 的解析器签名或行为改变，可能影响 codefree-o。→ 已评估风险低，opencode 是成熟项目，解析接口稳定
- **[测试覆盖]** 新增的 codefree-o 方法没有独立测试，依赖集成测试。→ 后续可在 `engine_integration_test.go` 中沿用 opencode 的测试模式补充 fixture 测试
