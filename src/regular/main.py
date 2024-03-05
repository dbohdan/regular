from __future__ import annotations

import argparse
import os
import random
import subprocess as sp
import sys
import textwrap
import time
import traceback
from concurrent.futures import ThreadPoolExecutor
from contextlib import contextmanager, suppress
from datetime import datetime, timezone
from pathlib import Path
from typing import TYPE_CHECKING, Any

import portalocker
from platformdirs import PlatformDirs
from termcolor import colored

from regular import notify
from regular.basis import (
    APP_AUTHOR,
    APP_NAME,
    QUEUE_LOCK_WAIT,
    Config,
    EnvVars,
    FileDirNames,
    Job,
    JobResult,
    JobResultCompleted,
    JobResultError,
    JobResultLocked,
    JobResultSkipped,
    Messages,
    parse_duration,
)

if TYPE_CHECKING:
    from collections.abc import Iterator


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
        with (
            portalocker.Lock(
                config.state_dir / job.name / FileDirNames.RUNNING_LOCK,
                fail_when_locked=True,
                mode="a",
            ),
            run_in_queue(
                config.state_dir / job.queue / FileDirNames.QUEUE_DIR, job.name
            ),
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
        env=os.environ | config.env | job.env,
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
        print(output)  # noqa: T201


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

        notify.notify_user_if_necessary(job_dir, config=config, result=result)

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

    return str(value).rstrip()


def show_job(job: Job, config: Config) -> str:
    d = {k: v for k, v in vars(job).items() if k not in ("name")}

    if d["env"]:
        d["env"] = "\n" + textwrap.indent((job.dir / "env").read_text(), "        ")

    last_run = job.last_run(config.state_dir)
    d[Messages.SHOW_LAST_RUN] = (
        datetime.fromtimestamp(last_run, tz=timezone.utc).astimezone()
        if last_run
        else Messages.SHOW_NEVER
    )

    d[Messages.SHOW_SHOULD_RUN] = job.should_run(config.state_dir)

    lines = [colored(job.name, attrs=["bold"])]

    for k, v in d.items():
        lines.append(f"    {k.capitalize()}: {show_value(v)}")

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

    config_dir = Path(os.environ.get(EnvVars.CONFIG_DIR, dirs.user_config_path))
    state_dir = Path(os.environ.get(EnvVars.STATE_DIR, dirs.user_state_path))

    for directory in (config_dir, state_dir):
        directory.mkdir(parents=True, exist_ok=True)

    config = Config.load_env(config_dir, [notify.notify_user_by_email], state_dir)

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
