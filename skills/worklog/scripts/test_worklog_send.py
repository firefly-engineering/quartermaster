"""Self-check for worklog-send against a fake herdr. Run: python3 test_worklog_send.py"""
import json
import os
import pathlib
import subprocess
import sys
import tempfile

SCRIPT = pathlib.Path(__file__).with_name("worklog-send")

NOTE = """# Worklog
- [▶ Run batch](obsidian://shell-commands/?execute=x&_date=2026-10-16&_item=batch-c)

```herdr-prompt id=batch-c to=trunk
/mass-implement arbora-partners/trunk #871 #819
```

- ⬜ **Deleter SA for IaC.** ^ask-iac
  > [!prompt]- Prompt sent to IaC
  > ```herdr-prompt id=ask-iac to=IaC
  > Please create the deleter SA.
  >   Indented line.
  > Reply with links.
  > ```
"""


def run(tmp, *args, status="idle"):
    fake = tmp / "bin" / "herdr"
    fake.parent.mkdir(exist_ok=True)
    spaces = {"result": {"workspaces": [{"workspace_id": "wZ", "label": "trunk"}, {"workspace_id": "wX", "label": "IaC"}]}}
    agents = {"result": {"agents": [{"workspace_id": "wZ", "pane_id": "wZ:p6", "agent_status": status},
                                    {"workspace_id": "wX", "pane_id": "wX:p9", "agent_status": "idle"}]}}
    fake.write_text(f"""#!/bin/sh
case "$1 $2" in
  "workspace list") echo '{json.dumps(spaces)}' ;;
  "agent list") echo '{json.dumps(agents)}' ;;
  "agent prompt") printf '%s\\n%s' "$3" "$4" > "{tmp}/sent" ;;
esac
""")
    fake.chmod(0o755)
    env = {**os.environ, "PATH": f"{fake.parent}:{os.environ['PATH']}"}
    return subprocess.run([sys.executable, str(SCRIPT), str(tmp), *args], capture_output=True, text=True, env=env)


with tempfile.TemporaryDirectory() as d:
    tmp = pathlib.Path(d)
    (tmp / "2026-10-16.md").write_text(NOTE)

    r = run(tmp, "2026-10-16", "batch-c")
    assert r.returncode == 0, r.stderr
    sent = (tmp / "sent").read_text()
    assert sent.startswith("wZ:p6\n/mass-implement arbora-partners/trunk #871 #819\n\nWhen this is done, update the worklog"), sent
    assert f"^batch-c in {tmp}/2026-10-16.md" in sent, sent

    r = run(tmp, "2026-10-16", "ask-iac")
    assert r.returncode == 0 and (tmp / "sent").read_text().startswith("wX:p9\nPlease create the deleter SA.\n  Indented line.\nReply with links.\n\n"), r.stderr

    for bad in [("2026-10-16", "x;rm"), ("../etc", "batch-c"), ("2026-10-16", "missing")]:
        r = run(tmp, *bad)
        assert r.returncode == 1, bad

    r = run(tmp, "2026-10-16", "batch-c", status="blocked")
    assert r.returncode == 1 and "blocked" in r.stderr, r.stderr
print("ok")
