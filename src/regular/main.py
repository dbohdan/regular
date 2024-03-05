from __future__ import annotations

import argparse
import getpass
import operator
import random
import re
import smtplib
import subprocess as sp
import sys
import time
import traceback
from concurrent.futures import ThreadPoolExecutor
from contextlib import contextmanager, suppress
from dataclasses import dataclass
from datetime import datetime, timedelta, timezone
from email.message import EmailMessage
from functools import reduce
from os import environ
from pathlib import Path
from typing import TYPE_CHECKING, Any, Protocol

import portalocker
from dotenv import dotenv_values
from platformdirs import PlatformDirs
from termcolor import colored
from typing_extensions import Self

if TYPE_CHECKING:
    from collections.abc import Iterator


class Defaults:
    FILENAME = "script"
    JITTER = ""
    SCHEDULE = "1d"


class EnvVars:
    CONFIG_DIR = "REGULAR_CONFIG_DIR"
    STATE_DIR = "REGULAR_STATE_DIR"


class FileDirNames:
    ALWAYS_NOTIFY = "always-notify"
    ENV = "env"
    FILENAME = "filename"
    JITTER = "jitter"
    LAST_RUN = "last"
    MAX_WORKERS = "max-workers"
    NEVER_NOTIFY = "never-notify"
    QUEUE_DIR = "queue"
    QUEUE_NAME = "queue"
    QUEUE_TEMPLATE = "{time}-{name}"
    RUNNING_LOCK = "lock"
    SCHEDULE = "schedule"


class Messages:
    SHOW_ERROR_TEMPLATE = colored("{name}", attrs=["bold"]) + "\n    error: {message}"
    SHOW_LAST_RUN = "last ran"
    SHOW_LAST_RUN_NEVER = "never"
    SHOW_NEVER = "never"
    SHOW_NONE = "none"
    SHOW_NO = "no"
    SHOW_SHOULD_RUN = "would run now"
    SHOW_YES = "yes"
    RESULT_COMPLETED_TITLE_FAILURE = "Job {name!r} failed with code {exit_status}"
    RESULT_COMPLETED_TITLE_SUCCESS = "Job {name!r} succeeded"
    RESULT_COMPLETED_TEXT = "stderr:\n{stderr}\nstdout:\n{stdout}"
    RESULT_ERROR_TITLE = "Job {name!r} did not run because of an error"
    RESULT_ERROR_TEXT = "Error message:\n{message}\n\nLog:\n{log}"


APP_NAME = "regular"
APP_AUTHOR = "dbohdan"
DURATION_RE = " *".join(
    ["", *(rf"(?:(\d+) *({unit}))?" for unit in ("w", "d", "h", "m", "s", "ms")), ""]
)
QUEUE_LOCK_WAIT = 0.01
SMTP_SERVER = "127.0.0.1"


@dataclass(frozen=True)
class Config:
    config_dir: Path
    env: Env
    notifiers: list[Notifier]
    state_dir: Path

    @classmethod
    def load_env(
        cls, config_dir: Path, notifiers: list[Notifier], state_dir: Path
    ) -> Self:
        return cls(
            config_dir=config_dir,
            env=load_env(config_dir / FileDirNames.ENV),
            notifiers=notifiers,
            state_dir=state_dir,
        )


Env = dict[str, str]


@dataclass(frozen=True)
class Job:
    dir: Path
    env: Env
    filename: str
    jitter: str
    name: str
    schedule: str

    @classmethod
    def load(cls, job_dir: Path, *, name: str = "") -> Self:
        env = load_env(job_dir / FileDirNames.ENV)

        filename = read_text_or_default(
            job_dir / FileDirNames.FILENAME, Defaults.FILENAME
        )
        jitter = read_text_or_default(job_dir / FileDirNames.JITTER, Defaults.JITTER)

        schedule = read_text_or_default(
            job_dir / FileDirNames.SCHEDULE, Defaults.SCHEDULE
        )

        return cls(
            dir=job_dir,
            env=env,
            filename=filename,
            jitter=jitter,
            name=name if name else cls.job_name(job_dir),
            schedule=schedule,
        )

    @classmethod
    def job_name(cls, job_dir: Path) -> str:
        return job_dir.name

    def last_run_file(self, state_dir: Path) -> Path:
        return state_dir / self.name / FileDirNames.LAST_RUN

    def last_run(self, state_dir: Path) -> float | None:
        last_run_file = self.last_run_file(state_dir)
        return last_run_file.stat().st_mtime if last_run_file.exists() else None

    def should_run(self, state_dir: Path) -> bool:
        last_run = self.last_run(state_dir)
        min_delay = parse_duration(self.schedule).total_seconds()

        return last_run is None or time.time() - last_run >= min_delay


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


