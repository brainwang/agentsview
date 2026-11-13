---
name: teleagent-task-share
description: 导出/导入TeleAgent本地任务完整数据（对话记录+思考过程+产出文件+技能包）为ZIP压缩包，支持多任务批量操作。导出时可脱敏（手机号/身份证/银行卡/邮箱），导入时含技能包安全审查与逐个确认。当用户要求"导出任务""把任务发给别人""任务交接""导入任务""恢复任务"等时触发。覆盖任务定位、对话重建、产出文件复制、ZIP打包与导入回写数据库全流程，纯本地处理不联网。不用于导出Excel/PPT文件或数据库备份。
name_cn: 星辰超级智能体任务共享
description_cn: 导出/导入TeleAgent本地任务完整数据为ZIP，支持脱敏与技能包安全审查，纯本地不联网。
AIGC:
  ContentProducer: 001191110102MAD55U9H0F10002
  ContentPropagator: 001191110102MAD55U9H0F10002
  Label: '1'
  ProduceID: 12748774-2dc7-42a6-9ad1-9bb251b44414
  PropagateID: 12748774-2dc7-42a6-9ad1-9bb251b44414
  ReservedCode1: e6498d0d-0c25-438d-9b1c-2d8fc04174aa
  ReservedCode2: e6498d0d-0c25-438d-9b1c-2d8fc04174aa
---
# TeleAgent 任务共享

## 概述

从 TeleAgent 本地 SQLite 数据库 `teleagent.db` 导出任务完整数据为 ZIP 压缩包，或将 ZIP 导入回数据库自动创建新任务。支持多任务批量操作，导出时可脱敏，导入前需用户确认。

- **导出**：支持一个或多个任务同时导出，每个任务输出对话记录+思考过程+产出文件+技能包（如有），固定打包为 ZIP。导出前自动扫描对话内容中的敏感信息（手机号/身份证号/银行卡号/邮箱），交互式询问是否脱敏；也可用 `--sanitize` 直接开启、用 `--no-prompt-sanitize` 跳过询问。
- **导入**：读取 ZIP 中每个任务子目录，自动创建对应数量的新 session。导入前列出任务清单并请用户确认，导入后自动刷新客户端，任务栏立即可见。若 ZIP 中含技能包，自动探测技能安装目录并安装。

## 数据存储位置（自动探测）

脚本会自动探测数据库与工作目录，无需硬编码路径。也可通过环境变量或命令行参数覆盖：

| 项目 | 自动探测规则 | 覆盖方式 |
|------|--------------|----------|
| 数据库 | 依次检查各平台常见位置（Windows `%USERPROFILE%\.local\share\TeleAgent\`、`%LOCALAPPDATA%\TeleAgent\`、`%APPDATA%\TeleAgent\`；macOS `~/Library/Application Support/TeleAgent/`、`~/.local/share/TeleAgent/`；Linux `~/.local/share/TeleAgent/`、`$XDG_DATA_HOME/TeleAgent/`、`~/.config/TeleAgent/`） | `--db` 或环境变量 `TELEAGENT_DB` |
| 工作目录 | 从数据库现有 session 的 `directory` 字段自动探测（优先非空、存在、非 TeleAgent 默认工作空间的目录，按最近使用排序）→ 当前目录（可写）→ 用户主目录 | `--work-dir` 或环境变量 `TELEAGENT_WORK_DIR` |
| 客户端窗口标题 | 含 `TeleAgent` 的窗口 | `--window-title` 或环境变量 `TELEAGENT_WINDOW_TITLE`（导入后自动刷新用） |
| 技能安装目录 | 环境变量 `TELEAGENT_CONFIG_DIR` → `$TELEAGENT_CONFIG_DIR/skills/` → 平台候选路径探测（`~/.config/TeleAgent/skills/`、`~/.config/teleai-super-agent/skills/` 等） | 自动探测，无需手动指定 |

- 数据库：默认从上述候选路径探测 `teleagent.db`，未找到时报错并提示用 `--db` 指定
- 已删除会话记录：同目录 `deleted-session-ids.json`
- 产出文件：任务工作目录（默认自动探测：数据库现有 session 的 `directory` → 当前目录 → 用户主目录）

> 数据库表结构、ID 生成规则、message/part 数据格式等技术细节，详见 [references/db-schema.md](references/db-schema.md)。

## 导出工作流

### 1. 定位任务（支持多个）

```python
# 按标题模糊搜索，支持多个关键词
for kw in keywords:
    cur.execute("SELECT id, title, directory, time_created, time_updated FROM session WHERE title LIKE ? ORDER BY time_updated DESC", (f'%{kw}%',))
    # 标题搜不到时用 part 内容关键词反查
    cur.execute("SELECT DISTINCT session_id FROM part WHERE data LIKE ?", (f'%{kw}%',))
