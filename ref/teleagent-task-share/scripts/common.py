#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""TeleAgent 任务共享技能 · 公共工具模块。

供 extract_session.py 与 import_session.py 共用，避免重复实现。
包含：数据库/工作目录自动探测、数据库连接（busy_timeout）、
跨平台文件路径提取、脱敏、ZIP 安全解压、SQLite 一致性备份。

仅依赖 Python 标准库（Python 3.8+）。
"""
import os
import re
import sys
import shutil
import sqlite3
import datetime
import zipfile

# ---------------------------------------------------------------------------
# 数据库候选路径（各平台，按优先级）
# ---------------------------------------------------------------------------
_CANDIDATE_DB_RELPATHS = [
    (r"%USERPROFILE%\.local\share\TeleAgent\teleagent.db", "USERPROFILE"),
    (r"%LOCALAPPDATA%\TeleAgent\teleagent.db", "LOCALAPPDATA"),
    (r"%APPDATA%\TeleAgent\teleagent.db", "APPDATA"),
    (r"$HOME/Library/Application Support/TeleAgent/teleagent.db", "HOME"),
    (r"$HOME/.local/share/TeleAgent/teleagent.db", "HOME"),
    (r"$XDG_DATA_HOME/TeleAgent/teleagent.db", "XDG_DATA_HOME"),
    (r"$HOME/.config/TeleAgent/teleagent.db", "HOME"),
]


def _expand_env(path_tpl, var):
    """按模板展开环境变量；var 缺失时返回 None。"""
    if var == "HOME" and os.name == "nt":
        home = os.environ.get("USERPROFILE") or os.environ.get("HOME")
    else:
        home = os.environ.get(var)
    if not home:
        return None
    return (
        path_tpl.replace("$HOME", home)
        .replace("%USERPROFILE%", home)
        .replace("%LOCALAPPDATA%", os.environ.get("LOCALAPPDATA", ""))
        .replace("%APPDATA%", os.environ.get("APPDATA", ""))
        .replace("$XDG_DATA_HOME", os.environ.get("XDG_DATA_HOME", ""))
    )


def resolve_db_path(explicit=None):
    """解析数据库路径：显式 > 环境变量 TELEAGENT_DB > 平台候选路径探测。"""
    if explicit:
        return os.path.abspath(explicit)
    env = os.environ.get("TELEAGENT_DB", "").strip()
    if env:
        return os.path.abspath(env)
    for tpl, var in _CANDIDATE_DB_RELPATHS:
        p = _expand_env(tpl, var)
        if p and os.path.isfile(p):
            return p
    raise FileNotFoundError(
        "未找到 teleagent.db，请通过 --db 或环境变量 TELEAGENT_DB 指定数据库路径"
    )


def _probe_work_dir_from_db(db_path):
    """从数据库现有 session 的 directory 字段探测真实工作目录。"""
    if not db_path:
        return None
    try:
        conn = sqlite3.connect(f"file:{db_path}?mode=ro", uri=True)
        cur = conn.cursor()
        cur.execute(
            "SELECT directory FROM session "
            "WHERE directory IS NOT NULL AND TRIM(directory) != '' "
            "AND directory NOT LIKE '%\\TeleAgent\\%工作空间%' "
            "AND directory NOT LIKE '%/TeleAgent/%工作空间%' "
            "ORDER BY time_updated DESC"
        )
        rows = [r[0] for r in cur.fetchall()]
        conn.close()
        for d in rows:
            d = d.strip()
            if d and os.path.isdir(d):
                return d
        return None
    except Exception:
        return None


def resolve_work_dir(explicit=None, db_path=None):
    """解析工作目录：显式 > 环境变量 TELEAGENT_WORK_DIR > 数据库探测 > 当前目录 > 主目录。"""
    if explicit:
        return os.path.abspath(explicit)
    env = os.environ.get("TELEAGENT_WORK_DIR", "").strip()
    if env:
        return os.path.abspath(env)
    probed = _probe_work_dir_from_db(db_path)
    if probed:
        return probed
    cwd = os.path.abspath(os.getcwd())
    if os.access(cwd, os.W_OK):
        return cwd
    return os.path.expanduser("~")


# ---------------------------------------------------------------------------
# 技能安装目录探测
# ---------------------------------------------------------------------------
_SKILL_IGNORE = shutil.ignore_patterns('__pycache__', '.git', '*.pyc', '*.pyo', '.DS_Store')


def load_ui_titles(db_path):
    """从 Electron Local Storage (LevelDB) 读取 UI 显示名映射。

    TeleAgent 前端在 LevelDB 中存储 session 相关数据（UTF-16LE 编码）：
    1. 完整 session 对象："ses_xxx":{"id":"ses_xxx","title":"原标题",...}
    2. UI 改名映射（简单字符串）："ses_xxx":"新标题"
    3. 其他映射：目录路径、数值、布尔值等（需过滤）

    用户在 UI 中改名只更新第 2 种，不同步 SQLite session.title。

    核心实现：用二进制搜索 session ID（UTF-16LE 字节），找到后提取局部窗口
    再解码，避免 LevelDB 二进制记录头破坏整文件 UTF-16LE 解码的字节对齐。

    返回 dict: {session_id: display_name}，读不到返回空 dict。
    """
    data_dir = os.path.dirname(db_path)
    leveldb_dir = os.path.join(data_dir, "Local Storage", "leveldb")
    if not os.path.isdir(leveldb_dir):
        return {}

    # "ses_" in UTF-16LE bytes
    sid_prefix_u16 = b's\x00e\x00s\x00_\x00'
    # Session ID chars after "ses_" (ASCII alphanumeric, each 2 bytes in UTF-16LE)
    sid_char_re = re.compile(rb'(?:[a-zA-Z0-9]\x00){20,}')

    # After SID, value pattern check (decoded text):
    # Rename: ":"string_value"  (value is a plain string, not object/path/number)
    # Object: ":{"id":"ses_xxx",...,"title":"original_title",...}
    _rename_value_re = re.compile(r'^":\s*"([^"]{1,200})"')
    _obj_title_re = re.compile(r'^":\s*\{[^}]*?"title"\s*:\s*"([^"]{1,200})"')

    obj_titles = {}      # low priority: original title from session object
    rename_titles = {}   # high priority: UI rename mapping

    for fname in sorted(os.listdir(leveldb_dir)):
        fpath = os.path.join(leveldb_dir, fname)
        if not os.path.isfile(fpath):
            continue
        try:
            with open(fpath, 'rb') as f:
                raw = f.read()
        except (PermissionError, OSError):
            continue

        # Binary search for "ses_" prefix (UTF-16LE)
        pos = 0
        while True:
            idx = raw.find(sid_prefix_u16, pos)
            if idx < 0:
                break
            pos = idx + 2  # advance 2 bytes (1 UTF-16LE char)

            # Extract SID chars after "ses_" prefix
            sid_chars_start = idx + len(sid_prefix_u16)
            sid_m = sid_char_re.match(raw, sid_chars_start)
            if not sid_m:
                continue
            sid = 'ses_' + sid_m.group().replace(b'\x00', b'').decode('ascii', errors='ignore')
            if len(sid) < 25:
                continue

            # Extract window right after SID chars (aligned at even byte position)
            sid_end = sid_m.end()
            window = raw[sid_end:sid_end + 600]
            try:
                text = window.decode('utf-16-le', errors='replace')
            except Exception:
                continue

            # Check for rename mapping: ":"string_value"
            rm = _rename_value_re.match(text)
            if rm:
                value = rm.group(1)
                # Filter out directory paths, booleans, numbers
                if re.match(r'^[A-Za-z]:[\\/]', value):
                    continue
                if '\\' in value or '/' in value:
                    continue
                if value in ('true', 'false', 'null'):
                    continue
                if value.isdigit():
                    continue
                if sid not in rename_titles:
                    rename_titles[sid] = value
                continue

            # Check for full object with title field
            om = _obj_title_re.match(text)
            if om:
                if sid not in obj_titles:
                    obj_titles[sid] = om.group(1)

    # Merge: rename (high priority) overrides obj titles
    ui_titles = {}
    ui_titles.update(obj_titles)
    ui_titles.update(rename_titles)
    return ui_titles


def resolve_skill_storage_dir():
    """解析技能安装目录：环境变量 > 候选路径探测 > None。

    优先级：
    1. 环境变量 TELEAGENT_CONFIG_DIR → $TELEAGENT_CONFIG_DIR/skills/（与 skill-creator 一致）
    2. 平台候选路径探测（含新旧目录名 TeleAgent / teleai-super-agent）
    3. 找不到返回 None，调用方提示用户手动安装
    """
    config_dir = os.environ.get("TELEAGENT_CONFIG_DIR", "").strip()
    if config_dir:
        d = os.path.join(os.path.expanduser(config_dir), "skills")
        if os.path.isdir(d):
            return d

    home = os.path.expanduser("~")
    if os.name == "nt":
        candidates = [
            os.path.join(home, ".config", "TeleAgent", "skills"),
            os.path.join(home, ".config", "teleai-super-agent", "skills"),
            os.path.join(os.environ.get("APPDATA", ""), "TeleAgent", "skills"),
            os.path.join(os.environ.get("LOCALAPPDATA", ""), "TeleAgent", "skills"),
        ]
    else:
        candidates = [
            os.path.join(home, ".config", "TeleAgent", "skills"),
            os.path.join(home, ".local", "share", "TeleAgent", "skills"),
            os.path.join(home, ".config", "teleai-super-agent", "skills"),
        ]

    for d in candidates:
        if d and os.path.isdir(d):
            return d
    return None


def find_teleagent_process():
    """查找正在运行的 TeleAgent 进程名。"""
    names = ["TeleAgent", "teleagent", "TeleAgent.exe", "teleagent.exe"]
    try:
        import subprocess
        if os.name == "nt":
            out = subprocess.run(
                ["tasklist", "/FO", "CSV", "/NH"],
                capture_output=True, text=True, timeout=10,
                encoding="utf-8", errors="ignore",
            ).stdout
            for line in out.splitlines():
                for n in names:
                    if n.lower() in line.lower():
                        return n
        else:
            out = subprocess.run(["ps", "-A", "-o", "comm="], capture_output=True, text=True, timeout=10).stdout
            for line in out.splitlines():
                for n in names:
                    if n.lower() in line.strip().lower():
                        return n
    except Exception:
        pass
    return None


def sanitize_filename(name):
    """将任务名转为安全的文件/目录名。"""
    return re.sub(r'[<>:"/\\|?*]', '_', name).strip() or "未命名任务"


def connect_db(db_path, readonly=False):
    """连接数据库。设置 busy_timeout，避免 TeleAgent 运行时写库竞争报锁。"""
    if readonly:
        conn = sqlite3.connect(f"file:{db_path}?mode=ro", uri=True)
    else:
        conn = sqlite3.connect(db_path)
    try:
        conn.execute("PRAGMA busy_timeout=10000")
    except Exception:
        pass
    return conn


def backup_database(db_path):
    """用 SQLite backup API 做一致性备份（正确处理 WAL 未 checkpoint 数据）。"""
    bak_name = f"teleagent.db.bak_{datetime.datetime.now().strftime('%Y%m%d_%H%M%S')}"
    bak_path = os.path.join(os.path.dirname(db_path), bak_name)
    src = sqlite3.connect(db_path)
    dst = sqlite3.connect(bak_path)
    try:
        src.backup(dst)
    finally:
        dst.close()
        src.close()
    print(f"已备份数据库: {bak_path}", file=sys.stderr)
    return bak_path


# ---------------------------------------------------------------------------
# 跨平台文件路径提取
# ---------------------------------------------------------------------------
# Windows 盘符路径（允许空格/中文，排除引号换行尖括号竖线，以文件扩展名结尾）
_WIN_PATH_RE = re.compile(r'[A-Za-z]:[\\/][^"\r\n<>|]{1,300}\.[A-Za-z0-9]{1,6}')
# Unix 绝对路径（同上）
_UNIX_PATH_RE = re.compile(r'/[^"\r\n<>|]{1,300}\.[A-Za-z0-9]{1,6}')

# 尾部需剥离的常见粘连字符（中英文标点/引号/括号）
_TRAILING = "`'\"),;:，。；、：）】」』"


def extract_paths_from_text(text):
    """从文本中提取文件路径（Windows + Unix），返回清洗后的去重路径列表。

    仅匹配以文件扩展名结尾的路径，避免误匹配目录、URL 与中文正文，
    显著减少候选数量与磁盘检查次数。
    """
    paths = set()
    for pat in (_WIN_PATH_RE, _UNIX_PATH_RE):
        for p in pat.findall(text):
            clean = p.strip().rstrip(_TRAILING).strip()
            if "\\\\" in clean:
                clean = clean.replace("\\\\", "\\")
            if clean:
                paths.add(clean)
    return sorted(paths)


# ---------------------------------------------------------------------------
# 脱敏
# ---------------------------------------------------------------------------
def build_sanitize_patterns():
    """构建脱敏正则（顺序：先长后短）。仅覆盖格式化 PII：身份证/手机号/银行卡/邮箱。"""
    return [
        (re.compile(r'\d{17}[\dXx]'), '[已脱敏:身份证号]'),
        (re.compile(r'\b1[3-9]\d{9}\b'), '[已脱敏:手机号]'),
        (re.compile(r'\b\d{16,19}\b'), '[已脱敏:银行卡号]'),
        (re.compile(r'[\w.+-]+@[\w-]+\.[\w.-]+'), '[已脱敏:邮箱]'),
    ]


def sanitize_text(text, report=None):
    """脱敏。返回 (text, 总替换次数)。report 为 dict 时写入分类统计。"""
    total = 0
    for pat, repl in build_sanitize_patterns():
        text, n = pat.subn(repl, text)
        if n:
            total += n
            if report is not None:
                report[repl] = report.get(repl, 0) + n
    return text, total


def scan_pii(text):
    """扫描文本中的格式化 PII，返回 {类别: 命中数} dict。不修改原文。"""
    report = {}
    for pat, repl in build_sanitize_patterns():
        n = len(pat.findall(text))
        if n:
            report[repl] = n
    return report


# ---------------------------------------------------------------------------
# ZIP 安全解压
# ---------------------------------------------------------------------------
def safe_extract_zip(zip_path, extract_dir, max_total_bytes=2 * 1024 ** 3):
    """安全解压：校验路径穿越 + zip 炸弹大小上限。返回解压条目数。"""
    abs_extract = os.path.abspath(extract_dir)
    total = 0
    count = 0
    with zipfile.ZipFile(zip_path, "r") as zf:
        for member in zf.infolist():
            member_path = os.path.abspath(os.path.join(extract_dir, member.filename))
            try:
                if os.path.commonpath([abs_extract, member_path]) != abs_extract:
                    print(f"  警告: 跳过可疑路径: {member.filename}", file=sys.stderr)
                    continue
            except ValueError:
                print(f"  警告: 跳过非法路径: {member.filename}", file=sys.stderr)
                continue
            total += member.file_size
            if total > max_total_bytes:
                raise RuntimeError(
                    f"解压总大小超过上限 {max_total_bytes} 字节，疑似 zip 炸弹，已中止"
                )
            zf.extract(member, extract_dir)
            count += 1
    return count