def load_env(*env_files: Path) -> Env:
    return reduce(
        operator.or_,
        [*(dotenv_values(env_file) for env_file in env_files), environ],
    )


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


def notify_user(result: JobResult, *, config: Config) -> None:
    for notifier in config.notifiers:
        notifier(result)


def result_message_completed(result: JobResultCompleted) -> tuple[str, str]:
    title_template = (
        Messages.RESULT_COMPLETED_TITLE_SUCCESS
        if result.exit_status == 0
        else Messages.RESULT_COMPLETED_TITLE_FAILURE
    )

    title = title_template.format(name=result.name, exit_status=result.exit_status)

    content = Messages.RESULT_COMPLETED_TEXT.format(
        name=result.name,
        exit_status=result.exit_status,
        stdout=result.stdout,
        stderr=result.stderr,
    )

    return (title, content)


def result_message_error(result: JobResultError) -> tuple[str, str]:
    title = Messages.RESULT_ERROR_TITLE.format(
        name=result.name,
    )

    text = Messages.RESULT_ERROR_TEXT.format(
        name=result.name,
        log=result.log,
        message=result.message,
    )

    return (title, text)


def email_message(subject: str, text: str) -> EmailMessage:
    msg = EmailMessage()

    msg["Subject"] = subject
    msg["From"] = APP_NAME
    msg["To"] = getpass.getuser()

    msg.set_content(text)

    return msg


def notify_user_by_email(result: JobResult) -> None:
    if isinstance(result, JobResultCompleted):
        title, text = result_message_completed(result)
    if isinstance(result, JobResultError):
        title, text = result_message_error(result)
    else:
        return

    msg = email_message(title, text)
    smtp = smtplib.SMTP(SMTP_SERVER)
    smtp.send_message(msg)
    smtp.quit()


def notify_user_if_necessary(
    job_dir: Path, *, config: Config, result: JobResult
) -> None:
    if not (job_dir / FileDirNames.NEVER_NOTIFY).exists() and (
        (isinstance(result, JobResultCompleted) and result.exit_status != 0)
        or isinstance(result, JobResultError)
        or (job_dir / FileDirNames.ALWAYS_NOTIFY).exists()
    ):
        notify_user(
            result,
            config=config,
        )


@contextmanager
def run_in_queue(queue_dir: Path, /, name: str) -> Iterator[None]:
    """
    The algorithm is based on <https://github.com/leahneukirchen/nq>.
    The bugs are all ours.
    """
    queue_dir.mkdir(parents=True, exist_ok=True)

    # The length of the `time` field will remain constant until late 2286,
    # enabling simple sorting.
    filename = FileDirNames.QUEUE_TEMPLATE.format(
        name=name, time=f"{time.time_ns() // 1_000_000:013d}"
    )
    my_lock_file_hidden = queue_dir / f".{filename}"
    my_lock_file = queue_dir / filename

    with my_lock_file_hidden.open(mode="w") as my_f:
        try:
            portalocker.lock(my_f, portalocker.LOCK_EX)
            my_lock_file_hidden.rename(my_lock_file)

            time.sleep(QUEUE_LOCK_WAIT)

            seen_locks = set()
            while True:
                locks = {
                    item
                    for item in queue_dir.iterdir()
                    if item not in seen_locks
                    and not item.name.startswith(".")
                    and item.name < my_lock_file.name
                }

                if not locks:
                    break

                for lock_file in sorted(locks):
                    # If the lock file has been removed,
                    # that is fine by us.
                    # Ignore it.
                    # If the lock file exists,
                    # try to lock it ourselves in order to wait
                    # until others release it.
                    with suppress(FileNotFoundError):  # noqa: SIM117
                        with lock_file.open() as f:
                            portalocker.lock(f, flags=portalocker.LOCK_SH)

                seen_locks = seen_locks | locks

            yield
        finally:
            with suppress(FileNotFoundError):
                my_lock_file_hidden.unlink()
            with suppress(FileNotFoundError):
                my_lock_file.unlink()


def run_job(job: Job, config: Config) -> JobResult:
    lock_path = config.state_dir / job.name / FileDirNames.RUNNING_LOCK
    lock_path.parent.mkdir(parents=True, exist_ok=True)

    try:
        with portalocker.Lock(
            config.state_dir / job.name / FileDirNames.RUNNING_LOCK,
            fail_when_locked=True,
            mode="a",
        ):
            queue_name = read_text_or_default(
                job.dir / FileDirNames.QUEUE_NAME, job.name
            )

            with run_in_queue(
                config.state_dir / queue_name / FileDirNames.QUEUE_DIR, job.name
            ):
                return run_job_no_lock_no_queue(job, config)
    except portalocker.AlreadyLocked:
        return JobResultLocked(name=job.name)


