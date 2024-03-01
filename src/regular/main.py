from __future__ import annotations

import re
import subprocess as sp
import time
from dataclasses import dataclass
from datetime import timedelta
from functools import reduce
from os import environ
from pathlib import Path
from typing import TYPE_CHECKING

from dotenv import dotenv_values
from platformdirs import PlatformDirs


class Defaults:
    FILENAME = "script"
    SCHEDULE = ""


class EnvVars:
    CONFIG_DIR = "REGULAR_CONFIG_DIR"
    STATE_DIR = "REGULAR_STATE_DIR"


class FileDirNames:
    ENV = "env"
    FILENAME = "filename"
    JOBS = "jobs"
    LAST_RUN = "last"
    SCHEDULE = "schedule"


SCHEDULE_RE = " *".join(
    ["", *(rf"(?:(\d+) *({unit}))?" for unit in ("w", "d", "h", "m", "s")), ""]
)

if TYPE_CHECKING:
    Env = dict[str, str]


@dataclass(frozen=True)
class Config:
    config_dir: Path
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
    env = load_env(config.config_dir / FileDirNames.ENV, job_dir / FileDirNames.ENV)

    filename = read_text_or_default(job_dir / FileDirNames.FILENAME, Defaults.FILENAME)
    schedule = read_text_or_default(job_dir / FileDirNames.SCHEDULE, Defaults.SCHEDULE)

    last_run_file = (
        config.state_dir / FileDirNames.JOBS / job_dir.name / FileDirNames.LAST_RUN
    )
    last_run = last_run_file.stat().st_mtime if last_run_file.exists() else None

    min_delay = parse_schedule(schedule).total_seconds()

    if last_run is None or time.time() - last_run > min_delay:
        sp.run([job_dir / filename], check=True, env=env)

        last_run_file.parent.mkdir(parents=True, exist_ok=True)
        last_run_file.touch(exist_ok=True)


def main() -> None:
    dirs = PlatformDirs("regular", "dbohdan")

    config = Config(
        config_dir=Path(environ.get(EnvVars.CONFIG_DIR, dirs.user_config_path)),
        state_dir=Path(environ.get(EnvVars.STATE_DIR, dirs.user_state_path)),
    )

    for item in (config.config_dir / FileDirNames.JOBS).iterdir():
        if not item.is_dir():
            continue

        run_job(item, config=config)


if __name__ == "__main__":
    main()
