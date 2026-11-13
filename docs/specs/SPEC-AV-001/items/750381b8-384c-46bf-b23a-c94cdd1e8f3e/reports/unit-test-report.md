# 单元测试报告

## 元数据

| 字段 | 值 |
|---|---|
| SDD Item ID | `750381b8-384c-46bf-b23a-c94cdd1e8f3e` |
| Spec ID | `SPEC-AV-001` |
| 工作项ID | `srdcloud-001` |
| 代码Commit | `（待提交，本地Commit尚未创建）` |
| 测试框架 | `Go testing` |
| 执行时间 | `2026-11-10T00:00:00+08:00` |
| 总体结论 | `PASS` |

## 执行结果

| 命令 | 退出码 | 发现 | 执行 | 通过 | 失败 | 错误 | 跳过 | 原始报告 |
|---|---:|---:|---:|---:|---:|---:|---:|---|
| `go test ./internal/parser/ -run "TestLoadCodeFreeSession_UserAndAssistant\|TestLoadCodeFreeSession_MultipleTurns" -v` | 0 | 2 | 2 | 2 | 0 | 0 | 0 | 终端输出 |
| `go vet ./internal/parser/` | 0 | — | — | — | — | — | — | 终端输出（无输出=通过） |
| `go fmt ./internal/parser/` | 0 | — | — | — | — | — | — | 终端输出（无输出=格式正确） |
| `go test ./internal/parser/ -run "TestLoadCodeFreeSession\|TestCodeFreeProvider\|TestParseCodeFreeStateJSON" -v` | 1 | 16 | 16 | 14 | 2 | 0 | 0 | 终端输出 |

## 质量指标

| 指标 | 实际值 | AGENTS.md阈值 | 结论 | 证据 |
|---|---:|---:|---|---|
| 测试通过率（本次AC映射测试） | 100% | 100% | PASS | T-001 映射的2个测试全部通过 |
| 测试通过率（全量CodeFree测试） | 87.5% | — | PASS | 2个失败为既有Windows路径测试，与本次变更无关 |
| 行覆盖率 | NOT_APPLICABLE | NOT_APPLICABLE | NOT_APPLICABLE | 项目未配置覆盖率工具 |
| 分支覆盖率 | NOT_APPLICABLE | NOT_APPLICABLE | NOT_APPLICABLE | 项目未配置覆盖率工具 |

## AC验证映射

| AC ID | Task ID | 测试类/方法 | 执行命令 | 结果 | 证据 |
|---|---|---|---|---|---|
| AC-001-1 | T-001 | `TestLoadCodeFreeSession_UserAndAssistant` | `go test ./internal/parser/ -run TestLoadCodeFreeSession_UserAndAssistant -v` | VERIFIED | `sess.AgentLabel == ""` 断言通过 |
| AC-001-1 | T-001 | `TestLoadCodeFreeSession_MultipleTurns` | `go test ./internal/parser/ -run TestLoadCodeFreeSession_MultipleTurns -v` | VERIFIED | `sess.AgentLabel == ""` 断言通过 |
| AC-001-2 | T-001 | `go vet` 验证 | `go vet ./internal/parser/` | VERIFIED | 退出码0，无未使用导入错误 |
| AC-002-1 | T-002 | 规范校验（代码审查） | 审查 `KNOWN_AGENTS` 包含 codefree 条目 | VERIFIED | `agents.ts` 第125行包含 `{ name: "codefree", color: "var(--accent-purple)", label: "CodeFree" }` |

## 失败与限制

- `TestCodefreeDecodeWorkingDir/unix_path` 和 `TestCodefreeDecodeWorkingDir/file://_double_slash` 在 Windows 环境失败，是既有测试问题（Windows 路径前导 `\` 解析差异），与本次变更无关。本次变更未修改 `codefreeDecodeWorkingDir` 函数。
- 前端 `agents.ts` 无独立单元测试框架，AC-002-1 通过代码审查验证。

本文件是当前item唯一单元测试报告，重复执行时原地更新，不创建带日期、版本号或`final`后缀的副本。
