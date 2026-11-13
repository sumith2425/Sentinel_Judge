import argparse
import html
import io
import json
import os
import re
import shutil
import time
import urllib.parse
import urllib.request
import zipfile
from html.parser import HTMLParser
from pathlib import Path

BASE_URL = "https://cses.fi"
USER_AGENT = "Mozilla/5.0 (X11; Linux x86_64) SentinelJudgeImporter/2.2"

STARTER_TEMPLATES = {
    "cpp": "#include <bits/stdc++.h>\nusing namespace std;\n\nint main() {\n    ios::sync_with_stdio(false);\n    cin.tie(nullptr);\n\n    return 0;\n}\n",
    "python": "import sys\n\ndef solve():\n    data = sys.stdin.read().split()\n    if not data:\n        return\n\nif __name__ == \"__main__\":\n    solve()\n",
    "go": "package main\n\nimport (\n\t\"bufio\"\n\t\"fmt\"\n\t\"os\"\n)\n\nfunc main() {\n\tin := bufio.NewReader(os.Stdin)\n\tout := bufio.NewWriter(os.Stdout)\n\tdefer out.Flush()\n\t_ = in\n\t_ = fmt.Println\n}\n",
}


class CSESContentParser(HTMLParser):
    def __init__(self):
        super().__init__()
        self.in_md = False
        self.md_depth = 0
        self.in_h1 = False
        self.in_pre = False
        self.in_li = False
        self.in_math = False
        self.current_section = "description"
        self.h1_buf = []
        self.pre_buf = []
        self.li_buf = []
        self.sections = {
            "description": [],
            "input": [],
            "output": [],
            "constraints": [],
            "example": [],
        }
        self.constraints_items = []
        self.example_pres = []

    def handle_starttag(self, tag, attrs):
        attr_dict = dict(attrs)
        if not self.in_md:
            if tag == "div" and "md" in attr_dict.get("class", "").split():
                self.in_md = True
                self.md_depth = 1
            return

        if tag == "div":
            self.md_depth += 1
        elif tag == "h1":
            self.in_h1 = True
            self.h1_buf = []
        elif tag == "pre":
            self.in_pre = True
            self.pre_buf = []
        elif tag == "li":
            self.in_li = True
            self.li_buf = []
            if self.current_section in self.sections:
                self.sections[self.current_section].append("• ")
        elif tag == "span" and "math" in attr_dict.get("class", "").split():
            self.in_math = True
            self._append_text("$")
        elif tag == "br":
            self._append_text("\n")

    def handle_endtag(self, tag):
        if not self.in_md:
            return

        if tag == "div":
            self.md_depth -= 1
            if self.md_depth <= 0:
                self.in_md = False
                return
        elif tag == "h1" and self.in_h1:
            self.in_h1 = False
            heading = "".join(self.h1_buf).strip().lower()
            if "input" in heading:
                self.current_section = "input"
            elif "output" in heading:
                self.current_section = "output"
            elif "constraint" in heading:
                self.current_section = "constraints"
            elif "example" in heading:
                self.current_section = "example"
            else:
                self.current_section = "description"
        elif tag == "pre" and self.in_pre:
            self.in_pre = False
            content = "".join(self.pre_buf).strip()
            if self.current_section == "example":
                self.example_pres.append(content)
            else:
                self._append_text("\n" + content + "\n")
        elif tag == "li" and self.in_li:
            self.in_li = False
            item_text = "".join(self.li_buf).strip()
            if self.current_section == "constraints" and item_text:
                self.constraints_items.append(item_text)
            self._append_text("\n")
        elif tag == "span" and self.in_math:
            self.in_math = False
            self._append_text("$")
        elif tag in ("p", "ul", "ol"):
            self._append_text("\n\n")

    def handle_data(self, data):
        if not self.in_md:
            return
        if self.in_h1:
            self.h1_buf.append(data)
        elif self.in_pre:
            self.pre_buf.append(data)
        else:
            self._append_text(data)

    def _append_text(self, text):
        if self.in_li:
            self.li_buf.append(text)
        if self.current_section in self.sections:
            self.sections[self.current_section].append(text)


def build_headers(session_id: str = "") -> dict:
    headers = {"User-Agent": USER_AGENT}
    if session_id:
        headers["Cookie"] = f"PHPSESSID={session_id.strip()}"
    return headers


def fetch_html(url: str, session_id: str = "") -> str:
    req = urllib.request.Request(url, headers=build_headers(session_id))
    with urllib.request.urlopen(req, timeout=25) as resp:
        return resp.read().decode("utf-8", errors="replace")


