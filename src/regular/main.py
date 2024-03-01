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
from typing import TYPE_CHECKING, Protocol, Union

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
    RESULT_TITLE_FAILURE = "Job {name!r} failed with code {exit_status}"
    RESULT_TITLE_SUCCESS = "Job {name!r} succeeded"
    RESULT_TEXT = "stderr:\n{stderr}\nstdout:\n{stdout}"


APP_NAME = "regular"
APP_AUTHOR = "dbohdan"
SCHEDULE_RE = " *".join(
    ["", *(rf"(?:(\d+) *({unit}))?" for unit in ("w", "d", "h", "m", "s")), ""]
)
SMTP_SERVER = "127.0.0.1"


@dataclass(frozen=True)
class Config:
    config_dir: Path
    notifiers: list[Notifier]
    state_dir: Path


@dataclass(frozen=True)
class JobResultLocked:
    name: str


@dataclass(frozen=True)
class JobResultRan:
    name: str
    exit_status: int
    stdout: str
    stderr: str


@dataclass(frozen=True)
class JobResultTooEarly:
    name: str


if TYPE_CHECKING:
    Env = dict[str, str]
    JobResult = Union[JobResultLocked, JobResultRan, JobResultTooEarly]

    class Notifier(Protocol):
        def __call__(self, result: JobResult) -> None:
            ...


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


def notify_user(result: JobResult, *, config: Config) -> None:
    for notifier in config.notifiers:
        notifier(result)


def notify_user_by_email(result: JobResult) -> None:
    if not isinstance(result, JobResultRan):
        return

    msg = EmailMessage()

    subj_template = (
        Messages.RESULT_TITLE_SUCCESS
        if result.exit_status == 0
        else Messages.RESULT_TITLE_FAILURE
    )

    msg["Subject"] = subj_template.format(
        name=result.name, exit_status=result.exit_status
    )
    msg["From"] = APP_NAME
    msg["To"] = getpass.getuser()

    msg.set_content(
        Messages.RESULT_TEXT.format(
            name=result.name,
            exit_status=result.exit_status,
            stdout=result.stdout,
            stderr=result.stderr,
        )
    )

    smtp = smtplib.SMTP(SMTP_SERVER)
    smtp.send_message(msg)
    smtp.quit()


def run_job(job_dir: Path, *, config: Config, name: str = "") -> JobResult:
    if not name:
        name = job_dir.name

    lock_path = config.state_dir / name / FileDirNames.RUNNING_LOCK
    lock_path.parent.mkdir(parents=True, exist_ok=True)

    try:
        with portalocker.Lock(
            config.state_dir / name / FileDirNames.RUNNING_LOCK,
            fail_when_locked=True,
            mode="a",
        ):
            return run_job_without_lock(job_dir, config=config, name=name)
    except portalocker.AlreadyLocked:
        return JobResultLocked(name=name)


def run_job_without_lock(job_dir: Path, *, config: Config, name: str) -> JobResult:
    env = load_env(config.config_dir / FileDirNames.ENV, job_dir / FileDirNames.ENV)

    filename = read_text_or_default(job_dir / FileDirNames.FILENAME, Defaults.FILENAME)
    schedule = read_text_or_default(job_dir / FileDirNames.SCHEDULE, Defaults.SCHEDULE)

    last_run_file = config.state_dir / FileDirNames.JOBS / name / FileDirNames.LAST_RUN
    last_run = last_run_file.stat().st_mtime if last_run_file.exists() else None

    min_delay = parse_schedule(schedule).total_seconds()

    if last_run is not None and time.time() - last_run < min_delay:
        return JobResultTooEarly(name=name)

    last_run_file.parent.mkdir(parents=True, exist_ok=True)
    last_run_file.touch(exist_ok=True)

    completed = sp.run(
        [job_dir / filename], capture_output=True, check=False, env=env, text=True
    )

    result = JobResultRan(
        name=name,
        exit_status=completed.returncode,
        stdout=completed.stdout,
        stderr=completed.stderr,
    )

    if (job_dir / FileDirNames.NEVER_NOTIFY).exists() or (
        completed.returncode == 0
        and not (job_dir / FileDirNames.ALWAYS_NOTIFY).exists()
    ):
        return result

    notify_user(
        result,
        config=config,
    )

    return result


def run_session(config: Config) -> list[JobResult]:
    return [
        run_job(item, config=config)
        for item in (config.config_dir / FileDirNames.JOBS).iterdir()
        if item.is_dir()
    ]


def main() -> None:
    dirs = PlatformDirs(APP_NAME, APP_AUTHOR)

    config = Config(
        config_dir=Path(environ.get(EnvVars.CONFIG_DIR, dirs.user_config_path)),
        notifiers=[notify_user_by_email],
        state_dir=Path(environ.get(EnvVars.STATE_DIR, dirs.user_state_path)),
    )

    run_session(config)


if __name__ == "__main__":
    main()
