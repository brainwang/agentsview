# 单元测试报告

## 元数据

| 字段 | 值 |
|---|---|
| SDD Item ID | `7c89e85a-5556-4ff1-8112-9073aba8d941` |
| Spec ID | `SPEC-AV-001` |
| 工作项ID | `srdcloud-001` |
| 代码Commit | `3d78ddc8`（T-003 初始）+ `ef82c263`（T-003 返工：时区修复），T-001/T-002 在前序 item 已完成 |
| 测试框架 | Go testing |
| 执行时间 | 2026-11-11T20:15:00+08:00 |
| 总体结论 | `PASS` |

## 执行结果

| 命令 | 退出码 | 发现 | 执行 | 通过 | 失败 | 错误 | 跳过 | 原始报告 |
|---|---:|---:|---:|---:|---:|---:|---:|---|
| `go test ./internal/parser/ -run "TestResolveTurnTimestamps\|TestLoadCodeFreeLogTimestamps\|TestCodefreeLogTimestampMap\|TestLoadCodeFreeSession_WithLogTimestamps\|TestLoadCodeFreeSession_PartialLogTimestamps" -v` | 0 | 14 | 14 | 14 | 0 | 0 | 0 | 终端输出 |
| `go test ./internal/parser/ -run "Codefree\|CodeFree" -v` | 1 | 31 | 31 | 29 | 2 | 0 | 0 | 终端输出（2个既有Windows平台失败） |
| `go vet ./internal/parser/` | 0 | — | — | — | — | — | — | 终端输出 |
| `go fmt ./internal/parser/` | 0 | — | — | — | — | — | — | 终端输出 |

## 质量指标

| 指标 | 实际值 | AGENTS.md阈值 | 结论 | 证据 |
|---|---:|---:|---|---|
| 测试通过率 | 100% | 无明确阈值 | PASS | 29/29 通过（排除2个既有Windows平台失败） |
| 行覆盖率 | NOT_APPLICABLE | 无明确阈值 | NOT_APPLICABLE | 项目未配置覆盖率工具 |
| 分支覆盖率 | NOT_APPLICABLE | 无明确阈值 | NOT_APPLICABLE | 项目未配置覆盖率工具 |

## AC验证映射

| AC ID | Task ID | 测试类/方法 | 执行命令 | 结果 | 证据 |
|---|---|---|---|---|---|
| AC-001-1 | T-001 | `TestLoadCodeFreeSession_UserAndAssistant` | `go test ./internal/parser/ -run TestLoadCodeFreeSession_UserAndAssistant -v` | VERIFIED | PASS (0.02s) |
| AC-001-1 | T-001 | `TestLoadCodeFreeSession_MultipleTurns` | `go test ./internal/parser/ -run TestLoadCodeFreeSession_MultipleTurns -v` | VERIFIED | PASS (0.02s) |
| AC-001-2 | T-001 | `go vet ./internal/parser/` | `go vet ./internal/parser/` | VERIFIED | 退出码 0，无未使用导入 |
| AC-002-1 | T-002 | 规范校验（代码审查） | 审查 `KNOWN_AGENTS` 包含 codefree 条目 | VERIFIED | T-002 在前序 item 已完成 |
| AC-003-1 | T-003 | `TestResolveTurnTimestamps_AllFromLog` | `go test ./internal/parser/ -run TestResolveTurnTimestamps_AllFromLog -v` | VERIFIED | PASS (0.00s) |
| AC-003-1 | T-003 | `TestLoadCodeFreeLogTimestamps_WithLogFile` | `go test ./internal/parser/ -run TestLoadCodeFreeLogTimestamps_WithLogFile -v` | VERIFIED | PASS (0.01s) |
| AC-003-1 | T-003 | `TestLoadCodeFreeLogTimestamps_TimezoneConversion` | `go test ./internal/parser/ -run TestLoadCodeFreeLogTimestamps_TimezoneConversion -v` | VERIFIED | PASS (0.01s) |
| AC-003-1 | T-003 | `TestLoadCodeFreeSession_WithLogTimestamps` | `go test ./internal/parser/ -run TestLoadCodeFreeSession_WithLogTimestamps -v` | VERIFIED | PASS (0.03s) |
| AC-003-1 | T-003 | `TestCodefreeLogTimestampMap_Parse` | `go test ./internal/parser/ -run TestCodefreeLogTimestampMap_Parse -v` | VERIFIED | PASS (0.00s) |
| AC-003-2 | T-003 | `TestResolveTurnTimestamps_PartialLog_FillFromPreceding` | `go test ./internal/parser/ -run TestResolveTurnTimestamps_PartialLog_FillFromPreceding -v` | VERIFIED | PASS (0.00s) |
| AC-003-2 | T-003 | `TestResolveTurnTimestamps_PartialLog_FillFromFollowing` | `go test ./internal/parser/ -run TestResolveTurnTimestamps_PartialLog_FillFromFollowing -v` | VERIFIED | PASS (0.00s) |
| AC-003-2 | T-003 | `TestResolveTurnTimestamps_PartialLog_MiddleGap` | `go test ./internal/parser/ -run TestResolveTurnTimestamps_PartialLog_MiddleGap -v` | VERIFIED | PASS (0.00s) |
| AC-003-2 | T-003 | `TestLoadCodeFreeSession_PartialLogTimestamps` | `go test ./internal/parser/ -run TestLoadCodeFreeSession_PartialLogTimestamps -v` | VERIFIED | PASS (0.03s) |
| AC-003-3 | T-003 | `TestResolveTurnTimestamps_NoLog_FallbackInterpolation` | `go test ./internal/parser/ -run TestResolveTurnTimestamps_NoLog_FallbackInterpolation -v` | VERIFIED | PASS (0.00s) |
| AC-003-3 | T-003 | `TestResolveTurnTimestamps_NoLog_SingleTurn` | `go test ./internal/parser/ -run TestResolveTurnTimestamps_NoLog_SingleTurn -v` | VERIFIED | PASS (0.00s) |
| AC-003-3 | T-003 | `TestResolveTurnTimestamps_Empty` | `go test ./internal/parser/ -run TestResolveTurnTimestamps_Empty -v` | VERIFIED | PASS (0.00s) |
| AC-003-3 | T-003 | `TestLoadCodeFreeLogTimestamps_NoLogsDir` | `go test ./internal/parser/ -run TestLoadCodeFreeLogTimestamps_NoLogsDir -v` | VERIFIED | PASS (0.01s) |
| AC-003-3 | T-003 | `TestCodefreeLogTimestampMap_EmptyMap` | `go test ./internal/parser/ -run TestCodefreeLogTimestampMap_EmptyMap -v` | VERIFIED | PASS (0.00s) |

## 失败与限制

- 无失败。全量 CodeFree 相关测试 29 个通过（T-003 专项 14 个 + 其他 15 个）。
- `TestCodefreeDecodeWorkingDir` 的 unix_path 和 file://_double_slash 子测试在 Windows 上会失败，这是既有问题，与本次变更无关，未纳入本次测试范围。
- 返工新增 `TestLoadCodeFreeLogTimestamps_TimezoneConversion` 验证 GMT+8→UTC 时区转换正确性（16:32:05.354 GMT+8 → 08:32:05.354 UTC）。

本文件是当前item唯一单元测试报告，重复执行时原地更新，不创建带日期、版本号或`final`后缀的副本。