def download_official_test_zip(task_id: str, session_id: str, archive_dir: Path) -> bool:
    if not session_id:
        return False

    target_dir = archive_dir / task_id
    if target_dir.exists() and any(target_dir.glob("*.in")):
        return True

    tests_page_url = f"{BASE_URL}/problemset/tests/{task_id}/"
    try:
        page_html = fetch_html(tests_page_url, session_id)
    except Exception:
        return False

    csrf_match = re.search(r'name="csrf_token"\s+value="([^"]+)"', page_html)
    if not csrf_match:
        return False

    csrf_token = csrf_match.group(1)
    form_data = urllib.parse.urlencode({
        "csrf_token": csrf_token,
        "download": "true",
    }).encode("utf-8")

    headers = build_headers(session_id)
    headers["Content-Type"] = "application/x-www-form-urlencoded"
    headers["Referer"] = tests_page_url

    req = urllib.request.Request(tests_page_url, data=form_data, headers=headers, method="POST")
    try:
        with urllib.request.urlopen(req, timeout=60) as resp:
            content_type = resp.headers.get("Content-Type", "")
            raw_bytes = resp.read()
            if "zip" not in content_type and not raw_bytes.startswith(b"PK"):
                return False

            target_dir.mkdir(parents=True, exist_ok=True)
            with zipfile.ZipFile(io.BytesIO(raw_bytes)) as zf:
                zf.extractall(target_dir)
            return any(target_dir.glob("*.in"))
    except Exception:
        return False


def clean_block(chunks: list) -> str:
    raw = "".join(chunks)
    lines = [line.strip() for line in raw.splitlines()]
    out = []
    prev_empty = False
    for line in lines:
        if not line:
            if not prev_empty and out:
                out.append("")
            prev_empty = True
        else:
            out.append(line)
            prev_empty = False
    return "\n".join(out).strip()


def slugify(text: str) -> str:
    s = text.lower().strip()
    s = re.sub(r"[^a-z0-9]+", "_", s)
    return s.strip("_")


def parse_problem_index(index_html: str):
    class IndexParser(HTMLParser):
        def __init__(self):
            super().__init__()
            self.in_h2 = False
            self.h2_buf = []
            self.current_category = "General"
            self.in_task_a = False
            self.current_task_id = None
            self.a_buf = []
            self.tasks = []
            self.seen_ids = set()

        def handle_starttag(self, tag, attrs):
            attr_dict = dict(attrs)
            if tag == "h2":
                self.in_h2 = True
                self.h2_buf = []
            elif tag == "a":
                href = attr_dict.get("href", "")
                m = re.match(r"^/problemset/task/(\d+)/?$", href)
                if m:
                    self.in_task_a = True
                    self.current_task_id = m.group(1)
                    self.a_buf = []

        def handle_endtag(self, tag):
            if tag == "h2" and self.in_h2:
                self.in_h2 = False
                cat = "".join(self.h2_buf).strip()
                if cat and cat.lower() != "general":
                    self.current_category = cat
            elif tag == "a" and self.in_task_a:
                self.in_task_a = False
                title = "".join(self.a_buf).strip()
                tid = self.current_task_id
                if tid and title and tid not in self.seen_ids:
                    self.seen_ids.add(tid)
                    self.tasks.append({
                        "task_id": tid,
                        "title": title,
                        "category": self.current_category,
                        "display_order": len(self.tasks) + 1,
                    })

        def handle_data(self, data):
            if self.in_h2:
                self.h2_buf.append(data)
            elif self.in_task_a:
                self.a_buf.append(data)

    parser = IndexParser()
    parser.feed(index_html)
    return parser.tasks


def parse_task_page(task_html: str, meta: dict) -> dict:
    time_limit_ms = 1000
    memory_limit_mb = 512

    tl_match = re.search(r"Time limit:\s*([0-9.]+)\s*s", task_html)
    if tl_match:
        time_limit_ms = int(float(tl_match.group(1)) * 1000)

    ml_match = re.search(r"Memory limit:\s*(\d+)\s*MB", task_html)
    if ml_match:
        memory_limit_mb = int(ml_match.group(1))

    parser = CSESContentParser()
    parser.feed(task_html)

    description = clean_block(parser.sections["description"])
    input_format = clean_block(parser.sections["input"]) or "Read from standard input."
    output_format = clean_block(parser.sections["output"]) or "Print to standard output."

    constraints = parser.constraints_items if parser.constraints_items else []
    if not constraints:
        raw_c = clean_block(parser.sections["constraints"])
        if raw_c:
            constraints = [line.lstrip("• ").strip() for line in raw_c.splitlines() if line.strip()]
    constraints.append(f"Time Limit: {time_limit_ms} ms")
    constraints.append(f"Memory Limit: {memory_limit_mb} MB")

    examples = []
    for idx in range(0, len(parser.example_pres) - 1, 2):
        examples.append({
            "input": parser.example_pres[idx],
            "output": parser.example_pres[idx + 1],
        })

    problem_slug = f"cses_{meta['task_id']}_{slugify(meta['title'])}"
    return {
        "id": problem_slug,
        "title": f"{meta['task_id']} · {meta['title']}",
        "category": meta["category"],
        "difficulty": "CSES Standard",
        "display_order": meta["display_order"],
        "time_limit_ms": time_limit_ms,
        "memory_limit_mb": memory_limit_mb,
        "description": description,
        "input_format": input_format,
        "output_format": output_format,
        "constraints": constraints,
        "examples": examples,
        "starter_code": STARTER_TEMPLATES,
    }


