"""Regenerate API outputs in a temporary source tree and compare their full file set."""

import os
from pathlib import Path
import shutil
import subprocess
import tempfile

ROOT = Path(__file__).resolve().parents[1]
PATTERNS = ("pkg/api/**/zz_generated*.go", "charts/db-operator/crds/*.yaml")


def outputs(root):
    return {
        str(path.relative_to(root)): path.read_bytes()
        for pattern in PATTERNS
        for path in root.glob(pattern)
    }


with tempfile.TemporaryDirectory(prefix="db-operator-generated-") as directory:
    scratch = Path(directory)
    shutil.copytree(ROOT / "pkg", scratch / "pkg")
    for name in ("go.mod", "go.sum", "Makefile"):
        shutil.copy2(ROOT / name, scratch / name)
    for pattern in PATTERNS:
        for path in scratch.glob(pattern):
            path.unlink()
    env = dict(os.environ, GOWORK="off")
    subprocess.run(
        ["make", "generate", f"REPO_ROOT={scratch}"],
        cwd=scratch,
        env=env,
        check=True,
        timeout=180,
    )
    actual, expected = outputs(ROOT), outputs(scratch)
    changed = sorted(
        name
        for name in actual.keys() | expected.keys()
        if actual.get(name) != expected.get(name)
    )
    if changed:
        raise SystemExit("Generated contract drift:\n" + "\n".join(changed))
print("Generated DeepCopy and CRD contracts match their sources.")
