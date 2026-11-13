from pathlib import Path

p = Path("scripts/fetch_cses_bundle.py")
src = p.read_text()

old_loop = """    for idx, meta in enumerate(tasks, 1):
        task_id = meta["task_id"]"""

new_loop = """    for idx, meta in enumerate(tasks, 1):
        task_id = meta["task_id"]
        slug = f"cses_{task_id}_{slugify(meta['title'])}"
        existing_json = out_root / slug / "problem.json"
        if existing_json.exists() and len(list((out_root / slug / "tests").glob("*.in"))) > 1:
            print(f"[{idx:03d}/{len(tasks):03d}] SKIP {meta['title']} (already packaged)")
            continue"""

if "already packaged" not in src and old_loop in src:
    src = src.replace(old_loop, new_loop, 1)
    p.write_text(src)
    print("Patched fetch_cses_bundle.py with resume support.")
else:
    print("Resume support already present or loop already updated.")