def natural_sort_key(path_obj: Path):
    return [int(part) if part.isdigit() else part for part in re.split(r"(\d+)", path_obj.name)]


def copy_matching_tests(task_id: str, archive_root: Path, dest_tests_dir: Path, fallback_examples: list) -> int:
    if dest_tests_dir.exists():
        shutil.rmtree(dest_tests_dir)
    dest_tests_dir.mkdir(parents=True, exist_ok=True)

    matched_dir = archive_root / task_id
    copied = 0

    if matched_dir.exists():
        in_files = sorted(matched_dir.rglob("*.in"), key=natural_sort_key)
        for idx, in_f in enumerate(in_files, 1):
            out_f = in_f.with_suffix(".out")
            if not out_f.exists():
                out_f = in_f.with_suffix(".ans")
            if out_f.exists():
                shutil.copy2(in_f, dest_tests_dir / f"{idx}.in")
                shutil.copy2(out_f, dest_tests_dir / f"{idx}.out")
                copied += 1

    if copied == 0 and fallback_examples:
        for idx, ex in enumerate(fallback_examples, 1):
            (dest_tests_dir / f"{idx}.in").write_text(ex["input"] + "\n", encoding="utf-8")
            (dest_tests_dir / f"{idx}.out").write_text(ex["output"] + "\n", encoding="utf-8")
            copied += 1

    return copied


def main():
    parser = argparse.ArgumentParser(description="Build SentinelJudge problem bundles from CSES.")
    parser.add_argument("--session-id", default=os.environ.get("CSES_PHPSESSID", ""), help="Logged-in CSES PHPSESSID cookie value")
    parser.add_argument("--archive", default="./testdata/external_cses", help="Cache directory for downloaded CSES test archives")
    parser.add_argument("--out", default="./testdata/cses_bundle", help="Output bundle directory")
    parser.add_argument("--limit", type=int, default=0, help="Limit number of problems (0 = all 400)")
    parser.add_argument("--delay", type=float, default=0.5, help="Delay between HTTP requests in seconds")
    args = parser.parse_args()

    if not args.session_id:
        print("WARNING: No --session-id provided. Only 1 sample test case per problem will be fetched.")
        print("Pass --session-id <PHPSESSID> to download all hidden CSES test suites.\n")

    archive_root = Path(args.archive)
    archive_root.mkdir(parents=True, exist_ok=True)
    out_root = Path(args.out)
    out_root.mkdir(parents=True, exist_ok=True)

    print("Fetching CSES problemset catalog...")
    index_html = fetch_html(f"{BASE_URL}/problemset/", args.session_id)
    tasks = parse_problem_index(index_html)
    if args.limit > 0:
        tasks = tasks[:args.limit]

    print(f"Packaging {len(tasks)} problems...\n")

    for idx, meta in enumerate(tasks, 1):
        task_id = meta["task_id"]
        slug = f"cses_{task_id}_{slugify(meta['title'])}"
        existing_json = out_root / slug / "problem.json"
        if existing_json.exists() and len(list((out_root / slug / "tests").glob("*.in"))) > 1:
            print(f"[{idx:03d}/{len(tasks):03d}] SKIP {meta['title']} (already packaged)")
            continue

        task_url = f"{BASE_URL}/problemset/task/{task_id}"
        try:
            task_html = fetch_html(task_url, args.session_id)
            problem_obj = parse_task_page(task_html, meta)

            if args.session_id:
                download_official_test_zip(task_id, args.session_id, archive_root)

            prob_dir = out_root / problem_obj["id"]
            tests_dir = prob_dir / "tests"
            test_count = copy_matching_tests(
                task_id,
                archive_root,
                tests_dir,
                problem_obj["examples"],
            )
            problem_obj["test_count"] = test_count

            (prob_dir / "problem.json").write_text(
                json.dumps(problem_obj, indent=2, ensure_ascii=False),
                encoding="utf-8",
            )

            print(f"[{idx:03d}/{len(tasks):03d}] {problem_obj['title']:<32} | {problem_obj['category']:<22} | {test_count} tests")
        except Exception as exc:
            print(f"[{idx:03d}/{len(tasks):03d}] FAILED {task_id} ({meta['title']}): {exc}")

        time.sleep(args.delay)

    print(f"\nBundle generation complete at {out_root}")


if __name__ == "__main__":
    main()
