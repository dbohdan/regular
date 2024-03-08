from __future__ import annotations

import os
import re
import time
from dataclasses import dataclass
from datetime import timedelta
from enum import Enum
from typing import TYPE_CHECKING, Protocol

from termcolor import colored
from typing_extensions import Self

if TYPE_CHECKING:
    from pathlib import Path


def parse_env(
    env_text: str, /, *, subst: bool = True, subst_env: Env | None = None
) -> Env:
    env = {}
    if not subst_env:
        subst_env = {}

    def replacement(m: re.Match) -> str:
        var = m.group(1)

        try:
            return env[var] if var in env else subst_env[var]
        except KeyError as e:
            msg = f"can't substitute env variable: {var!r}"
            raise KeyError(msg) from e

    for raw_line in env_text.splitlines():
        line = raw_line.strip()

        if not line or line.startswith("#"):
            continue

        if "=" in line:
            k, v = line.split("=", 1)
            k = k.rstrip()
            v = v.lstrip()

            v_subst = subst

            if (v.startswith('"') and v.endswith('"')) or (
                v.startswith("'") and v.endswith("'")
            ):
                if v.startswith("'"):
                    v_subst = False

                v = v[1:-1]

            if v_subst:
                # Replace all instances of `${foo}`
                # with the key `foo` in `env` or `subst_env`.
                v = re.sub(r"\${([^}\0=]+)\}", replacement, v)

            env[k] = v

            continue

        msg = f"can't parse env file line {line!r}"
        raise ValueError(msg)

    return env


def load_env(
    env_file: Path, *, subst: bool = True, subst_env: Env | None = None
) -> Env:
    try:
        text = env_file.read_text()
    except OSError:
        return {}

    return parse_env(text, subst=subst, subst_env=subst_env)


def read_text_or_default(text_file: Path, default: str) -> str:
    if text_file.exists():
        return text_file.read_text().rstrip()

    return default


def parse_duration(duration: str) -> timedelta:
    if duration.strip() == "0":
        return timedelta()

    m = re.fullmatch(DURATION_RE, duration)

    if not m:
        msg = f"invalid duration: {duration!r}"
        raise ValueError(msg)

    weeks, _, days, _, hours, _, minutes, _, seconds, _, milliseconds, _ = m.groups()

    return timedelta(
        weeks=int(weeks or "0"),
        days=int(days or "0"),
        hours=int(hours or "0"),
        minutes=int(minutes or "0"),
        seconds=int(seconds or "0"),
        milliseconds=int(milliseconds or "0"),
    )


Env = dict[str, str]


@dataclass(frozen=True)
class JobResult:
    name: str


@dataclass(frozen=True)
class JobResultCompleted(JobResult):
    exit_status: int
    stdout: str
    stderr: str


@dataclass(frozen=True)
class JobResultError(JobResult):
    message: str
    log: str = ""


@dataclass(frozen=True)
class JobResultLocked(JobResult):
    pass


@dataclass(frozen=True)
class JobResultSkipped(JobResult):
    pass


class Notifier(Protocol):
    def __call__(self, result: JobResult) -> None: ...


class Messages:
    LOG_FILE_TEMPLATE = "  {filename} ({mtime}):\n{contents}"
    LOG_JOB_TEMPLATE = colored("{name}\n", attrs=["bold"]) + "{text}"
    SHOW_ERROR_TEMPLATE = colored("{name}", attrs=["bold"]) + "\n    error: {error}"
    SHOW_EXIT_STATUS = "exit status"
    SHOW_IS_DUE = "due"
    SHOW_IS_RUNNING = "running"
    SHOW_JOB_TITLE_TEMPLATE = colored("{name}", attrs=["bold"])
    SHOW_LAST_START = "last start"
    SHOW_NONE = "none"
    SHOW_NO = "no"
    SHOW_RUN_TIME = "run time"
    SHOW_UNKNOWN = "unknown"
    SHOW_YES = "yes"
    RESULT_COMPLETED_TITLE_FAILURE = "Job {name!r} failed with code {exit_status}"
    RESULT_COMPLETED_TITLE_SUCCESS = "Job {name!r} succeeded"
    RESULT_COMPLETED_TEXT = "stderr:\n{stderr}\nstdout:\n{stdout}"
    RESULT_ERROR_TITLE = "Job {name!r} did not run because of an error"
    RESULT_ERROR_TEXT = "Error message:\n{message}\n\nLog:\n{log}"


class Defaults:
    FILENAME = "script"
    JITTER = ""
    SCHEDULE = "1d"


class EnvVars:
    CONFIG_DIR = "REGULAR_CONFIG_DIR"
    STATE_DIR = "REGULAR_STATE_DIR"


