from __future__ import annotations

import os
import shlex
from pathlib import Path


def source_files() -> list[Path]:
    sources = os.environ["PYTHON_SOURCES"]

    files = []
    for item in shlex.split(sources):
        path = Path(item)

        if path.is_dir():
            files.extend(
                Path(t[0]) / filename
                for t in os.walk(path)
                for filename in t[2]
                if filename.endswith(".py")
            )
        else:
            files.append(path)

    return sorted(files)


def files() -> None:
    print("\n".join(str(path) for path in source_files()))