def run_job_no_lock_no_queue(job: Job, config: Config) -> JobResult:
    if not job.should_run(config.state_dir):
        return JobResultSkipped(name=job.name)

    jitter_seconds = parse_duration(job.jitter).total_seconds()
    time.sleep(random.random() * jitter_seconds)  # noqa: S311

    last_run_file = job.last_run_file(config.state_dir)
    last_run_file.parent.mkdir(parents=True, exist_ok=True)
    last_run_file.touch()

    completed = sp.run(
        [job.dir / job.filename],
        capture_output=True,
        check=False,
        cwd=job.dir,
        env=environ | config.env | job.env,
        text=True,
    )

    return JobResultCompleted(
        name=job.name,
        exit_status=completed.returncode,
        stdout=completed.stdout,
        stderr=completed.stderr,
    )


def available_jobs(directory: Path, /) -> list[Path]:
    return [item for item in sorted(directory.iterdir()) if item.is_dir()]


def list_jobs(config: Config) -> None:
    output = "\n".join(
            Job.job_name(job_dir) for job_dir in available_jobs(config.config_dir)
        )

    if output:
        print(output) # noqa: T201


def run_session(config: Config) -> list[JobResult]:
    def run_job_with_config(job_dir: Path) -> JobResult:
        try:
            job = Job.load(job_dir)
            result = run_job(job, config)
        except Exception as e:  # noqa: BLE001
            tb = sys.exc_info()[-1]
            extracted = traceback.extract_tb(tb)
            result = JobResultError(
                name=Job.job_name(job_dir),
                message=str(e),
                log="\n".join(traceback.format_list(extracted)),
            )

        notify_user_if_necessary(job_dir, config=config, result=result)

        return result

    max_workers_file = config.config_dir / FileDirNames.MAX_WORKERS
    max_workers = (
        int(max_workers_file.read_text().strip()) if max_workers_file.exists() else None
    )

    with ThreadPoolExecutor(max_workers=max_workers) as executor:
        return list(
            executor.map(run_job_with_config, available_jobs(config.config_dir))
        )


def show_value(value: Any) -> str:
    if isinstance(value, bool):
        return Messages.SHOW_YES if value else Messages.SHOW_NO

    if not value:
        return Messages.SHOW_NONE

    return str(value).strip()


def show_job(job: Job, config: Config) -> str:
    d = {k: v for k, v in vars(job).items() if k not in ("env", "name")}

    last_run = job.last_run(config.state_dir)
    d[Messages.SHOW_LAST_RUN] = (
        datetime.fromtimestamp(last_run, tz=timezone.utc).astimezone()
        if last_run
        else Messages.SHOW_NEVER
    )

    d[Messages.SHOW_SHOULD_RUN] = job.should_run(config.state_dir)

    lines = [colored(job.name, attrs=["bold"])]

    for k, v in d.items():
        lines.append(f"    {k}: {show_value(v)}")

    return "\n".join(lines)


def show_jobs(config: Config) -> None:
    job_dirs = available_jobs(config.config_dir)

    entries = []
    for job_dir in job_dirs:
        try:
            job = Job.load(job_dir)
            entries.append(show_job(job, config))
        except Exception as e:  # noqa: BLE001, PERF203
            entries.append(
                Messages.SHOW_ERROR_TEMPLATE.format(
                    name=Job.job_name(job_dir), message=e
                )
            )

    if entries:
        print("\n\n".join(entries))  # noqa: T201


def cli() -> argparse.Namespace:
    parser = argparse.ArgumentParser(
        description="Run jobs on a regular basis.",
    )
    subparsers = parser.add_subparsers(required=True, title="commands")

    list_parser = subparsers.add_parser("list", help="list jobs")
    list_parser.set_defaults(subcommand="list")

    run_parser = subparsers.add_parser("run", help="run jobs")
    run_parser.set_defaults(subcommand="run")

    show_parser = subparsers.add_parser("show", help="show job information")
    show_parser.set_defaults(subcommand="show")

    return parser.parse_args()


def main() -> None:
    args = cli()

    dirs = PlatformDirs(APP_NAME, APP_AUTHOR)

    config_dir = Path(environ.get(EnvVars.CONFIG_DIR, dirs.user_config_path))
    state_dir = Path(environ.get(EnvVars.STATE_DIR, dirs.user_state_path))

    config = Config.load_env(config_dir, [notify_user_by_email], state_dir)

    if args.subcommand == "list":
        list_jobs(config)
    elif args.subcommand == "run":
        run_session(config)
    elif args.subcommand == "show":
        show_jobs(config)
    else:
        msg = "invalid command"
        raise ValueError(msg)


if __name__ == "__main__":
    main()
