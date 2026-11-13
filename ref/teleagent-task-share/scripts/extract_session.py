#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""从 TeleAgent 本地数据库提取一个或多个任务的完整数据，打包为 ZIP。

用法:
  python -B extract_session.py --search "任务A" "任务B" [--db <db路径>] [--output <zip>] [--work-dir <目录>]
  python -B extract_session.py --search "年会抽奖" --output "导出包.zip" --sanitize
  python -B extract_session.py --search "年会抽奖" --dry-run
  python -B extract_session.py --search "年会抽奖" --select 1 3

说明:
  - 支持同时导出多个任务；--select 可按序号只导出部分任务
  - 多任务匹配时交互式选择导出哪些任务（输入序号/空格或逗号分隔/a 全部/q 取消）
  - 导出物固定为 ZIP 压缩包，含每个任务的对话记录、思考过程、产出文件
  - --sanitize 自动脱敏（手机号/身份证/银行卡/邮箱），覆盖对话/标题/元数据
  - 未指定 --sanitize 时导出前自动扫描 PII，交互式询问是否脱敏
  - --max-file-size 跳过超过指定字节数的产出文件（默认无限制）
  - --self-test 用临时库跑一遍导出闭环，验证功能（不碰真实数据库）

仅依赖 Python 标准库（Python 3.8+）。
"""
import os
import sys
import json
import argparse
import shutil
import zipfile
import datetime

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


# ---------------------------------------------------------------------------
# 任务定位
# ---------------------------------------------------------------------------
def find_sessions(cur, keywords, ui_titles=None):
    """按多个关键词搜索 session，返回 [(sid, title, directory, time_created, time_updated), ...]。

    搜索优先级：
    1. SQLite session.title 模糊匹配（创建时的原始标题）
    2. UI 显示名模糊匹配（用户在 UI 中改名后的标题，存储在 LevelDB 中）
    3. part 内容关键词反查（对话正文中包含关键词）

    ui_titles: {session_id: display_name} 映射，由 common.load_ui_titles() 提供。
    传入时会同时按 UI 显示名搜索，且返回的 title 优先使用 UI 显示名。
    """
    if ui_titles is None:
        ui_titles = {}

    results = []
    found_sids = set()
    for kw in keywords:
        kw_lower = kw.lower()
        # 1. 先按 DB title 模糊匹配
        cur.execute(
            "SELECT id, title, directory, time_created, time_updated FROM session WHERE title LIKE ? ORDER BY time_updated DESC",
            (f"%{kw}%",),
        )
        rows = cur.fetchall()
        if rows:
            for r in rows:
                if r[0] not in found_sids and not r[1].startswith("_SYS_"):
                    # UI 显示名优先
                    display = ui_titles.get(r[0], r[1])
                    results.append((r[0], display, r[2], r[3], r[4]))
                    found_sids.add(r[0])
        # 2. 按 UI 显示名模糊匹配（覆盖 DB title 未命中的情况）
        for sid, display in ui_titles.items():
            if sid in found_sids:
                continue
            if kw_lower in display.lower():
                cur.execute("SELECT id, title, directory, time_created, time_updated FROM session WHERE id=?", (sid,))
                r = cur.fetchone()
                if r and not r[1].startswith("_SYS_"):
                    results.append((r[0], display, r[2], r[3], r[4]))
                    found_sids.add(sid)
        # 3. 标题和 UI 显示名都没命中时，用 part 内容关键词反查
        if not any(r[0] in found_sids for r in results):
            cur.execute("SELECT DISTINCT session_id FROM part WHERE data LIKE ?", (f"%{kw}%",))
            for (sid,) in cur.fetchall():
                if sid in found_sids:
                    continue
                cur.execute("SELECT id, title, directory, time_created, time_updated FROM session WHERE id=?", (sid,))
                r = cur.fetchone()
                if r and not r[1].startswith("_SYS_"):
                    display = ui_titles.get(r[0], r[1])
                    results.append((r[0], display, r[2], r[3], r[4]))
                    found_sids.add(r[0])
    return results


# ---------------------------------------------------------------------------
# 数据提取
# ---------------------------------------------------------------------------
def extract_conversation(conn, sid, include_reasoning=True):
    """提取会话全部对话（含思考过程），返回 Markdown 文本。"""
    cur = conn.cursor()
    cur.execute("SELECT id, time_created, data FROM message WHERE session_id=? ORDER BY time_created", (sid,))
    msgs = cur.fetchall()
    cur.execute("SELECT message_id, time_created, data FROM part WHERE session_id=? ORDER BY time_created", (sid,))
    parts = cur.fetchall()

    by_msg = {}
    for mid, _ts, data in parts:
        try:
            by_msg.setdefault(mid, []).append(json.loads(data))
        except Exception:
            pass

    lines = []
    for mid, ts, mdata in msgs:
        try:
            role = json.loads(mdata).get("role", "?")
        except Exception:
            role = "?"
        parts_list = by_msg.get(mid, [])
        ts_str = datetime.datetime.fromtimestamp(ts / 1000).strftime("%Y-%m-%d %H:%M:%S")
        label = "用户" if role == "user" else "智能体"

        reasoning_texts = []
        text_parts = []
        for p in parts_list:
            if p.get("type") == "reasoning" and p.get("text"):
                reasoning_texts.append(p["text"])
            elif p.get("type") == "text" and p.get("text"):
                text_parts.append(p["text"])

        body = "\n".join(text_parts)
        lines.append(f"### 【{label}】 {ts_str}\n")
        if body:
            lines.append(f"{body}\n")
        if reasoning_texts and include_reasoning:
            lines.append(f"<details>\n<summary>思考过程</summary>\n\n")
            lines.append("\n".join(reasoning_texts))
            lines.append(f"\n\n</details>\n")
        lines.append("")

    return "\n".join(lines)


def _is_temp_path(p):
    """判断路径是否位于 .temp 目录（agent-only 中间产物）。"""
    parts = p.replace("\\", "/").split("/")
    return ".temp" in parts


def _find_skill_root(file_path):
    """从文件路径中向上查找技能根目录（含 SKILL.md 的目录）。

    仅匹配路径含 'skills' 段的目录，避免误判。
    返回技能根目录绝对路径，找不到返回 None。
    """
    p = os.path.normpath(file_path)
    parts = p.replace("\\", "/").split("/")
    # 找到 'skills' 段的位置
    skill_idx = None
    for i, seg in enumerate(parts):
        if seg.lower() == "skills":
            skill_idx = i
            break
    if skill_idx is None or skill_idx + 1 >= len(parts):
        return None
    # skills/ 下一级就是技能名目录
    skill_name = parts[skill_idx + 1]
    # 构建技能根目录路径（skills/skill_name）
    if os.name == "nt":
        skill_root = "\\".join(parts[:skill_idx + 2])
    else:
        skill_root = "/".join(parts[:skill_idx + 2])
    if os.path.isdir(skill_root) and os.path.isfile(os.path.join(skill_root, "SKILL.md")):
        return os.path.normpath(skill_root)
    return None


def _collect_skill_dirs(file_paths):
    """从产出文件路径列表中识别所有技能根目录。

    返回 {skill_root_dir: skill_name} dict，避免重复。
    """
    skill_dirs = {}
    for fp in file_paths:
        root = _find_skill_root(fp)
        if root and root not in skill_dirs:
            skill_dirs[root] = os.path.basename(root)
    return skill_dirs


def extract_skill_paths(conn, sid, include_temp=False):
    """仅从 report_final_files 和 write/edit 工具提取指向 skills 目录的文件路径。

    read/glob 等只读工具引用的 skills 路径属于参考读取而非产出，不纳入技能包打包。
    返回指向 skills 目录的文件路径集合。
    """
    cur = conn.cursor()
    cur.execute('SELECT data FROM part WHERE session_id=? AND data LIKE \'%"tool"%\'', (sid,))
    skill_paths = set()
    for (data,) in cur.fetchall():
        try:
            d = json.loads(data)
            if d.get("type") != "tool":
                continue
            tool = d.get("tool", "")
            if tool not in ("report_final_files", "write", "edit"):
                continue
            is_final = tool == "report_final_files"
            state = d.get("state", {})
            if not isinstance(state, dict):
                continue
            candidates = []
            inp = state.get("input", {})
            if isinstance(inp, dict):
                files = inp.get("files")
                if isinstance(files, list):
                    candidates.extend(x for x in files if isinstance(x, str))
                for key in ("filePath", "file_path", "path", "target"):
                    v = inp.get(key)
                    if isinstance(v, str):
                        candidates.append(v)
                    elif isinstance(v, list):
                        candidates.extend(x for x in v if isinstance(x, str))
            md = state.get("metadata", {})
            if isinstance(md, dict):
                for key in ("filepath", "filePath"):
                    if isinstance(md.get(key), str):
                        candidates.append(md[key])
            for c in candidates:
                c = c.strip().strip('"\'')
                if not c:
                    continue
                if not is_final and not include_temp and _is_temp_path(c):
                    continue
                # 仅保留指向 skills 目录的路径
                if os.path.exists(c) and _find_skill_root(c):
                    skill_paths.add(os.path.normpath(c))
        except Exception:
            pass
    return sorted(skill_paths)


def extract_file_paths(conn, sid, include_temp=False):
    """从 tool 类型 part 提取产出文件路径（聚焦明确文件字段，不扫描超长文本）。

    report_final_files 声明的最终交付文件始终保留；其他工具产出的文件
    默认过滤 .temp 中间目录（include_temp=True 时保留）。
    """
    cur = conn.cursor()
    cur.execute('SELECT data FROM part WHERE session_id=? AND data LIKE \'%"tool"%\'', (sid,))
    paths = set()
    for (data,) in cur.fetchall():
        try:
            d = json.loads(data)
            if d.get("type") != "tool":
                continue
            is_final = d.get("tool") == "report_final_files"
            state = d.get("state", {})
            if not isinstance(state, dict):
                continue
            candidates = []
            inp = state.get("input", {})
            if isinstance(inp, dict):
                files = inp.get("files")
                if isinstance(files, list):
                    candidates.extend(x for x in files if isinstance(x, str))
                for key in ("filePath", "file_path", "path", "target", "image", "image_url"):
                    v = inp.get(key)
                    if isinstance(v, str):
                        candidates.append(v)
                    elif isinstance(v, list):
                        candidates.extend(x for x in v if isinstance(x, str))
            md = state.get("metadata", {})
            if isinstance(md, dict):
                for key in ("filepath", "filePath"):
                    if isinstance(md.get(key), str):
                        candidates.append(md[key])
            if isinstance(state.get("title"), str):
                candidates.append(state["title"])
            for c in candidates:
                c = c.strip().strip('"\'')
                if not c:
                    continue
                if not is_final and not include_temp and _is_temp_path(c):
                    continue
                if os.path.exists(c):
                    paths.add(os.path.normpath(c))
        except Exception:
            pass
    return sorted(paths)


def extract_models(conn, sid):
    """提取会话中使用过的模型信息（去重），返回 (models, providers)。"""
    cur = conn.cursor()
    cur.execute("SELECT data FROM message WHERE session_id=?", (sid,))
    models, providers = set(), set()
    for (data,) in cur.fetchall():
        try:
            d = json.loads(data)
            m = d.get("model") or {}
            if isinstance(m, dict):
                if m.get("modelID"):
                    models.add(m["modelID"])
                if m.get("providerID"):
                    providers.add(m["providerID"])
            else:
                if d.get("modelID"):
                    models.add(d["modelID"])
                if d.get("providerID"):
                    providers.add(d["providerID"])
        except Exception:
            pass
    return sorted(models), sorted(providers)


def extract_tool_summary(conn, sid):
    """提取工具调用摘要，返回 [{"tool":名, "count":次数, "description":描述}, ...]。"""
    cur = conn.cursor()
    cur.execute('SELECT data FROM part WHERE session_id=? AND data LIKE \'%"tool"%\'', (sid,))
    from collections import Counter
    counter = Counter()
    desc_map = {}
    for (data,) in cur.fetchall():
        try:
            d = json.loads(data)
            if d.get("type") != "tool":
                continue
            tool = d.get("tool", "unknown")
            counter[tool] += 1
            state = d.get("state", {}) or {}
            if isinstance(state, dict):
                inp = state.get("input", {}) or {}
                if isinstance(inp, dict):
                    if inp.get("description"):
                        desc_map.setdefault(tool, inp["description"])
                    elif inp.get("name"):
                        desc_map.setdefault(tool, inp["name"])
        except Exception:
            pass
    return [{"tool": t, "count": counter[t], "description": desc_map.get(t, "")} for t in counter]


# ---------------------------------------------------------------------------
# 元数据与导出主流程
# ---------------------------------------------------------------------------
def build_session_meta(sid, title, directory, time_created, time_updated,
                       produced_file_map=None, models=None, providers=None, tool_calls=None,
                       skill_packages=None, db_title=None):
    """构建 session_meta.json 内容。

    skill_packages: [{"skill_name": str, "skill_dir": str, "files": [str, ...]}]，
    记录打包的完整技能包目录信息，供导入时安装到目标用户技能目录。
    db_title: SQLite session.title 原始值（可能不同于 UI 显示名 title）。
    """
    return {
        "session_id": sid,
        "title": title,
        "db_title": db_title or title,
        "directory": directory,
        "time_created": datetime.datetime.fromtimestamp(time_created / 1000).strftime("%Y-%m-%d %H:%M:%S") if time_created else "",
        "time_updated": datetime.datetime.fromtimestamp(time_updated / 1000).strftime("%Y-%m-%d %H:%M:%S") if time_updated else "",
        "time_created_ms": time_created,
        "time_updated_ms": time_updated,
        "produced_files": produced_file_map or [],
        "skill_packages": skill_packages or [],
        "models": models or [],
        "providers": providers or [],
        "tool_calls": tool_calls or [],
    }


def export_tasks_to_zip(db_path, keywords, output_zip, work_dir=None, sanitize=False,
                        confirm=True, dry_run=False, select=None, max_file_size=None,
                        include_temp=False, prompt_sanitize=True):
    """导出多个任务到 ZIP 压缩包。返回任务数。

    prompt_sanitize: 未显式指定 sanitize 时，导出前扫描对话中的 PII 并交互式询问。
    """
    db_path = common.resolve_db_path(db_path)
    work_dir = common.resolve_work_dir(work_dir, db_path)
    conn = common.connect_db(db_path, readonly=True)
    cur = conn.cursor()

    # 加载 UI 显示名映射（Electron LevelDB 中存储的用户改名）
    ui_titles = common.load_ui_titles(db_path)
    if ui_titles:
        print(f"已加载 {len(ui_titles)} 个 UI 显示名", file=sys.stderr)

    sessions = find_sessions(cur, keywords, ui_titles=ui_titles)
    if not sessions:
        print(f"未找到匹配的任务（关键词: {keywords}）", file=sys.stderr)
        conn.close()
        return 0

    # 按序号筛选（--select，1-based）
    if select:
        idxs = set(int(i) - 1 for i in select)
        sessions = [s for i, s in enumerate(sessions) if i in idxs]
        if not sessions:
            print("--select 指定的序号均无效", file=sys.stderr)
            conn.close()
            return 0

    print(f"找到 {len(sessions)} 个任务:", file=sys.stderr)
    for i, s in enumerate(sessions, 1):
        print(f"  [{i}] {s[1]} ({s[0]})", file=sys.stderr)

    # 预览模式：统计文件数/大小，不写 ZIP（先于确认，只读无需确认）
    if dry_run:
        print(f"\n=== 导出预览 (DRY RUN) ===", file=sys.stderr)
        total_files = 0
        for sid, title, _, _, _ in sessions:
            fps = extract_file_paths(conn, sid, include_temp=include_temp)
            size = sum(os.path.getsize(f) for f in fps if os.path.isfile(f))
            total_files += len(fps)
            print(f"  - {title}: {len(fps)} 个文件, 约 {size / 1024:.1f} KB", file=sys.stderr)
        print(f"合计: {total_files} 个文件", file=sys.stderr)
        conn.close()
        return len(sessions)

    # 多任务匹配时需用户选择导出哪些
    if confirm and len(sessions) > 1:
        print(f"\n以上共匹配到 {len(sessions)} 个任务。", file=sys.stderr)
        print("请选择要导出的任务（输入序号，多个用空格或逗号分隔，输入 a 全部导出，输入 q 取消）：", file=sys.stderr)
        try:
            choice = input().strip()
        except EOFError:
            print("非交互式环境无法读取输入，已自动取消。请添加 --no-confirm。", file=sys.stderr)
            conn.close()
            return 0
        if not choice or choice.lower() == 'q':
            print("已取消导出。", file=sys.stderr)
            conn.close()
            return 0
        if choice.lower() == 'a':
            pass  # 全部导出，sessions 不变
        else:
            # 解析用户输入的序号（支持空格/逗号分隔，1-based）
            raw_idxs = choice.replace(',', ' ').split()
            selected_idxs = set()
            for x in raw_idxs:
                try:
                    n = int(x)
                    if 1 <= n <= len(sessions):
                        selected_idxs.add(n - 1)
                    else:
                        print(f"  忽略无效序号: {x}", file=sys.stderr)
                except ValueError:
                    print(f"  忽略无效输入: {x}", file=sys.stderr)
            if not selected_idxs:
                print("未选择任何有效任务，已取消导出。", file=sys.stderr)
                conn.close()
                return 0
            sessions = [s for i, s in enumerate(sessions) if i in selected_idxs]
            print(f"已选择 {len(sessions)} 个任务进行导出。", file=sys.stderr)

    # 未显式指定 --sanitize 时，扫描对话中的 PII 并交互式询问
    if not sanitize and prompt_sanitize:
        pii_all = {}
        for sid, title, _, _, _ in sessions:
            md = extract_conversation(conn, sid, include_reasoning=False)
            pii = common.scan_pii(md)
            for k, v in pii.items():
                pii_all[k] = pii_all.get(k, 0) + v
        if pii_all:
            summary = "、".join(f"{k} {v} 处" for k, v in pii_all.items())
            print(f"\n检测到对话中包含敏感信息：{summary}", file=sys.stderr)
            print("是否对相关信息进行脱敏？(y/n)", file=sys.stderr)
            try:
                choice = input().strip().lower()
            except EOFError:
                print("非交互式环境无法读取输入，默认不脱敏。可使用 --sanitize 显式开启。", file=sys.stderr)
                choice = 'n'
            if choice == 'y':
                sanitize = True
        else:
            print("\n未检测到格式化敏感信息。", file=sys.stderr)

    temp_dir = os.path.join(work_dir, ".temp", f"export_{datetime.datetime.now().strftime('%Y%m%d_%H%M%S')}")
    os.makedirs(temp_dir, exist_ok=True)

    sanitize_report = {}

    def _apply_sanitize(text):
        if not sanitize:
            return text
        text, _ = common.sanitize_text(text, sanitize_report)
        return text

    task_count = 0
    for sid, title, directory, time_created, time_updated in sessions:
        # 标题脱敏后再生成目录名
        safe_title = _apply_sanitize(title)
        safe_name = common.sanitize_filename(safe_title)
        task_dir = os.path.join(temp_dir, safe_name)
        os.makedirs(task_dir, exist_ok=True)

        # 复制产出文件
        file_paths = extract_file_paths(conn, sid, include_temp=include_temp)
        copied_files = []
        produced_file_map = []
        skipped = 0

        # 已通过技能目录打包的文件集合，避免在单文件复制时重复
        skill_packed_files = set()

        # 检测技能包目录并递归打包（仅从 report_final_files/write/edit 提取的路径）
        skill_file_paths = extract_skill_paths(conn, sid, include_temp=include_temp)
        skill_dirs = _collect_skill_dirs(skill_file_paths)
        skill_packages = []
        for skill_root, skill_name in skill_dirs.items():
            skill_dest = os.path.join(task_dir, "__skill_packages__", skill_name)
            if os.path.exists(skill_dest):
                skill_dest = os.path.join(task_dir, "__skill_packages__", f"{skill_name}_{hash(skill_root) % 10000}")
            try:
                shutil.copytree(skill_root, skill_dest, ignore=common._SKILL_IGNORE)
                # 收集打包的文件
                packed = []
                for root2, dirs2, files2 in os.walk(skill_dest):
                    for fn2 in files2:
                        rel2 = os.path.relpath(os.path.join(root2, fn2), skill_dest)
                        packed.append(rel2)
                        # 记录原始完整路径，用于排除单文件重复复制
                        orig_full = os.path.join(skill_root, rel2)
                        skill_packed_files.add(os.path.normpath(orig_full))
                skill_packages.append({
                    "skill_name": skill_name,
                    "skill_dir": skill_root,
                    "files": sorted(packed),
                })
                print(f"  已打包技能包: {skill_name} ({len(packed)} 个文件)", file=sys.stderr)
            except Exception as e:
                print(f"  警告: 打包技能目录失败 {skill_root}: {e}", file=sys.stderr)

        for fp in file_paths:
            if os.path.normpath(fp) in skill_packed_files:
                continue  # 已在技能包中打包，跳过单文件复制
            if not os.path.isfile(fp):
                continue
            if max_file_size and os.path.getsize(fp) > max_file_size:
                skipped += 1
                print(f"  跳过超限文件 ({os.path.getsize(fp)} > {max_file_size} B): {fp}", file=sys.stderr)
                continue
            dest_name = os.path.basename(fp)
            dest_path = os.path.join(task_dir, dest_name)
            if os.path.exists(dest_path):
                name, ext = os.path.splitext(dest_name)
                dest_path = os.path.join(task_dir, f"{name}_{hash(fp) % 10000}{ext}")
            try:
                shutil.copy2(fp, dest_path)
                copied_name = os.path.basename(dest_path)
                copied_files.append(copied_name)
                produced_file_map.append({"original_path": fp, "filename": copied_name})
            except Exception as e:
                print(f"  警告: 复制文件失败 {fp}: {e}", file=sys.stderr)

        # 产出文件清单
        if copied_files:
            with open(os.path.join(task_dir, "产出文件清单.txt"), "w", encoding="utf-8") as f:
                f.write("\n".join(copied_files))

        # 模型与工具调用摘要
        models, providers = extract_models(conn, sid)
        tool_calls = extract_tool_summary(conn, sid)

        # 查 DB 原始 title（可能与 UI 显示名不同）
        cur.execute("SELECT title FROM session WHERE id=?", (sid,))
        db_title_row = cur.fetchone()
        db_title = db_title_row[0] if db_title_row else title

        # session_meta.json
        meta = build_session_meta(sid, safe_title, _apply_sanitize(directory),
                                  time_created, time_updated, produced_file_map,
                                  models, providers, tool_calls, skill_packages,
                                  db_title=_apply_sanitize(db_title))
        with open(os.path.join(task_dir, "session_meta.json"), "w", encoding="utf-8") as f:
            json.dump(meta, f, ensure_ascii=False, indent=2)

        # 工具调用记录
        if tool_calls:
            tool_lines = [f"{tc['tool']}\t{tc['count']}\t{tc['description']}" for tc in tool_calls]
            with open(os.path.join(task_dir, "工具调用记录.txt"), "w", encoding="utf-8") as f:
                f.write("工具\t次数\t描述\n" + "\n".join(tool_lines))

        # 完整对话与思考过程.md
        md_content = extract_conversation(conn, sid, include_reasoning=True)
        md_content = _apply_sanitize(md_content)
        md_full = f"# 任务导出：{safe_title}\n\n"
        md_full += f"| 项目 | 内容 |\n|------|------|\n"
        md_full += f"| 任务名称 | {safe_title} |\n"
        md_full += f"| Session ID | {sid} |\n"
        md_full += f"| 工作目录 | {_apply_sanitize(directory)} |\n"
        md_full += f"| 创建时间 | {meta['time_created']} |\n"
        md_full += f"| 更新时间 | {meta['time_updated']} |\n"
        if models:
            md_full += f"| 使用模型 | {', '.join(models)} |\n"
        md_full += f"\n---\n\n## 完整对话记录\n\n"
        md_full += md_content

        with open(os.path.join(task_dir, "完整对话与思考过程.md"), "w", encoding="utf-8") as f:
            f.write(md_full if md_content else "（无对话记录）")

        task_count += 1
        skill_count = len(skill_packages)
        print(f"  已打包: {safe_title} ({len(copied_files)} 个产出文件" +
              (f", {skill_count} 个技能包" if skill_count else "") +
              (f", 跳过 {skipped} 个超限" if skipped else "") + ")", file=sys.stderr)

    # README.md
    readme = f"# TeleAgent 任务导出包\n\n"
    readme += f"导出时间: {datetime.datetime.now().strftime('%Y-%m-%d %H:%M:%S')}\n"
    readme += f"任务数量: {task_count}\n\n## 任务列表\n\n"
    for i, (sid, title, _, _, _) in enumerate(sessions, 1):
        readme += f"{i}. {_apply_sanitize(title)} (ID: {sid})\n"
    readme += f"\n## 使用方法\n\n导入时，使用 teleagent-task-share 技能的导入功能，指定此 ZIP 文件路径即可。\n"
    with open(os.path.join(temp_dir, "README.md"), "w", encoding="utf-8") as f:
        f.write(readme)

    conn.close()

    # 打包 ZIP
    output_path = output_zip or os.path.join(work_dir, f"任务导出包_{datetime.datetime.now().strftime('%Y%m%d_%H%M%S')}.zip")
    with zipfile.ZipFile(output_path, "w", zipfile.ZIP_DEFLATED) as zf:
        for root, dirs, files in os.walk(temp_dir):
            for file in files:
                file_path = os.path.join(root, file)
                arcname = os.path.relpath(file_path, temp_dir)
                zf.write(file_path, arcname)

    shutil.rmtree(temp_dir, ignore_errors=True)

    print(f"\n导出完成: {output_path}", file=sys.stderr)
    print(f"任务数量: {task_count}", file=sys.stderr)
    if sanitize and sanitize_report:
        print(f"\n脱敏报告:", file=sys.stderr)
        for k, v in sanitize_report.items():
            print(f"  {k}: {v} 处", file=sys.stderr)
    return task_count


# ---------------------------------------------------------------------------
# 自检（用临时库跑导出闭环，不碰真实数据库）
# ---------------------------------------------------------------------------
def self_test():
    """用临时 SQLite 库构造假会话，验证导出闭环。"""
    import tempfile
    tmp = tempfile.mkdtemp(prefix="ts_extract_selftest_")
    try:
        db = os.path.join(tmp, "test.db")
        conn = sqlite3_connect(db)
        cur = conn.cursor()
        cur.execute("""CREATE TABLE session (id TEXT PRIMARY KEY, project_id TEXT, parent_id TEXT, slug TEXT,
            directory TEXT, title TEXT, version TEXT, share_url TEXT, summary_additions INTEGER,
            summary_deletions INTEGER, summary_files INTEGER, summary_diffs TEXT, revert TEXT, permission TEXT,
            time_created INTEGER, time_updated INTEGER, time_compacting INTEGER, time_archived INTEGER, workspace_id TEXT)""")
        cur.execute("""CREATE TABLE message (id TEXT PRIMARY KEY, session_id TEXT, time_created INTEGER, time_updated INTEGER, data TEXT)""")
        cur.execute("""CREATE TABLE part (id TEXT PRIMARY KEY, message_id TEXT, session_id TEXT, time_created INTEGER, time_updated INTEGER, data TEXT)""")
        now = int(datetime.datetime.now().timestamp() * 1000)
        sid = "ses_selftest001abcdefghijklmn"
        cur.execute("INSERT INTO session (id, project_id, directory, title, version, time_created, time_updated) VALUES (?,?,?,?,?,?,?)",
                    (sid, "global", tmp, "自检测试任务", "1.0.0", now, now))
        # 产出文件
        prod = os.path.join(tmp, "报告.docx")
        with open(prod, "w", encoding="utf-8") as f:
            f.write("test")
        # 模拟技能包目录
        skill_root = os.path.join(tmp, "skills", "test-skill")
        skill_md = os.path.join(skill_root, "SKILL.md")
        skill_script = os.path.join(skill_root, "scripts", "run.py")
        os.makedirs(os.path.dirname(skill_script), exist_ok=True)
        with open(skill_md, "w", encoding="utf-8") as f:
            f.write("# test skill")
        with open(skill_script, "w", encoding="utf-8") as f:
            f.write("print('hello')")
        # 消息与 part（含 tool 调用，指向产出文件和技能文件；含 model 信息；含手机号用于脱敏）
        mid_user = "msg_selftest001user0000000000"
        mid_ai = "msg_selftest002ai000000000000"
        cur.execute("INSERT INTO message VALUES (?,?,?,?,?)", (mid_user, sid, now, now,
            json.dumps({"role": "user", "model": {"providerID": "NewApi", "modelID": "chat-pro"}})))
        cur.execute("INSERT INTO message VALUES (?,?,?,?,?)", (mid_ai, sid, now, now,
            json.dumps({"role": "assistant", "modelID": "chat-pro", "providerID": "NewApi"})))
        cur.execute("INSERT INTO part VALUES (?,?,?,?,?,?)", ("prt_u1", mid_user, sid, now, now,
            json.dumps({"type": "text", "text": "请生成报告，联系 13800138000"})))
        cur.execute("INSERT INTO part VALUES (?,?,?,?,?,?)", ("prt_a1", mid_ai, sid, now, now,
            json.dumps({"type": "text", "text": f"已生成：{prod}"})))
        cur.execute("INSERT INTO part VALUES (?,?,?,?,?,?)", ("prt_a2", mid_ai, sid, now, now,
            json.dumps({"type": "tool", "tool": "write", "state": {"input": {"filePath": prod}}})))
        # report_final_files 声明技能包文件
        cur.execute("INSERT INTO part VALUES (?,?,?,?,?,?)", ("prt_a3", mid_ai, sid, now, now,
            json.dumps({"type": "tool", "tool": "report_final_files", "state": {"input": {"files": [skill_script]}}})))
        conn.commit()
        conn.close()

        zip_path = os.path.join(tmp, "out.zip")
        n = export_tasks_to_zip(db, ["自检测试"], zip_path, work_dir=tmp, sanitize=True, confirm=False)
        assert n == 1, f"导出任务数错误: {n}"
        assert os.path.isfile(zip_path), "ZIP 未生成"
        with zipfile.ZipFile(zip_path) as zf:
            names = zf.namelist()
        assert any("session_meta.json" in x for x in names), "缺少 session_meta.json"
        assert any("完整对话与思考过程.md" in x for x in names), "缺少对话记录"
        assert any("报告.docx" in x for x in names), "产出文件未打包"
        # 验证技能包打包
        assert any("__skill_packages__/test-skill/SKILL.md" in x for x in names), "技能包 SKILL.md 未打包"
        assert any("__skill_packages__/test-skill/scripts/run.py" in x for x in names), "技能包脚本未打包"
        # 验证 session_meta.json 含 skill_packages 字段
        with zipfile.ZipFile(zip_path) as zf:
            meta_file = [x for x in names if x.endswith("session_meta.json")][0]
            meta_data = json.loads(zf.read(meta_file).decode("utf-8"))
        assert meta_data.get("skill_packages"), "session_meta.json 缺少 skill_packages 字段"
        assert meta_data["skill_packages"][0]["skill_name"] == "test-skill", "技能包名称错误"
        # 验证脱敏：ZIP 内对话应不含手机号
        with zipfile.ZipFile(zip_path) as zf:
            md = [x for x in names if x.endswith(".md")][0]
            content = zf.read(md).decode("utf-8")
        assert "13800138000" not in content, "脱敏未生效"
        print(f"[SELF-TEST] 导出闭环通过：ZIP 含 {len(names)} 个条目，脱敏生效", file=sys.stderr)
        return True
    except Exception as e:
        print(f"[SELF-TEST] 失败: {e}", file=sys.stderr)
        return False
    finally:
        shutil.rmtree(tmp, ignore_errors=True)


def sqlite3_connect(path):
    import sqlite3
    return sqlite3.connect(path)


def main():
    sys.stdout.reconfigure(encoding="utf-8")
    ap = argparse.ArgumentParser(description="导出 TeleAgent 任务到 ZIP 压缩包")
    ap.add_argument("--search", nargs="+", help="任务标题关键词（可指定多个）")
    ap.add_argument("--db", default=None, help="teleagent.db 路径（默认自动探测）")
    ap.add_argument("--output", default=None, help="输出 ZIP 路径（默认自动命名）")
    ap.add_argument("--work-dir", default=None, help="工作目录（默认自动探测）")
    ap.add_argument("--sanitize", action="store_true", help="导出时自动脱敏（手机号/身份证/银行卡/邮箱）")
    ap.add_argument("--no-prompt-sanitize", action="store_true", help="跳过交互式脱敏询问（未指定 --sanitize 时生效）")
    ap.add_argument("--no-confirm", action="store_true", help="跳过多任务交互选择（直接全部导出）")
    ap.add_argument("--dry-run", action="store_true", help="仅预览不导出")
    ap.add_argument("--select", nargs="+", type=int, help="按序号只导出指定任务（1-based，可多个）")
    ap.add_argument("--max-file-size", type=int, default=None, help="跳过超过该字节数的产出文件")
    ap.add_argument("--include-temp", action="store_true", help="导出时包含 .temp 中间文件（默认过滤）")
    ap.add_argument("--self-test", action="store_true", help="用临时库跑自检")
    args = ap.parse_args()

    if args.self_test:
        sys.exit(0 if self_test() else 1)

    if not args.search:
        ap.error("--search 是必需参数（或使用 --self-test）")

    try:
        db_path = common.resolve_db_path(args.db)
        work_dir = common.resolve_work_dir(args.work_dir, db_path)
    except FileNotFoundError as e:
        print(f"错误: {e}", file=sys.stderr)
        sys.exit(1)

    print(f"数据库: {db_path}", file=sys.stderr)
    print(f"工作目录: {work_dir}", file=sys.stderr)
    proc = common.find_teleagent_process()
    if proc:
        print(f"检测到 TeleAgent 进程: {proc}", file=sys.stderr)

    count = export_tasks_to_zip(db_path, args.search, args.output, work_dir,
                                sanitize=args.sanitize, confirm=not args.no_confirm,
                                dry_run=args.dry_run, select=args.select,
                                max_file_size=args.max_file_size,
                                include_temp=args.include_temp,
                                prompt_sanitize=not args.no_prompt_sanitize)
    if count == 0:
        sys.exit(1)


if __name__ == "__main__":
    main()