class FileDirNames:
    DEFAULTS = "defaults"
    ENV = "env"
    EXIT_STATUS_FILE = "exit-status"
    FILENAME = "filename"
    IGNORED_JOBS = frozenset({DEFAULTS})
    JITTER = "jitter"
    LAST_START = "last"
    MAX_WORKERS = "max-workers"
    NOTIFY = "notify"
    STDOUT_LOG = "stdout.log"
    STDERR_LOG = "stderr.log"
    QUEUE_DIR = "queue"
    QUEUE_NAME = "queue"
    QUEUE_TEMPLATE = "{time}-{name}"
    RUNNING_LOCK = "lock"
    SCHEDULE = "schedule"


APP_NAME = "regular"
APP_AUTHOR = "dbohdan"
DURATION_RE = " *".join(
    ["", *(rf"(?:(\d+) *({unit}))?" for unit in ("w", "d", "h", "m", "s", "ms")), ""]
)
QUEUE_LOCK_WAIT = 0.01
SMTP_SERVER = "127.0.0.1"
TOLERANCES = [(300, 60), (60, 12), (10, 2)]


@dataclass(frozen=True)
class Config:
    config_dir: Path
    defaults: Job
    max_workers: int | None
    notifiers: list[Notifier]
    state_dir: Path

    @classmethod
    def load_env(
        cls, config_dir: Path, notifiers: list[Notifier], state_dir: Path
    ) -> Self:
        max_workers_file = config_dir / FileDirNames.MAX_WORKERS

        max_workers = (
            int(max_workers_file.read_text().strip())
            if max_workers_file.exists()
            else None
        )

        return cls(
            config_dir=config_dir,
            defaults=Job.load(config_dir / FileDirNames.DEFAULTS),
            max_workers=max_workers,
            notifiers=notifiers,
            state_dir=state_dir,
        )


class Notify(Enum):
    NEVER = "never"
    ALWAYS = "always"
    ON_ERROR = "on error"

    @classmethod
    def from_str(cls, s: str, /) -> Self:
        return cls(s.lower().replace("-", " ", 1) if s else cls.ON_ERROR)

    @classmethod
    def load(cls, notify_file: Path, /) -> Self:
        return cls.from_str(read_text_or_default(notify_file, ""))

    def __str__(self) -> str:
        return self.value


@dataclass(frozen=True)
class LastStart:
    pid: int
    time: float


@dataclass(frozen=True)
class Job:
    dir: Path
    env: Env
    filename: str
    jitter: str
    name: str
    notify: Notify
    queue: str
    schedule: str

    @classmethod
    def load(cls, job_dir: Path, *, name: str = "") -> Self:
        if not name:
            name = cls.job_name(job_dir)

        env = load_env(job_dir / FileDirNames.ENV, subst_env=dict(os.environ))

        filename = read_text_or_default(
            job_dir / FileDirNames.FILENAME, Defaults.FILENAME
        )

        jitter = read_text_or_default(job_dir / FileDirNames.JITTER, Defaults.JITTER)

        notify = Notify.load(job_dir / FileDirNames.NOTIFY)

        queue = read_text_or_default(job_dir / FileDirNames.QUEUE_NAME, name)

        schedule = read_text_or_default(
            job_dir / FileDirNames.SCHEDULE, Defaults.SCHEDULE
        )

        return cls(
            dir=job_dir,
            env=env,
            filename=filename,
            jitter=jitter,
            name=name,
            notify=notify,
            queue=queue,
            schedule=schedule,
        )

    @classmethod
    def job_name(cls, job_dir: Path) -> str:
        return job_dir.name

    def exit_status_file(self, state_dir: Path) -> Path:
        return state_dir / FileDirNames.EXIT_STATUS_FILE

    def exit_status(self, state_dir: Path) -> int | None:
        exit_status_file = self.exit_status_file(state_dir)

        return int(exit_status_file.read_text()) if exit_status_file.exists() else None

    def queue_dir(self, state_dir: Path) -> Path:
        return state_dir / self.queue

    def state_dir(self, state_dir: Path) -> Path:
        return state_dir / self.name

    def last_start_file(self, state_dir: Path) -> Path:
        return self.state_dir(state_dir) / FileDirNames.LAST_START

    def last_start(self, state_dir: Path) -> LastStart | None:
        try:
            last_start_file = self.last_start_file(state_dir)

            return LastStart(
                pid=int(last_start_file.read_text().strip()),
                time=last_start_file.stat().st_mtime,
            )
        except (FileNotFoundError, ValueError):
            return None

    def last_start_update(self, state_dir: Path, pid: int) -> None:
        last_start_file = self.last_start_file(state_dir)

        last_start_file.parent.mkdir(parents=True, exist_ok=True)
        last_start_file.write_text(str(pid))

    def is_due(self, state_dir: Path) -> bool:
        last_start = self.last_start(state_dir)
        min_delay = parse_duration(self.schedule).total_seconds()

        tolerance = 0
        for delay, tol in TOLERANCES:
            if min_delay >= delay:
                tolerance = tol

        return (
            last_start is None or time.time() - last_start.time >= min_delay - tolerance
        )
