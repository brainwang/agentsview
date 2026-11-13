---
AIGC:
  ContentProducer: '001191110102MAD55U9H0F10002'
  ContentPropagator: '001191110102MAD55U9H0F10002'
  Label: '1'
  ProduceID: 'e97110da-ec0c-41fe-9ee2-41ab2ddf0cdc'
  PropagateID: 'e97110da-ec0c-41fe-9ee2-41ab2ddf0cdc'
  ReservedCode1: '05e80262-e78d-4945-8f05-e9981d07235e'
  ReservedCode2: '05e80262-e78d-4945-8f05-e9981d07235e'
---

# 数据库表结构与 ID 生成规则

> 本文件供脚本编写时参考，LLM 无需在主对话中加载此技术细节。

## session 表

| 关键字段 | 说明 |
|----------|------|
| id | session ID，格式 `ses_<ts_hex>01<rand16>` |
| project_id | NOT NULL，固定 `"global"` |
| parent_id | 父会话 ID，无则 None |
| slug | NOT NULL，URL slug，如 `import-<ts_hex>` |
| directory | NOT NULL，工作目录路径（导入时由 `resolve_work_dir()` 探测：显式参数 > 环境变量 > 数据库 session.directory > 当前目录 > 主目录） |
| title | NOT NULL，任务显示名 |
| version | NOT NULL，客户端版本如 `"1.2.27"`（导入脚本从现有 session 动态读取，避免硬编码） |
| share_url | 分享 URL，可为 None |
| summary_additions/deletions/files | 整数统计字段，默认 0 |
| summary_diffs | 可为 None |
| revert | 可为 None |
| permission | 可为 None |
| time_created | NOT NULL，毫秒时间戳 |
| time_updated | NOT NULL，毫秒时间戳 |
| time_compacting | 可为 None |
| time_archived | 可为 None |
| workspace_id | 可为 None |

### 创建新 session 的 SQL

```python
# 注意：第 5 个占位符对应 directory 列，必须传 directory 参数（工作目录路径），
# 不能用 work_dir（旧参数名默认 None），否则触发 NOT NULL constraint failed: session.directory
cur.execute(
    "INSERT INTO session (id, project_id, parent_id, slug, directory, title, version, "
    "share_url, summary_additions, summary_deletions, summary_files, summary_diffs, "
    "revert, permission, time_created, time_updated, time_compacting, time_archived, workspace_id) "
    "VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)",
    (session_id, "global", None, slug, directory, title, version, None, 0, 0, 0, None, None, None, now_ts, now_ts, None, None, None)
)
```

## message 表

| 关键字段 | 说明 |
|----------|------|
| id | message ID，格式 `msg_<ts_hex>01<rand16>` |
| session_id | 关联的 session ID |
| time_created | 毫秒时间戳 |
| time_updated | 毫秒时间戳 |
| data | JSON 字符串，**仅含 role/agent/model，无正文** |

### message.data 结构

```python
# 用户消息
{"role":"user","agent":"opencowork-default","model":{"providerID":"NewApi","modelID":"chat-pro"},"time":{"created":ts},"queryID":"q_<uuid>"}
# 智能体消息
{"role":"assistant","parentID":"<同轮用户消息id>","modelID":"chat-pro","providerID":"NewApi","agent":"opencowork-default","mode":"","path":{"cwd":"","root":""},"time":{"created":ts,"completed":ts+1000},"cost":0,"tokens":{"total":0,"input":0,"output":0,"reasoning":0,"cache":{"read":0,"write":0}},"finish":"stop"}
```

## part 表

| 关键字段 | 说明 |
|----------|------|
| id | part ID，格式 `prt_<ts_hex>01<rand16>` |
| message_id | 关联的 message ID |
| session_id | 关联的 session ID |
| time_created | 毫秒时间戳 |
| time_updated | 毫秒时间戳 |
| data | JSON 字符串，含 type 和实际内容 |

### part.data 结构

```python
{"type":"text","text":"<正文>"}
{"type":"reasoning","text":"<思考过程>"}
{"type":"step-start"}
{"type":"step-finish"}
{"type":"compaction"}  # 可跳过
```

### part.type 含义

| type | 说明 | 提取方式 |
|------|------|----------|
| text | 用户输入或智能体回复正文 | `d['text']` |
| reasoning | 智能体思考过程 | `d['text']`，必须提取 |
| tool | 工具调用 | 含文件路径等，用正则提取产出文件路径 |
| step-start / step-finish | 步骤标记 | 导入时需生成 |
| compaction | 上下文压缩标记 | 可跳过 |

## ID 生成规则

```python
import secrets, string
def gen_id(prefix, ts_ms):
    ts_hex = f"{ts_ms:x}"[-11:]
    rand = ''.join(secrets.choice(string.ascii_letters + string.digits) for _ in range(16))
    return f"{prefix}{ts_hex}01{rand}"
# 示例: msg_0381c35db001ZZC6I5NlPPcAn9, prt_0381c35e1001xoKNYuyQaMquaZ
# 前缀: session=ses_, message=msg_, part=prt_
```

> AI生成