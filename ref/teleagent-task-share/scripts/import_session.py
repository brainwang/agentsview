#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""将任务导出 ZIP 中的对话记录导入回 TeleAgent 本地数据库。

用法:
  python -B import_session.py --zip "任务导出包.zip" [--db <db路径>] [--backup]
  python -B import_session.py --zip "导出包.zip" --session-titles "任务1" "任务2"
  python -B import_session.py --zip "导出包.zip" --dry-run
  python -B import_session.py --zip "导出包.zip" --no-confirm --no-refresh

说明:
  - 自动扫描 ZIP 中的任务子目录，每个目录创建一个新 session
  - 导入后自动发送 Ctrl+R 刷新客户端（可用 --no-refresh 跳过，避免抢焦点）
  - 恢复原对话使用的模型信息（从 session_meta.json 的 models 字段）
  - 目标已有同名任务时自动加 _imported 后缀
  - 多任务导入具备事务保护：任一任务失败整体回滚，不留脏数据
  - --self-test 用临时库跑一遍导入闭环，验证功能（不碰真实数据库）

仅依赖 Python 标准库（Python 3.8+）。
"""
import os
import sys
import json
import re
import time
import secrets
import string
import uuid
import shutil
import argparse
import zipfile
from datetime import datetime, timezone, timedelta

# 主动清理 __pycache__ 目录，防止 .pyc 被安全扫描拦截（第四层防护）
_script_dir = os.path.dirname(os.path.abspath(__file__))
_pycache_dir = os.path.join(_script_dir, '__pycache__')
if os.path.isdir(_pycache_dir):
    shutil.rmtree(_pycache_dir, ignore_errors=True)

# 通过 importlib 加载 common.py — 配合 sys.dont_write_bytecode 避免 .pyc 生成
sys.dont_write_bytecode = True
import importlib.util
_common_path = os.path.join(_script_dir, 'common.py')
_spec = importlib.util.spec_from_file_location('common', _common_path)
common = importlib.util.module_from_spec(_spec)
_spec.loader.exec_module(common)

# 退出时再次清理 __pycache__（双保险：脚本运行期间若仍生成则退出时清除）
import atexit
atexit.register(lambda: shutil.rmtree(
    os.path.join(os.path.dirname(os.path.abspath(__file__)), '__pycache__'),
    ignore_errors=True))

DEFAULT_VERSION = "1.2.27"  # 仅在数据库无 session 记录时兜底
DEFAULT_MODEL = "chat-pro"
DEFAULT_PROVIDER = "NewApi"
CST = timezone(timedelta(hours=8))


def _client_version_from_db(db_path):
    """从现有 session 读取客户端版本号（避免硬编码）。"""
    try:
        conn = common.connect_db(db_path, readonly=True)
        cur = conn.cursor()
        cur.execute("SELECT version FROM session WHERE version IS NOT NULL AND version != '' LIMIT 1")
        row = cur.fetchone()
        conn.close()
        return row[0] if row else DEFAULT_VERSION
    except Exception:
        return DEFAULT_VERSION


# ---------------------------------------------------------------------------
# 客户端刷新（PostMessage → SendKeys → 手动，三级降级链）
# ---------------------------------------------------------------------------
def _refresh_via_postmessage(hwnd, user32):
    VK_CONTROL, VK_R = 0x11, 0x52
    WM_KEYDOWN, WM_KEYUP = 0x0100, 0x0101
    user32.PostMessageW(hwnd, WM_KEYDOWN, VK_CONTROL, 0)
    time.sleep(0.05)
    user32.PostMessageW(hwnd, WM_KEYDOWN, VK_R, 0)
    time.sleep(0.05)
    user32.PostMessageW(hwnd, WM_KEYUP, VK_R, 0)
    user32.PostMessageW(hwnd, WM_KEYUP, VK_CONTROL, 0)


def _refresh_via_sendkeys_ps(hwnd):
    import subprocess
    ps_script = (
        'Add-Type -AssemblyName System.Windows.Forms\n'
        'Add-Type @"\n'
        'using System;\n'
        'using System.Runtime.InteropServices;\n'
        'public class WinHelper {\n'
        '    [DllImport("user32.dll")]\n'
        '    public static extern bool SetForegroundWindow(IntPtr hWnd);\n'
        '    [DllImport("user32.dll")]\n'
        '    public static extern bool ShowWindow(IntPtr hWnd, int nCmdShow);\n'
        '}\n'
        '"@\n'
        f'$hwnd = [IntPtr]::new({hwnd})\n'
        '[WinHelper]::ShowWindow($hwnd, 9) | Out-Null\n'
        'Start-Sleep -Milliseconds 300\n'
        '[WinHelper]::SetForegroundWindow($hwnd) | Out-Null\n'
        'Start-Sleep -Milliseconds 500\n'
        '[System.Windows.Forms.SendKeys]::SendWait("^r")\n'
    )
    try:
        result = subprocess.run(
            ["powershell", "-NoProfile", "-Command", ps_script],
            capture_output=True, text=True, timeout=15,
        )
        return result.returncode == 0
    except Exception:
        return False


def refresh_teleagent_client(window_title=None):
    """向 TeleAgent 客户端窗口发送 Ctrl+R，触发前端刷新。"""
    title_kw = (window_title or os.environ.get("TELEAGENT_WINDOW_TITLE", "").strip() or "TeleAgent")
    try:
        import ctypes
        from ctypes import wintypes
    except ImportError:
        print("  非 Windows 环境，请手动按 Ctrl+R 刷新客户端", file=sys.stderr)
        return False

    user32 = ctypes.windll.user32
    found_hwnd = None

    @ctypes.WINFUNCTYPE(ctypes.c_bool, wintypes.HWND, wintypes.LPARAM)
    def enum_callback(hwnd, lparam):
        nonlocal found_hwnd
        if not user32.IsWindowVisible(hwnd):
            return True
        length = user32.GetWindowTextLengthW(hwnd)
        if length <= 0:
            return True
        buf = ctypes.create_unicode_buffer(length + 1)
        user32.GetWindowTextW(hwnd, buf, length + 1)
        if title_kw.lower() in buf.value.lower():
            found_hwnd = hwnd
            return False
        return True

    user32.EnumWindows(enum_callback, 0)

    if found_hwnd is None:
        print(f"  未找到标题含 '{title_kw}' 的窗口，请手动按 Ctrl+R 刷新客户端", file=sys.stderr)
        return None

    _refresh_via_postmessage(found_hwnd, user32)
    print(f"  已通过 PostMessage 发送 Ctrl+R 到窗口: {title_kw}", file=sys.stderr)
    try:
        if _refresh_via_sendkeys_ps(found_hwnd):
            print(f"  已通过 PowerShell SendKeys 发送 Ctrl+R 到窗口: {title_kw}", file=sys.stderr)
        else:
            print("  PowerShell SendKeys 调用失败，PostMessage 可能已生效，若无反应请手动按 Ctrl+R", file=sys.stderr)
    except Exception as e:
        print(f"  PowerShell SendKeys 异常: {e}", file=sys.stderr)

    return True


# ---------------------------------------------------------------------------
# ID 生成 / 时间解析 / 对话解析
# ---------------------------------------------------------------------------
def gen_id(prefix, ts_ms, seq=0):
    ts_hex = f"{ts_ms:x}"[-11:]
    rand = "".join(secrets.choice(string.ascii_letters + string.digits) for _ in range(16))
    return f"{prefix}{ts_hex}01{rand}"


def parse_datetime_to_ms(dt_str):
    try:
        dt = datetime.strptime(dt_str.strip(), "%Y-%m-%d %H:%M:%S")
        dt = dt.replace(tzinfo=CST)
        return int(dt.timestamp() * 1000)
    except Exception:
        return int(datetime.now(timezone.utc).timestamp() * 1000)


def parse_md_conversation(md_content):
    """解析 完整对话与思考过程.md，返回消息序列。"""
    lines = md_content.split("\n")
    start_idx = 0
    for i, line in enumerate(lines):
        if "## 完整对话记录" in line:
            start_idx = i + 1
            break

    blocks = []
    cur = None
    in_details = False
    details_buf = []

    def flush_reasoning():
        nonlocal details_buf
        if cur is not None and details_buf:
            raw = "\n".join(details_buf).strip()
            raw = re.sub(r"<summary>.*?</summary>\s*", "", raw, count=1)
            cur["reasoning"] = raw.strip()
            details_buf = []

    for line in lines[start_idx:]:
        m = re.match(r"^###\s*【(用户|智能体)】\s*(.+)$", line)
        if m:
            flush_reasoning()
            role_label, tstr = m.group(1), m.group(2).strip()
            cur = {
                "role": "user" if role_label == "用户" else "assistant",
                "time_str": tstr,
                "ts_ms": parse_datetime_to_ms(tstr),
                "body": [],
                "reasoning": None,
            }
            blocks.append(cur)
            continue
        if "<details>" in line:
            in_details = True
            continue
        if "</details>" in line:
            in_details = False
            flush_reasoning()
            continue
        if "<summary>" in line:
            continue
        if "---" == line.strip():
            continue
        if "[DEGRADED MODE]" in line or "Install pandoc" in line:
            continue
        if in_details:
            details_buf.append(line)
            continue
        if cur is not None:
            cur["body"].append(line)

    flush_reasoning()
    for b in blocks:
        b["body"] = "\n".join(b["body"]).strip()
    return blocks


def build_message_data(role, ts_ms, parent_id=None, model_id=DEFAULT_MODEL, provider_id=DEFAULT_PROVIDER):
    """构建 message.data JSON。model_id/provider_id 从导出元数据恢复。"""
    if role == "user":
        return {
            "role": "user",
            "agent": "opencowork-default",
            "model": {"providerID": provider_id, "modelID": model_id},
            "time": {"created": ts_ms},
            "queryID": f"q_{uuid.uuid4()}",
        }
    else:
        return {
            "role": "assistant",
            "parentID": parent_id or "",
            "modelID": model_id,
            "providerID": provider_id,
            "agent": "opencowork-default",
            "mode": "",
            "path": {"cwd": "", "root": ""},
            "time": {"created": ts_ms, "completed": ts_ms + 1000},
            "cost": 0,
            "tokens": {"total": 0, "input": 0, "output": 0, "reasoning": 0, "cache": {"read": 0, "write": 0}},
            "finish": "stop",
        }


def build_parts(body, reasoning):
    parts = []
    if reasoning:
        parts.append({"type": "step-start"})
        parts.append({"type": "reasoning", "text": reasoning})
    parts.append({"type": "text", "text": body})
    if reasoning:
        parts.append({"type": "step-finish"})
    return parts


def create_session(cur, title, directory, version=DEFAULT_VERSION):
    """创建新 session 记录，返回 session_id。"""
    now_ts = int(datetime.now(timezone.utc).timestamp() * 1000)
    ts_hex = f"{now_ts:x}"[-11:]
    rand = "".join(secrets.choice(string.ascii_letters + string.digits) for _ in range(16))
    session_id = f"ses_{ts_hex}01{rand}"
    slug = f"import-{ts_hex}"

    cur.execute(
        "INSERT INTO session (id, project_id, parent_id, slug, directory, title, version, "
        "share_url, summary_additions, summary_deletions, summary_files, summary_diffs, "
        "revert, permission, time_created, time_updated, time_compacting, time_archived, workspace_id) "
        "VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)",
        (session_id, "global", None, slug, directory, title, version, None, 0, 0, 0, None, None, None, now_ts, now_ts, None, None, None),
    )
    return session_id


def _unique_title(cur, title):
    """目标已有同名任务时，自动加后缀，返回唯一标题。"""
    cur.execute("SELECT COUNT(*) FROM session WHERE title=?", (title,))
    if cur.fetchone()[0] == 0:
        return title
    for i in range(2, 1000):
        cand = f"{title}_imported{i}"
        cur.execute("SELECT COUNT(*) FROM session WHERE title=?", (cand,))
        if cur.fetchone()[0] == 0:
            return cand
    return f"{title}_{int(time.time())}"


# ---------------------------------------------------------------------------
# 技能包审查与安装
# ---------------------------------------------------------------------------
# 脚本安全扫描：检测高风险模式（仅警告，不阻断）
_SCRIPT_DANGER_PATTERNS = [
    (re.compile(r'\bsubprocess\.(call|run|Popen|check_output)\s*\('), 'subprocess 调用'),
    (re.compile(r'\bos\.system\s*\('), 'os.system 调用'),
    (re.compile(r'\beval\s*\('), 'eval 执行'),
    (re.compile(r'\bexec\s*\('), 'exec 执行'),
    (re.compile(r'\b__import__\s*\('), '动态 import'),
    (re.compile(r'\bos\.popen\s*\('), 'os.popen 调用'),
    (re.compile(r'socket\.connect|urllib\.request|requests\.(get|post|put|delete)'), '网络请求'),
    (re.compile(r'\bopen\s*\([^)]*["\']w'), '文件写入'),
    (re.compile(r'shutil\.rmtree|os\.remove|os\.unlink'), '文件删除'),
]


def _audit_skill_package(src_dir, skill_name):
    """审查单个技能包目录的内容安全性与结构合规性。

    返回 (passed, warnings, file_tree)：
    - passed: True 表示通过审查（可能有警告但无致命问题），False 表示有致命问题
    - warnings: [str] 警告/问题列表
    - file_tree: str 技能目录文件树（用于展示给用户）
    """
    warnings = []
    file_list = []

    # 1. 检查 SKILL.md 存在
    skill_md_path = os.path.join(src_dir, "SKILL.md")
    if not os.path.isfile(skill_md_path):
        return False, [f"致命: 缺少 SKILL.md，不是有效的技能包"], f"{skill_name}/ (无 SKILL.md)"

    # 2. 解析 SKILL.md frontmatter，检查必需字段
    try:
        with open(skill_md_path, "r", encoding="utf-8") as f:
            md_content = f.read()
        # 提取 frontmatter
        if md_content.startswith("---"):
            fm_end = md_content.find("---", 3)
            if fm_end > 0:
                fm_text = md_content[3:fm_end]
                required_fields = ["name", "description"]
                for field in required_fields:
                    if f"{field}:" not in fm_text:
                        warnings.append(f"SKILL.md frontmatter 缺少建议字段: {field}")
    except Exception as e:
        warnings.append(f"SKILL.md 读取异常: {e}")

    # 3. 遍历文件，检查脚本安全性
    for root, dirs, files in os.walk(src_dir):
        # 排除 __pycache__ 等
        dirs[:] = [d for d in dirs if d not in ("__pycache__", ".git")]
        for fn in sorted(files):
            full = os.path.join(root, fn)
            rel = os.path.relpath(full, src_dir)
            file_list.append(rel)
            # 脚本安全扫描
            if fn.endswith((".py", ".js", ".sh", ".ps1")):
                try:
                    with open(full, "r", encoding="utf-8", errors="replace") as f:
                        content = f.read()
                    for pattern, label in _SCRIPT_DANGER_PATTERNS:
                        matches = pattern.findall(content)
                        if matches:
                            warnings.append(f"  {rel}: 检测到 {label} ({len(matches)} 处)")
                except Exception:
                    pass

    # 4. 生成文件树
    tree_lines = [f"{skill_name}/"]
    for rel in file_list:
        depth = rel.count(os.sep)
        indent = "  " * (depth + 1)
        tree_lines.append(f"{indent}{os.path.basename(rel)}")
    file_tree = "\n".join(tree_lines)

    # 5. 检查是否有可疑文件（非技能常见格式）
    known_exts = {".md", ".py", ".js", ".sh", ".ps1", ".json", ".yaml", ".yml",
                  ".txt", ".html", ".css", ".svg", ".png", ".jpg", ".toml", ".ini",
                  ".cfg", ".xml", ".sql", ".gitignore", ".license"}
    for rel in file_list:
        ext = os.path.splitext(rel)[1].lower()
        if ext and ext not in known_exts and not rel.startswith("."):
            warnings.append(f"  {rel}: 非常见技能文件格式 ({ext})")

    passed = True  # 有警告但仍可通过，由用户决定是否安装
    return passed, warnings, file_tree


def audit_skill_packages(task_dir_path, meta):
    """审查 ZIP 中所有技能包，返回审查报告列表。

    返回 [{"skill_name", "passed", "warnings", "file_tree", "src_dir"}, ...]
    """
    skill_packages = meta.get("skill_packages", []) if isinstance(meta, dict) else []
    if not skill_packages:
        return []

    reports = []
    for pkg in skill_packages:
        skill_name = pkg.get("skill_name", "")
        if not skill_name:
            continue
        src_dir = os.path.join(task_dir_path, "__skill_packages__", skill_name)
        if not os.path.isdir(src_dir):
            reports.append({
                "skill_name": skill_name,
                "passed": False,
                "warnings": [f"致命: 技能包目录不存在: {src_dir}"],
                "file_tree": "(目录缺失)",
                "src_dir": src_dir,
            })
            continue
        passed, warnings, file_tree = _audit_skill_package(src_dir, skill_name)
        reports.append({
            "skill_name": skill_name,
            "passed": passed,
            "warnings": warnings,
            "file_tree": file_tree,
            "src_dir": src_dir,
        })
    return reports


def install_skill_packages(task_dir_path, meta, approved_skills=None):
    """检测 ZIP 中的技能包目录，安装到目标用户技能目录。

    approved_skills: 用户确认安装的技能名集合；为 None 时安装全部通过审查的技能。
    返回安装的技能数量。
    """
    skill_packages = meta.get("skill_packages", []) if isinstance(meta, dict) else []
    if not skill_packages:
        return 0

    skills_dir = common.resolve_skill_storage_dir()
    if not skills_dir:
        print("  警告: 未找到技能安装目录，技能包未安装。", file=sys.stderr)
        print("  提示: 设置环境变量 TELEAGENT_CONFIG_DIR 或确保 ~/.config/TeleAgent/skills/ 存在", file=sys.stderr)
        return 0

    installed = 0
    for pkg in skill_packages:
        skill_name = pkg.get("skill_name", "")
        if not skill_name:
            continue
        # 仅安装用户确认的技能
        if approved_skills is not None and skill_name not in approved_skills:
            continue
        src_dir = os.path.join(task_dir_path, "__skill_packages__", skill_name)
        if not os.path.isdir(src_dir):
            print(f"  警告: 技能包目录不存在: {src_dir}", file=sys.stderr)
            continue
        dest_dir = os.path.join(skills_dir, skill_name)
        # 目标已有同名技能时加 _imported 后缀
        if os.path.exists(dest_dir):
            dest_dir = os.path.join(skills_dir, f"{skill_name}_imported")
            print(f"  技能 {skill_name} 已存在，安装为 {skill_name}_imported", file=sys.stderr)
        try:
            shutil.copytree(src_dir, dest_dir, ignore=common._SKILL_IGNORE)
            installed += 1
            print(f"  已安装技能包: {skill_name} → {dest_dir}", file=sys.stderr)
        except Exception as e:
            print(f"  警告: 安装技能包失败 {skill_name}: {e}", file=sys.stderr)

    if installed:
        print(f"  共安装 {installed} 个技能包到: {skills_dir}", file=sys.stderr)
    return installed


# ---------------------------------------------------------------------------
# 单任务导入
# ---------------------------------------------------------------------------
def import_task_to_db(conn, task_dir_path, task_name, work_dir=None, version=DEFAULT_VERSION):
    """将单个任务从解压目录导入数据库（不 commit，由调用方统一事务提交）。"""
    cur = conn.cursor()

    meta_path = os.path.join(task_dir_path, "session_meta.json")
    original_title = task_name
    original_dir = ""
    meta = {}
    if os.path.exists(meta_path):
        with open(meta_path, "r", encoding="utf-8") as f:
            meta = json.load(f)
        original_title = meta.get("title", task_name)
        original_dir = meta.get("directory", "")

    # 恢复模型信息
    meta_models = meta.get("models", []) if isinstance(meta, dict) else []
    meta_providers = meta.get("providers", []) if isinstance(meta, dict) else []
    model_id = meta_models[0] if meta_models else DEFAULT_MODEL
    provider_id = meta_providers[0] if meta_providers else DEFAULT_PROVIDER

    # 读取对话记录
    md_path = None
    target_md = "完整对话与思考过程.md"
    for fname in os.listdir(task_dir_path):
        if fname == target_md:
            md_path = os.path.join(task_dir_path, fname)
            break
    if not md_path:
        for fname in os.listdir(task_dir_path):
            if fname.endswith(".md") and "README" not in fname.upper() and "导出" not in fname:
                md_path = os.path.join(task_dir_path, fname)
                break

    if not md_path:
        print(f"  警告: {task_dir_path} 中未找到对话记录 Markdown", file=sys.stderr)
        return 0, None

    with open(md_path, "r", encoding="utf-8") as f:
        md_content = f.read()

    # 技能包安装已移至 main() 流程中单独确认后执行

    # 复制产出文件到目标用户工作目录
    file_list_path = os.path.join(task_dir_path, "产出文件清单.txt")
    produced_files = []
    if os.path.exists(file_list_path):
        with open(file_list_path, "r", encoding="utf-8") as f:
            produced_files = [line.strip() for line in f if line.strip()]
    _meta_files = {"session_meta.json", "完整对话与思考过程.md", "产出文件清单.txt", "工具调用记录.txt"}
    if not produced_files:
        for fname in os.listdir(task_dir_path):
            fpath = os.path.join(task_dir_path, fname)
            if os.path.isfile(fpath) and fname not in _meta_files:
                produced_files.append(fname)
    copied_count = 0
    for fname in produced_files:
        src = os.path.join(task_dir_path, fname)
        if not os.path.isfile(src):
            continue
        dest = os.path.join(work_dir, fname)
        if os.path.exists(dest):
            name, ext = os.path.splitext(fname)
            dest = os.path.join(work_dir, f"{name}_imported{ext}")
        try:
            shutil.copy2(src, dest)
            copied_count += 1
        except Exception as e:
            print(f"  警告: 复制产出文件失败 {fname}: {e}", file=sys.stderr)
    if copied_count:
        print(f"  已复制 {copied_count} 个产出文件到工作目录: {work_dir}", file=sys.stderr)

    # 替换对话中的文件路径
    if original_dir and work_dir and original_dir != work_dir:
        md_content = md_content.replace(original_dir, work_dir)
        original_dir_fwd = original_dir.replace("\\", "/")
        work_dir_fwd = work_dir.replace("\\", "/")
        if original_dir_fwd != original_dir:
            md_content = md_content.replace(original_dir_fwd, work_dir_fwd)
        print(f"  已将工作目录从 {original_dir} 替换为 {work_dir}", file=sys.stderr)

    produced_file_map = meta.get("produced_files", []) if isinstance(meta, dict) else []
    replaced_full, replaced_dir = 0, 0

    for pf in produced_file_map:
        orig_path = pf.get("original_path", "")
        orig_name = pf.get("filename", "")
        if not orig_path or not orig_name:
            continue
        if "\\\\" in orig_path:
            orig_path = orig_path.replace("\\\\", "\\")
        new_path = os.path.join(work_dir, orig_name)
        if orig_path != new_path and orig_path in md_content:
            md_content = md_content.replace(orig_path, new_path)
            replaced_full += 1

    dir_prefixes = set()
    for pf in produced_file_map:
        orig_path = pf.get("original_path", "")
        if "\\\\" in orig_path:
            orig_path = orig_path.replace("\\\\", "\\")
        parts = orig_path.replace("/", "\\").split("\\")
        for i in range(1, len(parts)):
            prefix = "\\".join(parts[:i + 1])
            if prefix and not prefix.startswith(work_dir):
                dir_prefixes.add(prefix)

    for prefix in sorted(dir_prefixes, key=len, reverse=True):
        if prefix in md_content and prefix != work_dir:
            md_content = md_content.replace(prefix, work_dir)
            replaced_dir += 1
            prefix_fwd = prefix.replace("\\", "/")
            if prefix_fwd != prefix and prefix_fwd in md_content:
                md_content = md_content.replace(prefix_fwd, work_dir.replace("\\", "/"))

    if replaced_full or replaced_dir:
        print(f"  已替换 {replaced_full} 个完整路径 + {replaced_dir} 个目录前缀到工作目录", file=sys.stderr)

    blocks = parse_md_conversation(md_content)
    if not blocks:
        print(f"  警告: {task_name} 未解析到任何消息", file=sys.stderr)
        return 0, None

    session_id = create_session(cur, task_name, work_dir, version)
    print(f"  创建新 session: {task_name} ({session_id})", file=sys.stderr)

    last_user_msg_id = None
    imported = 0
    for i, block in enumerate(blocks):
        ts = block["ts_ms"]
        role = block["role"]
        body = block["body"]
        reasoning = block["reasoning"]
        if not body:
            continue

        msg_id = gen_id("msg_", ts, i)
        msg_data = build_message_data(role, ts, last_user_msg_id if role == "assistant" else None,
                                      model_id=model_id, provider_id=provider_id)
        if role == "user":
            last_user_msg_id = msg_id

        parts = build_parts(body, reasoning)
        cur.execute(
            "INSERT INTO message (id, session_id, time_created, time_updated, data) VALUES (?,?,?,?,?)",
            (msg_id, session_id, ts, ts, json.dumps(msg_data, ensure_ascii=False)),
        )
        for k, pdata in enumerate(parts):
            pid = gen_id("prt_", ts, k)
            cur.execute(
                "INSERT INTO part (id, message_id, session_id, time_created, time_updated, data) VALUES (?,?,?,?,?,?)",
                (pid, msg_id, session_id, ts + k, ts + k, json.dumps(pdata, ensure_ascii=False)),
            )
        imported += 1

    cur.execute("SELECT COUNT(*) FROM message WHERE session_id=?", (session_id,))
    msg_count = cur.fetchone()[0]
    cur.execute("SELECT COUNT(*) FROM part WHERE session_id=?", (session_id,))
    part_count = cur.fetchone()[0]
    print(f"  导入完成: {imported} 条消息, {part_count} 个 part (DB验证: msg={msg_count}, part={part_count})", file=sys.stderr)

    return imported, session_id


def scan_zip_tasks(zip_path, extract_dir):
    """安全解压 ZIP 并扫描任务子目录，返回 [(task_dir_path, original_title), ...]。"""
    common.safe_extract_zip(zip_path, extract_dir)

    tasks = []
    for root, dirs, files in os.walk(extract_dir):
        if "session_meta.json" in files:
            meta_path = os.path.join(root, "session_meta.json")
            try:
                with open(meta_path, "r", encoding="utf-8") as f:
                    meta = json.load(f)
                original_title = meta.get("title", os.path.basename(root))
            except Exception:
                original_title = os.path.basename(root)
            tasks.append((root, original_title))

    if not tasks:
        for root, dirs, files in os.walk(extract_dir):
            for fname in files:
                if fname.endswith(".md") and "README" not in fname.upper():
                    tasks.append((root, os.path.basename(root)))
                    break
    return tasks


# ---------------------------------------------------------------------------
# 自检（用临时库 + 临时 ZIP 跑导入闭环）
# ---------------------------------------------------------------------------
def self_test():
    import tempfile
    import sqlite3
    tmp = tempfile.mkdtemp(prefix="ts_import_selftest_")
    try:
        db = os.path.join(tmp, "test.db")
        conn = sqlite3.connect(db)
        cur = conn.cursor()
        cur.execute("""CREATE TABLE session (id TEXT PRIMARY KEY, project_id TEXT, parent_id TEXT, slug TEXT,
            directory TEXT, title TEXT, version TEXT, share_url TEXT, summary_additions INTEGER,
            summary_deletions INTEGER, summary_files INTEGER, summary_diffs TEXT, revert TEXT, permission TEXT,
            time_created INTEGER, time_updated INTEGER, time_compacting INTEGER, time_archived INTEGER, workspace_id TEXT)""")
        cur.execute("""CREATE TABLE message (id TEXT PRIMARY KEY, session_id TEXT, time_created INTEGER, time_updated INTEGER, data TEXT)""")
        cur.execute("""CREATE TABLE part (id TEXT PRIMARY KEY, message_id TEXT, session_id TEXT, time_created INTEGER, time_updated INTEGER, data TEXT)""")
        conn.commit()
        conn.close()

        # 构造 ZIP：一个任务子目录，含 session_meta.json + md + 产出文件 + 技能包
        task_dir = os.path.join(tmp, "zipsrc", "测试任务")
        os.makedirs(task_dir, exist_ok=True)
        # 构造技能包目录（含安全脚本和含风险脚本各一个）
        skill_pkg_dir = os.path.join(task_dir, "__skill_packages__", "test-skill")
        os.makedirs(os.path.join(skill_pkg_dir, "scripts"), exist_ok=True)
        with open(os.path.join(skill_pkg_dir, "SKILL.md"), "w", encoding="utf-8") as f:
            f.write("---\nname: test-skill\ndescription: test\n---\n# test skill")
        with open(os.path.join(skill_pkg_dir, "scripts", "run.py"), "w", encoding="utf-8") as f:
            f.write("print('hello')")
        with open(os.path.join(skill_pkg_dir, "scripts", "danger.py"), "w", encoding="utf-8") as f:
            f.write("import subprocess\nsubprocess.call(['rm', '-rf', '/'])")
        meta = {"title": "测试任务", "directory": tmp, "models": ["chat-lite"], "providers": ["NewApi"],
                "produced_files": [{"original_path": os.path.join(tmp, "a.txt"), "filename": "a.txt"}],
                "skill_packages": [{"skill_name": "test-skill", "skill_dir": "/fake/skills/test-skill",
                                     "files": ["SKILL.md", "scripts/run.py"]}]}
        with open(os.path.join(task_dir, "session_meta.json"), "w", encoding="utf-8") as f:
            json.dump(meta, f, ensure_ascii=False)
        with open(os.path.join(task_dir, "完整对话与思考过程.md"), "w", encoding="utf-8") as f:
            f.write("# 任务导出\n\n## 完整对话记录\n\n### 【用户】 2026-08-30 10:00:00\n你好\n\n### 【智能体】 2026-08-30 10:00:01\n你好，有什么可以帮你\n")
        with open(os.path.join(task_dir, "产出文件清单.txt"), "w", encoding="utf-8") as f:
            f.write("a.txt\n")
        with open(os.path.join(task_dir, "a.txt"), "w", encoding="utf-8") as f:
            f.write("hello")
        zip_path = os.path.join(tmp, "in.zip")
        with zipfile.ZipFile(zip_path, "w", zipfile.ZIP_DEFLATED) as zf:
            for root, dirs, files in os.walk(os.path.join(tmp, "zipsrc")):
                for file in files:
                    fp = os.path.join(root, file)
                    zf.write(fp, os.path.relpath(fp, os.path.join(tmp, "zipsrc")))

        # 导入到临时库
        extract_dir = os.path.join(tmp, "extract")
        os.makedirs(extract_dir, exist_ok=True)
        tasks = scan_zip_tasks(zip_path, extract_dir)
        assert len(tasks) == 1, f"任务扫描数错误: {len(tasks)}"

        conn = common.connect_db(db)
        total = 0
        for task_dir_path, orig_title in tasks:
            cnt, sid = import_task_to_db(conn, task_dir_path, "测试任务", tmp, DEFAULT_VERSION)
            total += cnt
        conn.commit()

        cur = conn.cursor()
        cur.execute("SELECT COUNT(*) FROM session")
        assert cur.fetchone()[0] == 1, "session 未创建"
        cur.execute("SELECT COUNT(*) FROM message")
        assert cur.fetchone()[0] == 2, "message 数错误"
        # 验证模型恢复
        cur.execute("SELECT data FROM message WHERE session_id=?", (sid,))
        found_model = any("chat-lite" in r[0] for r in cur.fetchall())
        assert found_model, "模型信息未恢复"
        # 验证产出文件复制
        assert os.path.isfile(os.path.join(tmp, "a.txt")), "产出文件未复制"

        # 验证技能包审查
        task_meta = {}
        for task_dir_path, _ in tasks:
            mp = os.path.join(task_dir_path, "session_meta.json")
            if os.path.exists(mp):
                with open(mp, "r", encoding="utf-8") as f:
                    task_meta = json.load(f)
                break
        reports = audit_skill_packages(tasks[0][0], task_meta)
        assert len(reports) == 1, f"审查报告数错误: {len(reports)}"
        rpt = reports[0]
        assert rpt["skill_name"] == "test-skill", "技能名错误"
        assert rpt["passed"], "技能包审查应通过（有警告但无致命问题）"
        assert any("subprocess" in w for w in rpt["warnings"]), "应检测到 subprocess 风险"
        assert "SKILL.md" in rpt["file_tree"], "文件树应包含 SKILL.md"

        # 模拟用户确认安装
        skills_dir = common.resolve_skill_storage_dir()
        if skills_dir:
            install_skill_packages(tasks[0][0], task_meta, approved_skills={"test-skill"})
            installed_skill = os.path.join(skills_dir, "test-skill")
            assert os.path.isdir(installed_skill), "技能包目录未安装"
            assert os.path.isfile(os.path.join(installed_skill, "SKILL.md")), "技能 SKILL.md 未安装"
            # 清理测试安装的技能
            shutil.rmtree(installed_skill, ignore_errors=True)
        conn.close()
        print("[SELF-TEST] 导入闭环通过：session/message/模型/产出文件/技能包审查+安装均正确", file=sys.stderr)
        return True
    except Exception as e:
        print(f"[SELF-TEST] 失败: {e}", file=sys.stderr)
        import traceback
        traceback.print_exc()
        return False
    finally:
        shutil.rmtree(tmp, ignore_errors=True)


def main():
    sys.stdout.reconfigure(encoding="utf-8")
    ap = argparse.ArgumentParser(description="导入任务导出 ZIP 到 TeleAgent 数据库")
    ap.add_argument("--zip", help="任务导出 ZIP 文件路径")
    ap.add_argument("--db", default=None, help="teleagent.db 路径（默认自动探测）")
    ap.add_argument("--session-titles", nargs="*", default=None, help="自定义各任务新名称")
    ap.add_argument("--backup", action="store_true", default=True, help="导入前自动备份（默认开启）")
    ap.add_argument("--no-backup", action="store_true", help="跳过数据库备份")
    ap.add_argument("--work-dir", default=None, help="工作目录（默认自动探测）")
    ap.add_argument("--window-title", default=None, help="TeleAgent 窗口标题关键词")
    ap.add_argument("--dry-run", action="store_true", help="仅预览不写入")
    ap.add_argument("--no-confirm", action="store_true", default=False, help="跳过导入前确认")
    ap.add_argument("--no-refresh", action="store_true", help="导入后不自动刷新客户端（避免抢焦点）")
    ap.add_argument("--self-test", action="store_true", help="用临时库跑自检")
    args = ap.parse_args()

    if args.self_test:
        sys.exit(0 if self_test() else 1)

    if not args.zip:
        ap.error("--zip 是必需参数（或使用 --self-test）")

    if args.no_backup:
        args.backup = False

    try:
        db_path = common.resolve_db_path(args.db)
        work_dir = common.resolve_work_dir(args.work_dir, db_path)
    except FileNotFoundError as e:
        print(f"错误: {e}", file=sys.stderr)
        sys.exit(1)

    version = _client_version_from_db(db_path)
    print(f"数据库: {db_path}", file=sys.stderr)
    print(f"工作目录: {work_dir}", file=sys.stderr)
    print(f"客户端版本: {version}", file=sys.stderr)

    if args.backup and not args.dry_run:
        common.backup_database(db_path)

    extract_dir = os.path.join(work_dir, ".temp", f"import_{datetime.now().strftime('%Y%m%d_%H%M%S')}")
    os.makedirs(extract_dir, exist_ok=True)

    if not os.path.exists(args.zip):
        print(f"错误: ZIP 文件不存在: {args.zip}", file=sys.stderr)
        shutil.rmtree(extract_dir, ignore_errors=True)
        sys.exit(1)

    print(f"解压 ZIP: {args.zip}", file=sys.stderr)
    try:
        tasks = scan_zip_tasks(args.zip, extract_dir)
    except RuntimeError as e:
        print(f"错误: {e}", file=sys.stderr)
        shutil.rmtree(extract_dir, ignore_errors=True)
        sys.exit(1)
    print(f"扫描到 {len(tasks)} 个任务", file=sys.stderr)

    if not tasks:
        print("错误: ZIP 中未找到任何任务数据", file=sys.stderr)
        shutil.rmtree(extract_dir, ignore_errors=True)
        sys.exit(1)

    if args.dry_run:
        print("=== DRY RUN (预览模式) ===", file=sys.stderr)
        for i, (task_dir, orig_title) in enumerate(tasks):
            new_title = args.session_titles[i] if args.session_titles and i < len(args.session_titles) else orig_title
            print(f"  [{i+1}] {orig_title} → 新任务名: {new_title} (目录: {task_dir})", file=sys.stderr)
        shutil.rmtree(extract_dir, ignore_errors=True)
        return

    if not args.no_confirm:
        print(f"\n即将导入以下 {len(tasks)} 个任务:", file=sys.stderr)
        for i, (task_dir, orig_title) in enumerate(tasks):
            new_title = args.session_titles[i] if args.session_titles and i < len(args.session_titles) else orig_title
            print(f"  [{i+1}] {orig_title} → {new_title}", file=sys.stderr)
        print("\n是否确认导入？(y/n)", file=sys.stderr)
        try:
            choice = input().strip().lower()
        except EOFError:
            print("非交互式环境无法读取输入，已自动取消。请添加 --no-confirm。", file=sys.stderr)
            shutil.rmtree(extract_dir, ignore_errors=True)
            return
        if choice != 'y':
            print("已取消导入。", file=sys.stderr)
            shutil.rmtree(extract_dir, ignore_errors=True)
            return

    # ---- 技能包审查与单独确认（与任务导入确认分离）----
    approved_skills = None  # None 表示无技能包或 --no-confirm 模式下自动跳过
    all_skill_reports = []
    for task_dir, orig_title in tasks:
        meta_path = os.path.join(task_dir, "session_meta.json")
        meta = {}
        if os.path.exists(meta_path):
            try:
                with open(meta_path, "r", encoding="utf-8") as f:
                    meta = json.load(f)
            except Exception:
                pass
        if meta.get("skill_packages"):
            reports = audit_skill_packages(task_dir, meta)
            all_skill_reports.extend(reports)

    if all_skill_reports:
        print(f"\n{'='*60}", file=sys.stderr)
        print(f"检测到 {len(all_skill_reports)} 个技能包，开始安全审查", file=sys.stderr)
        print(f"{'='*60}", file=sys.stderr)

        approved_skills = set()
        for rpt in all_skill_reports:
            sn = rpt["skill_name"]
            print(f"\n--- 技能包: {sn} ---", file=sys.stderr)
            print(f"文件结构:", file=sys.stderr)
            print(rpt["file_tree"], file=sys.stderr)

            if rpt["warnings"]:
                print(f"\n审查警告:", file=sys.stderr)
                for w in rpt["warnings"]:
                    print(f"  {w}", file=sys.stderr)
            else:
                print(f"\n审查结果: 未发现风险项", file=sys.stderr)

            if not rpt["passed"]:
                print(f"\n  该技能包存在致命问题，已自动跳过。", file=sys.stderr)
                continue

            if args.no_confirm:
                print(f"  (--no-confirm 模式，自动跳过技能安装)", file=sys.stderr)
                continue

            print(f"\n  是否安装技能 [{sn}]？(y/n/s 跳过/查看完整警告)", file=sys.stderr)
            try:
                choice = input().strip().lower()
            except EOFError:
                print("  非交互式环境，自动跳过技能安装。使用 --no-confirm 可跳过此提示。", file=sys.stderr)
                choice = 'n'
            if choice == 'y':
                approved_skills.add(sn)
            elif choice == 's':
                pass  # 跳过
            # 其他输入也跳过

        if approved_skills:
            print(f"\n开始安装 {len(approved_skills)} 个已确认的技能包...", file=sys.stderr)
            for task_dir, orig_title in tasks:
                meta_path = os.path.join(task_dir, "session_meta.json")
                meta = {}
                if os.path.exists(meta_path):
                    try:
                        with open(meta_path, "r", encoding="utf-8") as f:
                            meta = json.load(f)
                    except Exception:
                        pass
                if meta.get("skill_packages"):
                    install_skill_packages(task_dir, meta, approved_skills=approved_skills)
        else:
            print("\n未选择安装任何技能包。", file=sys.stderr)
        print(f"{'='*60}\n", file=sys.stderr)

    conn = common.connect_db(db_path)
    total_imported = 0
    session_ids = []

    # 事务保护：任一任务失败整体回滚，不留脏数据
    try:
        for i, (task_dir, orig_title) in enumerate(tasks):
            new_title = args.session_titles[i] if args.session_titles and i < len(args.session_titles) else orig_title
            unique_title = _unique_title(conn.cursor(), new_title)
            print(f"\n[{i+1}/{len(tasks)}] 导入: {orig_title} → {unique_title}", file=sys.stderr)
            count, sid = import_task_to_db(conn, task_dir, unique_title, work_dir, version)
            total_imported += count
            if sid:
                session_ids.append((unique_title, sid))
        conn.commit()
    except Exception as e:
        conn.rollback()
        print(f"\n导入失败，已回滚所有数据库改动: {e}", file=sys.stderr)
        shutil.rmtree(extract_dir, ignore_errors=True)
        sys.exit(1)
    finally:
        conn.close()

    shutil.rmtree(extract_dir, ignore_errors=True)

    print(f"\n=== 导入完成 ===", file=sys.stderr)
    print(f"总任务数: {len(session_ids)}", file=sys.stderr)
    print(f"总消息数: {total_imported}", file=sys.stderr)
    for title, sid in session_ids:
        print(f"  - {title} ({sid})", file=sys.stderr)

    if not args.no_refresh:
        time.sleep(0.5)
        refreshed = refresh_teleagent_client(args.window_title)
        if refreshed:
            print("\n客户端已自动刷新（PostMessage + SendKeys），任务栏即可看到导入的任务。", file=sys.stderr)
            print("若仍未显示，请手动按 Ctrl+R 刷新。", file=sys.stderr)
        else:
            print("\n请手动按 Ctrl+R 刷新 TeleAgent 客户端。", file=sys.stderr)
    else:
        print("\n已跳过自动刷新，请手动按 Ctrl+R 刷新以显示导入任务。", file=sys.stderr)


if __name__ == "__main__":
    main()
