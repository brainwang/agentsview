# PRD追溯索引

## 权威源模式声明

```yaml
source: repo
prd_path: docs/prd.md
prd_version_field: null
updated_at: 2026-11-11
```

## 索引条目

| PRD ID/工作项 | 来源链接或路径 | PRD版本 | FEAT编号 | 需求点摘要 | 归属候选 | 澄清状态 | Spec指针 |
|---|---|---|---|---|---|---|---|
| AV-CODEFREE-LABEL | docs/prd.md#FEAT-001 | 1.1.0 | FEAT-001 | 移除 CodeFree provider AgentLabel 路径段提取 | internal/parser | CLEAR | SPEC-AV-001/FR-001 |
| AV-CODEFREE-LABEL | docs/prd.md#FEAT-002 | 1.1.0 | FEAT-002 | 前端注册 CodeFree 显示名 | frontend | CLEAR | SPEC-AV-001/FR-002 |
| AV-CODEFREE-LABEL | docs/prd.md#FEAT-003 | 1.1.0 | FEAT-003 | CodeFree 会话消息时间戳从 agenthost.log 提取 | internal/parser | CLEAR | SPEC-AV-001/FR-003 |

## 变更记录

| 日期 | 来源版本 | 变更 | 操作人 |
|---|---|---|---|
| 2026-11-10 | 1.0.0 | 初始登记 | srd18901883260 |
| 2026-11-11 | 1.1.0 | 新增 FEAT-003：消息时间戳从 agenthost.log 提取 | srd18901883260 |

> 本项目只保留这一份`docs/prd-index.md`并原地更新。工作项模式不复制PRD正文；仓内模式的唯一正文为`docs/prd.md`。
