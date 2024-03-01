from __future__ import annotations

import re
import subprocess as sp
import time
from dataclasses import dataclass
from datetime import timedelta, datetime, timezone
from functools import reduce
from os import environ
from pathlib import Path
from typing import TYPE_CHECKING, Optional

from dotenv import dotenv_values
from platformdirs import PlatformDirs

ENV_FILE = "env"
FILENAME_DEFAULT = "script"
FILENAME_FILE = "filename"
JOBS_DIR_DEFAULT = "jobs"
JOBS_DIR_ENV_VAR = "REGULAR_JOBS_DIR"
LAST_RUN_FILE = "last"
SCHEDULE_DEFAULT = ""
SCHEDULE_FILE = "schedule"
STATE_DIR_ENV_VAR = "REGULAR_STATE_DIR"

SCHEDULE_RE = " *".join(
    ["", *(rf"(?:(\d+) *({unit}))?" for unit in ("w", "d", "h", "m", "s")), ""]
)

if TYPE_CHECKING:
    Env = dict[str, str]


@dataclass(frozen=True)
class Config:
    env_file: Path
    jobs_dir: Path
    state_dir: Path


def load_env(*env_files: Path) -> Env:
    return reduce(
        lambda x, y: x | y,
        [*(dotenv_values(env_file) for env_file in env_files), environ],
    )


def read_text_or_default(text_file: Path, default: str) -> str:
    if text_file.exists():
        return text_file.read_text().rstrip()

    return default


def parse_schedule(schedule: str) -> timedelta:
    m = re.fullmatch(SCHEDULE_RE, schedule)

    if not m:
        msg = f"invalid schedule: {schedule!r}"
        raise ValueError(msg)

    weeks, _, days, _, hours, _, minutes, _, seconds, _ = m.groups()

    return timedelta(
        weeks=int(weeks or "0"),
        days=int(days or "0"),
        hours=int(hours or "0"),
        minutes=int(minutes or "0"),
        seconds=int(seconds or "0"),
    )


def run_job(job_dir: Path, *, config: Config) -> None:
    env = load_env(config.env_file, job_dir / ENV_FILE)

    filename = read_text_or_default(job_dir / FILENAME_FILE, FILENAME_DEFAULT)
    schedule = read_text_or_default(job_dir / SCHEDULE_FILE, SCHEDULE_DEFAULT)

    last_run_file = config.state_dir / config.jobs_dir.name / job_dir.name / LAST_RUN_FILE
    last_run = last_run_file.stat().st_mtime if last_run_file.exists() else None

    min_delay = parse_schedule(schedule).total_seconds()

    if last_run is None or time.time() - last_run > min_delay:
        sp.run([job_dir / filename], check=True, env=env)

        last_run_file.parent.mkdir(parents=True, exist_ok=True)
        last_run_file.touch(exist_ok=True)


def main() -> None:
    dirs = PlatformDirs("regular", "dbohdan")

    config = Config(
        env_file=Path(dirs.user_config_path) / ENV_FILE,
        jobs_dir=Path(
            environ.get(
                JOBS_DIR_ENV_VAR, Path(dirs.user_config_path) / JOBS_DIR_DEFAULT
            )
        ),
        state_dir=Path(environ.get(STATE_DIR_ENV_VAR, dirs.user_state_path)),
    )

    for item in config.jobs_dir.iterdir():
        if not item.is_dir():
            continue

        run_job(item, config=config)


if __name__ == "__main__":
    main()