```

**注意**：TeleAgent 前端在 UI 中改名只写入 Electron LevelDB（Local Storage），不同步 SQLite `session.title`。导出脚本现自动从 LevelDB 读取 UI 显示名映射，搜索时同时匹配 DB title 和 UI 显示名，导出标题优先使用 UI 显示名。若两者均未命中，再用 part 内容关键词反查 session_id。自动跳过 `_SYS_` 系统会话。

**多任务选择**：当匹配到多个任务时，脚本列出匹配清单并交互式询问用户选择导出哪些任务。用户可输入序号选择（多个用空格或逗号分隔，如 `1 3` 或 `1,3`）、输入 `a` 全部导出、输入 `q` 取消。`--no-confirm` 可跳过交互直接全部导出，`--select` 可在命令行直接指定序号（如 `--select 1 3`）。

**Agent 调用时的选择流程**：Agent 通过 PowerShell 调用脚本时无 stdin，交互式 `input()` 会抛 EOFError。正确做法：先 `--dry-run` 预览匹配清单，若匹配多个任务（或用户指定的任务名与 DB 标题不一致、疑似匹配错误），先向用户确认要导出哪一个/哪几个任务，再用 `--select` 指定序号或 `--no-confirm` 直接导出。切勿在未确认的情况下直接导出全部匹配任务。

### 2. 提取对话与产出文件

- 遍历 message + part 表，type=text 提取正文，type=reasoning 提取思考过程（用 `<details>` 折叠展示）
- 遍历 type=tool 的 part，从明确文件字段（`report_final_files.files` / `filePath` / `metadata.filepath`）提取文件路径（跨平台），默认过滤 `.temp` 中间目录，确认存在后复制到打包目录
- **技能包检测**：仅从 `report_final_files`/`write`/`edit` 工具的文件路径中检测 `skills/技能名/` 目录结构，向上查找含 `SKILL.md` 的技能根目录，递归打包整个技能目录到 `__skill_packages__/` 子目录。`read`/`glob` 等只读工具引用的 skills 路径不纳入（避免误打包被参考读取的其他技能）。已在技能包中打包的文件不再单独复制。

### 3. 敏感数据脱敏（可选）

导出默认读取并打包完整对话记录、思考过程与产出文件。脱敏(`--sanitize`)为可选参数，仅覆盖手机号/身份证号/银行卡号/邮箱等格式化信息，不涉及用户名/用户目录等非格式化信息。

**导出时自动检测并询问**：未显式指定 `--sanitize` 时，脚本导出前自动扫描所有待导出任务的对话内容中是否包含格式化 PII（手机号/身份证号/银行卡号/邮箱）。若检测到，交互式询问是否脱敏：

```
检测到对话中包含敏感信息：[已脱敏:手机号] 3 处、[已脱敏:邮箱] 1 处
是否对相关信息进行脱敏？(y/n)
```

```python
# 脱敏规则（按匹配顺序执行，覆盖对话/标题/README/session_meta 文本字段）
# 身份证号（18位）→ [已脱敏:身份证号]
# 手机号（1开头11位） → [已脱敏:手机号]
# 银行卡号（16-19位数字）→ [已脱敏:银行卡号]
# 邮箱                → [已脱敏:邮箱]
```

| 场景 | 参数 | 行为 |
|------|------|------|
| 默认（不带任何脱敏参数） | — | 扫描 PII → 有则询问 y/n → 按用户选择执行 |
| 强制脱敏 | `--sanitize` | 直接脱敏，不询问 |
| 强制不脱敏 | `--no-prompt-sanitize` | 跳过扫描与询问，直接导出不脱敏 |

**提醒**：脱敏仅覆盖格式化 PII。对话中的人名、项目编号、业务数据等非格式化敏感信息需人工审查。

### 4. ZIP 打包结构

```
任务导出包_YYYYMMDD_HHMMSS.zip
├── README.md                    # 导出说明（任务列表+使用方法）
├── 任务A/                        # 每个任务一个子目录（以任务名命名）
│   ├── session_meta.json         # 任务元信息（标题/ID/时间/工作目录/模型/工具调用/技能包信息）
│   ├── 完整对话与思考过程.md      # 对话记录+思考过程
│   ├── 产出文件清单.txt           # 产出文件名列表
│   ├── 产出文件...               # 实际产出文件（如 .html, .docx 等）
│   └── __skill_packages__/       # 技能包目录（如有）
│       └── 技能名/               # 完整技能目录（含 SKILL.md/scripts/references 等）
├── 任务B/
│   ├── session_meta.json
│   ├── 完整对话与思考过程.md
│   └── ...
```

```powershell
$env:PYTHONUTF8="1"
$env:PYTHONDONTWRITEBYTECODE=1
python -B scripts/extract_session.py --search "年会抽奖" "需求梳理" --output "导出包.zip"
# 指定数据库/工作目录（通常无需，自动探测即可）：
python -B scripts/extract_session.py --search "年会抽奖" --output "导出包.zip" --db "C:\path\to\teleagent.db" --work-dir "D:\work"
# 导出并自动脱敏：
python -B scripts/extract_session.py --search "年会抽奖" --output "导出包.zip" --sanitize
```

| 参数 | 说明 |
|------|------|
| `--search` | 任务标题关键词，可指定多个（空格分隔） |
| `--db` | teleagent.db 路径（默认自动探测，可用环境变量 `TELEAGENT_DB`） |
| `--output` | 输出 ZIP 路径（默认工作目录下自动命名） |
| `--work-dir` | 工作目录（默认自动探测，可用环境变量 `TELEAGENT_WORK_DIR`） |
| `--sanitize` | 导出时直接脱敏（手机号/身份证/银行卡/邮箱），不询问 |
| `--no-prompt-sanitize` | 跳过交互式脱敏询问（未指定 `--sanitize` 时生效，直接导出不脱敏） |
| `--no-confirm` | 跳过多任务交互选择（直接全部导出） |
| `--dry-run` | 仅预览任务与文件数，不写 ZIP |
| `--select` | 按序号只导出指定任务（1-based，可多个） |
| `--max-file-size` | 跳过超过该字节数的产出文件 |
| `--include-temp` | 导出时包含 .temp 中间文件（默认过滤） |
| `--self-test` | 用临时库跑导出闭环自检，不碰真实数据库 |

## 导入工作流

### 1. 解压并扫描任务

```python
# 解压 ZIP 到 .temp 临时目录（逐条校验路径穿越，跳过可疑条目）
# 扫描含 session_meta.json 的子目录，每个目录代表一个任务
# 无 session_meta.json 时退而查找含 .md 对话文件的目录
```

**安全措施**：解压时逐条校验 ZIP 条目路径是否在解压目录范围内，跳过路径穿越攻击条目；解压总大小上限 2GB 防 zip 炸弹。

### 2. 逐任务创建新 session 并写入数据

每个任务子目录创建一个新 session：
- 从 session_meta.json 读取原始任务信息（含产出文件原路径映射、模型信息、技能包信息）
- **恢复模型信息**：从 session_meta.json 的 `models`/`providers` 字段恢复原对话使用的模型，不再硬编码 chat-pro
- **安装技能包**：检测 `__skill_packages__/` 目录，自动探测目标用户技能安装目录（`resolve_skill_storage_dir()`），将技能目录复制到 `<skills_dir>/技能名/` 下，对方刷新后即可在技能列表看到。同名技能自动加 `_imported` 后缀。
- **技能包安全审查**：导入时对每个技能包进行独立安全审查，包括 SKILL.md 完整性检查、frontmatter 必需字段检查、脚本安全扫描（检测 subprocess/os.system/eval/exec/网络请求/文件删除等高风险模式）、非常见文件格式检查。审查报告展示给用户后，逐个技能包单独确认是否安装，用户可选择安装、跳过或查看完整警告。致命问题（如缺少 SKILL.md）的技能包自动跳过。
- **复制产出文件**到目标用户工作目录（避免覆盖已有文件，重名时自动加 `_imported` 后缀）
- **替换对话中的文件路径**：将原始用户的工作目录路径替换为目标用户的工作目录，确保对话中引用的文件路径在本机有效
- 创建新 session（补全 project_id/slug/version 等必填字段）
- 解析 Markdown 对话序列，生成 message + part 记录写入数据库
- 时间戳使用原始任务时间，UI 按时间排序自动显示在顶部
- **重名检测**：目标已有同名任务时自动加 `_imported` 后缀

### 3. 技能包安全审查与单独确认

任务导入确认后、技能安装前，脚本对 ZIP 中所有技能包进行独立安全审查：

1. **SKILL.md 完整性**：缺少 SKILL.md 的技能包直接标记为致命问题，自动跳过
2. **Frontmatter 检查**：检查 name、description 等建议字段是否缺失
3. **脚本安全扫描**：扫描 `.py`/`.js`/`.sh`/`.ps1` 脚本中的高风险模式：
   - `subprocess.call/run/Popen` — 子进程调用
   - `os.system` / `os.popen` — 系统命令执行
   - `eval` / `exec` — 动态代码执行
   - `__import__` — 动态导入
   - `requests.get/post` / `urllib.request` / `socket.connect` — 网络请求
   - `open(..., 'w')` — 文件写入
   - `shutil.rmtree` / `os.remove` / `os.unlink` — 文件删除
4. **文件格式检查**：标记不在技能常见格式列表中的文件
5. **文件树展示**：向用户展示技能包的完整目录结构

审查完成后，逐个技能包展示报告并询问用户是否安装（`y/n`），用户单独确认后才安装对应技能。`--no-confirm` 模式下自动跳过技能安装。

### 4. 导入前确认

导入写入数据库前，脚本列出即将导入的任务清单（原名→新名），请用户确认：
- 列出任务数量、每个任务的原名和新名称
- 用户输入 y 确认后才开始写入数据库
- `--no-confirm` 可跳过确认（适用于自动化脚本）
- `--dry-run` 可仅预览不写入

### 4. 使用脚本导入

```powershell
$env:PYTHONUTF8="1"
$env:PYTHONDONTWRITEBYTECODE=1
python -B scripts/import_session.py --zip "任务导出包.zip" --backup
# 可自定义新任务名：
python -B scripts/import_session.py --zip "导出包.zip" --session-titles "新任务1" "新任务2" --backup
# 跳过确认（自动化场景）：
python -B scripts/import_session.py --zip "导出包.zip" --no-confirm --backup
# 仅预览不写入：
python -B scripts/import_session.py --zip "导出包.zip" --dry-run
# 不自动刷新（避免抢焦点）：
python -B scripts/import_session.py --zip "导出包.zip" --no-confirm --no-refresh
```

| 参数 | 说明 |
|------|------|
| `--zip` | 任务导出 ZIP 文件路径 |
| `--db` | teleagent.db 路径（默认自动探测，可用环境变量 `TELEAGENT_DB`） |
| `--session-titles` | 可选，自定义各任务新名称（按 ZIP 中目录顺序对应） |
| `--backup` | 导入前自动备份数据库（默认开启） |
| `--no-backup` | 跳过备份 |
| `--work-dir` | 工作目录（默认自动探测：数据库 session.directory → 当前目录 → 主目录，可用环境变量 `TELEAGENT_WORK_DIR`） |
| `--window-title` | 客户端窗口标题关键词（默认 TeleAgent，可用环境变量 `TELEAGENT_WINDOW_TITLE`） |
| `--dry-run` | 仅预览不写入 |
| `--no-confirm` | 跳过导入前用户确认 |
| `--no-refresh` | 导入后不自动刷新客户端（避免抢窗口焦点） |
| `--self-test` | 用临时库跑导入闭环自检，不碰真实数据库 |

### 5. 导入后

- 脚本依次通过 PostMessage 和 PowerShell SendKeys 两种方式向 TeleAgent 窗口发送 Ctrl+R 刷新客户端（降级链，详见下文）
- 若两种方式均失败（如窗口未找到），提示用户手动按 Ctrl+R 刷新
- 历史消息时间戳使用原始任务时间，UI 按时间排序自动显示在对话顶部
- 数据库已备份，如需回滚可恢复备份文件

> **为什么需要刷新？** TeleAgent 前端通过 SSE（Server-Sent Events）订阅 Go 后端 `/global/event` 端点接收 `session.created` 等事件来更新任务栏。直接写 SQLite 数据库不会触发 Go 后端推送 SSE 事件，因此前端无法感知新 session。刷新键触发前端 SSE 重连，重连时调用 `/global/session/status` 获取最新 session 列表。Go 后端 API（后端API端口）使用 `HMAC签名认证`，无法从外部 Python 脚本直接调用，因此采用发送 **Ctrl+R** 键的方案（F5 对 TeleAgent 的 Electron 前端无效，已验证）。

> **刷新降级链（Ctrl+R 三级保障）**：
> 1. **PostMessage**（`user32.PostMessageW`）— 非侵入式，窗口无需前台聚焦；但对 Electron/Chromium 前端可能**静默失败**（函数返回成功但前端无反应）
> 2. **PowerShell SendKeys**（`System.Windows.Forms.SendKeys::SendWait("^r")`）— 需 `SetForegroundWindow` 将窗口激活到前台；对 Electron 更可靠，作为降级方案始终追加执行
> 3. **手动提示** — 两种方式均失败时提示用户手动按 Ctrl+R
>
> 脚本始终先 PostMessage 再 SendKeys，不依赖单一机制的可靠性。

## 陷阱与注意事项

1. **PowerShell 中文乱码与 .pyc 防护**：python 输出前加 `sys.stdout.reconfigure(encoding='utf-8')`；命令前加 `$env:PYTHONUTF8="1"`；防止 .pyc 缓存加 `$env:PYTHONDONTWRITEBYTECODE=1` 或用 `python -B`。脚本内已设 `sys.dont_write_bytecode = True` 三重防护。
2. **message 表无正文**：正文在 part 表，必须按 message_id 关联
3. **标题不一致**：UI 任务名可能与 DB title 不同，用 part 内容关键词兜底
4. **session 表必填字段**：project_id、slug、directory、title、version、time_created、time_updated 均为 NOT NULL
5. **part 表 6 列**：id, message_id, session_id, time_created, time_updated, data 缺一不可
6. **type 在 JSON 内**：part 表无独立 type 列，查询/过滤需 `json.loads(data).get('type')`
7. **导入前必须备份数据库**：写入操作不可逆，备份命名 `teleagent.db.bak_YYYYMMDD_HHMM`
8. **导入消息时间戳**：使用原始任务时间戳（毫秒），确保历史消息排在当前会话消息之前
9. **数据库锁定**：TeleAgent 运行时可能锁定数据库，写入前确认连接成功
10. **多任务去重**：导出时按 session_id 去重，避免同一任务被重复打包
11. **敏感数据审查**：脱敏仅覆盖手机号/身份证/银行卡/邮箱等格式化 PII，人名/项目编号/业务数据等非格式化敏感信息需人工审查。导出时默认自动扫描并交互式询问是否脱敏，`--no-prompt-sanitize` 可跳过询问。
12. **路径穿越防护**：导入解压时逐条校验 ZIP 条目路径，跳过不在解压目录范围内的可疑条目
13. **工作目录探测优先级**：`resolve_work_dir()` 按优先级探测：显式参数 > 环境变量 > 数据库 session.directory > 当前目录 > 主目录。排除空值和 TeleAgent 默认"工作空间"路径。
14. **刷新键用 Ctrl+R 而非 F5**：F5 对 TeleAgent（Electron）前端无效（`PostMessage` 发送 `WM_KEYDOWN`/`WM_KEYUP` 的 F5 虚拟键码 `0x74` 不触发前端刷新）。Ctrl+R（`VK_CONTROL=0x11` + `VK_R=0x52`）可触发前端 SSE 重连，重新调用 `/global/session/status` 加载最新任务列表。
15. **刷新降级链（PostMessage → SendKeys → 手动）**：PostMessage 对 Electron 可能静默失败（返回成功但前端无反应）。脚本先 PostMessage（非侵入式），再通过 PowerShell 子进程 SetForegroundWindow + SendKeys 追加发送（需窗口前台聚焦但对 Electron 可靠），两者均失败时提示手动按 Ctrl+R。
16. **非交互式环境 input() EOFError**：当脚本由 Agent 通过 PowerShell 工具调用时（无 stdin），交互确认的 `input()` 会抛 `EOFError` 导致脚本崩溃。已增加 `try/except EOFError` 保护，提示用户添加 `--no-confirm`。Agent 调用导入脚本时应始终添加 `--no-confirm` 参数。
17. **直接写库不触发 SSE**：Python 脚本直接写 SQLite 不会触发 Go 后端推送 SSE 事件，前端无法感知新 session，必须靠刷新键让前端重连 SSE 并拉取 `/global/session/status`。Go 后端 API 使用 HMAC 签名认证，外部脚本无法直接调用。
18. **create_session 参数名陷阱**：`create_session()` 的 SQL 中 `directory` 列必须用 `directory` 参数（工作目录），不能用 `work_dir`（旧参数名默认 None），否则触发 `NOT NULL constraint failed: session.directory`。
19. **回调函数变量作用域**：`refresh_teleagent_client()` 中 `EnumWindows` 回调内的 `buf.value` 是局部变量，回调外引用会导致 `NameError`。窗口标题关键词应通过外层 `title_kw` 变量传递。
20. **导入时产出文件复制与路径替换**：导入时读取产出文件清单，将文件复制到目标用户工作目录（重名时加 `_imported` 后缀），并将对话中的原始工作目录路径替换为目标用户工作目录。导出时在 session_meta.json 中记录产出文件原路径与打包文件名的映射，供导入时精确替换。
21. **tool part JSON 序列化格式差异**：数据库 `part.data` 是紧凑无空格 JSON（`"type":"tool"`），而 Python `json.dumps` 默认带空格（`"type": "tool"`）。SQL 过滤 tool part 应使用宽泛 `LIKE '%"tool"%'` 配合 Python 侧 `json.loads(data).get('type') == 'tool'` 精确判断，避免依赖序列化格式导致匹配 0 条。
22. **产出文件路径提取性能**：聚焦明确文件字段（`report_final_files.files` / `filePath` / `metadata.filepath`），不扫描超长 output/content 文本，避免大任务超时。
23. **.temp 中间产物过滤**：write 工具的 filePath 常指向 .temp 下的脚本（中间产物，非最终交付）。默认过滤 `.temp` 目录，仅 report_final_files 声明的文件始终保留；`--include-temp` 可关闭过滤。
24. **数据库写锁与备份一致性**：连接必须 `PRAGMA busy_timeout=10000` 避免 `database is locked`；备份必须用 SQLite `backup` API（而非 `shutil.copy2`），否则 WAL 未 checkpoint 数据丢失。
25. **多任务导入事务保护**：逐任务 commit 会在中途失败时留下脏数据；应全部任务成功后统一 commit，任一失败整体 rollback。
26. **模型信息恢复**：导入时从 session_meta.json 的 `models`/`providers` 字段恢复原对话模型，不再硬编码 chat-pro。
27. **关键词模糊匹配可能导出错误任务**：`--search` 是标题 LIKE 模糊匹配，用户口中的任务名可能与 DB 标题不一致（如用户说"控制浏览器"，DB 标题是"192.168.10.130页面数据操作192.168.10.191"），且当前会话自身的标题也可能命中关键词（如"控制浏览器任务导出"）。导出前必须先用 `--dry-run` 预览匹配清单，若匹配结果与用户预期不符（标题不一致、疑似匹配到当前会话），用 part 内容关键词反查定位真实任务，并向用户确认后再导出。多任务匹配时交互式选择（序号/a/q）或 `--select` 指定，避免误导出。
28. **防止 .pyc 字节码缓存**：脚本运行时加载 common.py 可能生成 `scripts/__pycache__/common.cpython-XX.pyc`，被 TeleAgent 审核系统判为恶意扩展名（.pyc 禁止导入）。已通过四层防护修复：①脚本内 `sys.dont_write_bytecode = True`（importlib 加载前设置）；②SKILL.md 命令示例统一用 `python -B`；③PowerShell 示例加 `$env:PYTHONDONTWRITEBYTECODE=1`；④脚本启动时主动 `shutil.rmtree(__pycache__)` 清理残留 + `atexit` 退出时再次清理（双保险）。加载方式已从 `exec(compile(...))` 改为 `importlib.util.spec_from_file_location`，消除源码中的显式 exec 调用。若仍遇拦截，手动删除 `scripts/__pycache__/` 目录即可。

## 支持脚本

- `scripts/common.py`：公共模块（路径探测、数据库连接/备份、跨平台路径提取、脱敏、ZIP 安全解压），供两脚本共用
- `scripts/extract_session.py`：多任务导出到 ZIP（`--search`、`--output`、`--sanitize`、`--no-prompt-sanitize`、`--dry-run`、`--select`、`--max-file-size`、`--include-temp`、`--self-test`）
- `scripts/import_session.py`：多任务 ZIP 导入到数据库（`--zip`、`--session-titles`、`--backup`、`--dry-run`、`--no-confirm`、`--no-refresh`、`--self-test`）

## 参考文档

- [references/db-schema.md](references/db-schema.md)：数据库表结构、ID 生成规则、message/part 数据格式（编写脚本时查阅）