from __future__ import annotations

import getpass
import re
import smtplib
import subprocess as sp
import time
from dataclasses import dataclass
from datetime import timedelta
from email.message import EmailMessage
from functools import reduce
from os import environ
from pathlib import Path
from typing import TYPE_CHECKING, Protocol

import portalocker
from dotenv import dotenv_values
from platformdirs import PlatformDirs


class Defaults:
    FILENAME = "script"
    SCHEDULE = ""


class EnvVars:
    CONFIG_DIR = "REGULAR_CONFIG_DIR"
    STATE_DIR = "REGULAR_STATE_DIR"


class FileDirNames:
    ALWAYS_NOTIFY = "always-notify"
    ENV = "env"
    FILENAME = "filename"
    JOBS = "jobs"
    LAST_RUN = "last"
    NEVER_NOTIFY = "never-notify"
    RUNNING_LOCK = "lock"
    SCHEDULE = "schedule"


class Messages:
    RESULT_TITLE_FAILURE = "Job {name!r} failed with code {returncode}"
    RESULT_TITLE_SUCCESS = "Job {name!r} succeeded"
    RESULT_TEXT = "stderr:\n{stderr}\nstdout:\n{stdout}"


APP_NAME = "regular"
APP_AUTHOR = "dbohdan"
SCHEDULE_RE = " *".join(
    ["", *(rf"(?:(\d+) *({unit}))?" for unit in ("w", "d", "h", "m", "s")), ""]
)
SMTP_SERVER = "127.0.0.1"

if TYPE_CHECKING:
    Env = dict[str, str]

    class Notifier(Protocol):
        def __call__(
            self, job_dir: Path, *, returncode: int, stdout: str, stderr: str
        ) -> None:
            ...


@dataclass(frozen=True)
class Config:
    config_dir: Path
    notifiers: list[Notifier]
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


def notify_user(
    job_dir: Path, *, config: Config, returncode: int, stdout: str, stderr: str
) -> None:
    for notifier in config.notifiers:
        notifier(job_dir, returncode=returncode, stdout=stdout, stderr=stderr)


def notify_user_by_email(
    job_dir: Path, *, returncode: int, stdout: str, stderr: str
) -> None:
    msg = EmailMessage()

    subj_template = (
        Messages.RESULT_TITLE_SUCCESS
        if returncode == 0
        else Messages.RESULT_TITLE_FAILURE
    )

    msg["Subject"] = subj_template.format(name=job_dir.name, returncode=returncode)
    msg["From"] = APP_NAME
    msg["To"] = getpass.getuser()

    msg.set_content(
        Messages.RESULT_TEXT.format(
            name=job_dir.name, returncode=returncode, stdout=stdout, stderr=stderr
        )
    )

    smtp = smtplib.SMTP(SMTP_SERVER)
    smtp.send_message(msg)
    smtp.quit()


def run_job_with_lock(job_dir: Path, *, config: Config, name: str = "") -> None:
    try:
        with portalocker.Lock(
            config.state_dir / name / FileDirNames.RUNNING_LOCK,
            fail_when_locked=True,
            mode="a",
        ):
            run_job(job_dir, config=config, name=name)
    except portalocker.AlreadyLocked:
        pass


def run_job(job_dir: Path, *, config: Config, name: str = "") -> None:
    if not name:
        name = job_dir.name

    env = load_env(config.config_dir / FileDirNames.ENV, job_dir / FileDirNames.ENV)

    filename = read_text_or_default(job_dir / FileDirNames.FILENAME, Defaults.FILENAME)
    schedule = read_text_or_default(job_dir / FileDirNames.SCHEDULE, Defaults.SCHEDULE)

    last_run_file = config.state_dir / FileDirNames.JOBS / name / FileDirNames.LAST_RUN
    last_run = last_run_file.stat().st_mtime if last_run_file.exists() else None

    min_delay = parse_schedule(schedule).total_seconds()

    if last_run is not None and time.time() - last_run < min_delay:
        return

    last_run_file.parent.mkdir(parents=True, exist_ok=True)
    last_run_file.touch(exist_ok=True)

    completed = sp.run(
        [job_dir / filename], capture_output=True, check=False, env=env, text=True
    )

    if (job_dir / FileDirNames.NEVER_NOTIFY).exists() or (
        completed.returncode == 0
        and not (job_dir / FileDirNames.ALWAYS_NOTIFY).exists()
    ):
        return

    notify_user(
        job_dir,
        config=config,
        returncode=completed.returncode,
        stdout=completed.stdout,
        stderr=completed.stderr,
    )


def main() -> None:
    dirs = PlatformDirs(APP_NAME, APP_AUTHOR)

    config = Config(
        config_dir=Path(environ.get(EnvVars.CONFIG_DIR, dirs.user_config_path)),
        notifiers=[notify_user_by_email],
        state_dir=Path(environ.get(EnvVars.STATE_DIR, dirs.user_state_path)),
    )

    for item in (config.config_dir / FileDirNames.JOBS).iterdir():
        if not item.is_dir():
            continue

        run_job_with_lock(item, config=config)


if __name__ == "__main__":
    main()
