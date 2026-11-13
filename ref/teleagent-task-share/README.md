---
AIGC:
  ContentProducer: '001191110102MAD55U9H0F10002'
  ContentPropagator: '001191110102MAD55U9H0F10002'
  Label: '1'
  ProduceID: 'ac20ad42-b503-4542-b017-0da7cd832ffb'
  PropagateID: 'ac20ad42-b503-4542-b017-0da7cd832ffb'
  ReservedCode1: '14049a3a-cc7d-4139-a557-4b83c180ddcd'
  ReservedCode2: '14049a3a-cc7d-4139-a557-4b83c180ddcd'
---

# TeleAgent 任务共享

## 解决什么问题

从 TeleAgent 本地数据库导出任务完整数据（对话记录+思考过程+产出文件+技能包）为 ZIP 压缩包，或将 ZIP 导入回数据库自动创建新任务。支持多任务批量操作，导出时可脱敏，导入时含技能包安全审查。

## 安装

**方式一**：把本仓库地址告诉智能体，由智能体自动安装。

**方式二**：将技能目录复制到智能体的 skills 目录下。

## 使用

对智能体说："导出任务""把任务发给别人""任务交接""导入任务""恢复任务"等。

## 配置

无需额外配置。脚本自动探测数据库路径和工作目录，支持环境变量覆盖（TELEAGENT_DB、TELEAGENT_WORK_DIR）。

## 依赖与兼容

- Python 3.10+
- 脚本依赖：标准库（sqlite3, zipfile, paramiko 可选）
- 兼容：Windows、macOS、Linux
- 数据库自动探测，无需手动指定路径
- 不支持：导出技能包、导出 Excel/PPT 文件、数据库备份

## 数据边界

- 脱敏覆盖手机号/身份证/银行卡/邮箱，人名等非格式化信息需人工审查
- 导入前自动备份数据库
- 导入解压时逐条校验路径穿越，防 zip 炸弹（2GB 上限）
- 技能包导入前独立安全审查（脚本扫描+文件格式检查）

> AI生成