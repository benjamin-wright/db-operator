"""Check Go source formatting without modifying the checkout."""

from pathlib import Path
import subprocess

root = Path(__file__).resolve().parents[1]
files = sorted(
    str(path)
    for directory in ("cmd", "internal", "pkg")
    for path in (root / directory).rglob("*.go")
)
result = subprocess.run(
    ["gofmt", "-l", *files], check=True, capture_output=True, text=True, timeout=30
)
if result.stdout:
    raise SystemExit("Go formatting differs:\n" + result.stdout)
print("Go source formatting is current.")
