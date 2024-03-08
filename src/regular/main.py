from __future__ import annotations

import argparse
import json
import os
import random
import subprocess as sp
import sys
import textwrap
import time
import traceback
from collections.abc import Sized
from concurrent.futures import ThreadPoolExecutor
from contextlib import contextmanager, suppress
from datetime import datetime, timezone
from pathlib import Path
from typing import TYPE_CHECKING, Any

import portalocker
from platformdirs import PlatformDirs

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
    Notify,
    load_env,
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


def run_job(job: Job, config: Config, *, force: bool = False) -> JobResult:
    lock_path = job.state_dir(config.state_dir) / FileDirNames.RUNNING_LOCK
    lock_path.parent.mkdir(parents=True, exist_ok=True)

    try:
        with (
            portalocker.Lock(
                job.state_dir(config.state_dir) / FileDirNames.RUNNING_LOCK,
                fail_when_locked=True,
                mode="a",
            ),
            run_in_queue(
                job.queue_dir(config.state_dir) / FileDirNames.QUEUE_DIR, job.name
            ),
        ):
            return run_job_no_lock_no_queue(job, config, force=force)
    except portalocker.AlreadyLocked:
        return JobResultLocked(name=job.name)


def run_job_no_lock_no_queue(
    job: Job, config: Config, *, force: bool = False
) -> JobResult:
    if not job.dir.exists():
        msg = f"no job directory: {str(job.dir)!r}"
        raise FileNotFoundError(msg)

    if not force and not job.due(config.state_dir):
        return JobResultSkipped(name=job.name)

    jitter_seconds = parse_duration(job.jitter).total_seconds()
    time.sleep(random.random() * jitter_seconds)  # noqa: S311

    exit_status_file = job.exit_status_file(config.state_dir)
    with suppress(FileNotFoundError):
        exit_status_file.unlink()

    last_start_file = job.last_start_file(config.state_dir)
    last_start_file.parent.mkdir(parents=True, exist_ok=True)
    last_start_file.touch()

    stdout_log = job.state_dir(config.state_dir) / FileDirNames.STDOUT_LOG
    stderr_log = job.state_dir(config.state_dir) / FileDirNames.STDERR_LOG

    with stdout_log.open("w") as f_out, stderr_log.open("w") as f_err:
        completed = sp.run(
            [job.dir / job.filename],
            check=False,
            cwd=job.dir,
            env=os.environ | config.defaults.env | job.env,
            stdout=f_out,
            stderr=f_err,
        )

    exit_status_file.write_text(str(completed.returncode))

    return JobResultCompleted(
        name=job.name,
        exit_status=completed.returncode,
        stdout=stdout_log.read_text(),
        stderr=stderr_log.read_text(),
    )


def available_jobs(config_dir: Path, /) -> list[Path]:
    return [
        item
        for item in sorted(config_dir.iterdir())
        if item.is_dir() and item.name not in FileDirNames.IGNORED_JOBS
    ]


def select_jobs(config_dir: Path, /, job_names: list[str] | None = None) -> list[Path]:
    return (
        [config_dir / job_name for job_name in job_names]
        if job_names
        else available_jobs(config_dir)
    )


class JSONEncoder(json.JSONEncoder):
    def default(self, o) -> str:
        if isinstance(o, Notify):
            return o.value

        return super().default(o)


def jsonize(data: Any) -> str:
    return json.dumps(data, cls=JSONEncoder, ensure_ascii=False)


def cli_command_list(config: Config, *, json_lines: bool = False) -> None:
    for job_dir in available_jobs(config.config_dir):
        name = Job.job_name(job_dir)
        print(jsonize(name) if json_lines else name)  # noqa: T201


def local_datetime(timestamp: float) -> datetime:
    return datetime.fromtimestamp(timestamp, tz=timezone.utc).astimezone()


def cli_command_log(
    config: Config, *, json_lines: bool = False, job_names: list[str] | None = None
) -> None:
    job_dirs = select_jobs(config.config_dir, job_names)

    for job_dir in job_dirs:
        job = Job.load(job_dir)
        state_dir = job.state_dir(config.state_dir)

        record = {"name": job.name, "logs": []}

        for filename in (FileDirNames.STDOUT_LOG, FileDirNames.STDERR_LOG):
            with suppress(FileNotFoundError):
                log_file = state_dir / filename

                record["logs"].append(
                    {
                        "filename": filename,
                        "modified": show_value(
                            local_datetime(log_file.stat().st_mtime)
                        ),
                        "contents": log_file.read_text(),
                    }
                )

        text = "\n".join(
            Messages.LOG_FILE_TEMPLATE.format(
                filename=log["filename"],
                timestamp=log["modified"],
                contents=log["contents"],
            )
            for log in record["logs"]
        )

        print(  # noqa: T201
            jsonize(record)
            if json_lines
            else (
                Messages.LOG_JOB_TEMPLATE.format(
                    name=record["name"],
                    text=text,
                )
            )
        )


def run_session(
    config: Config, *, force: bool = False, job_names: list[str] | None = None
) -> list[JobResult]:
    def run_job_with_config(job_dir: Path) -> JobResult:
        try:
            job = Job.load(job_dir)
            result = run_job(job, config, force=force)
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

    job_dirs_to_run = select_jobs(config.config_dir, job_names)

    with ThreadPoolExecutor(max_workers=config.max_workers) as executor:
        return list(executor.map(run_job_with_config, job_dirs_to_run))


def show_value(value: Any) -> str:
    if value is None or (isinstance(value, Sized) and not value):
        return Messages.SHOW_NONE

    if isinstance(value, bool):
        return Messages.SHOW_YES if value else Messages.SHOW_NO

    if isinstance(value, datetime):
        return value.isoformat(sep=" ", timespec="seconds")

    return str(value).rstrip()


def show_job(job: Job, config: Config, *, json: bool = False) -> str:
    record = dict(vars(job))

    if record["env"]:
        record["env"] = (
            load_env(job.dir / "env", subst=False)
            if json
            else "\n" + textwrap.indent((job.dir / "env").read_text(), "        ")
        )

    if not json:
        del record["name"]

    last_start = job.last_start(config.state_dir)

    record[Messages.SHOW_LAST_START] = (
        local_datetime(last_start) if last_start else Messages.SHOW_NEVER
    )

    record[Messages.SHOW_EXIT_STATUS] = job.exit_status(config.state_dir)

    record[Messages.SHOW_DUE] = job.due(config.state_dir)

    if json:
        return jsonize(
            {
                k.replace(" ", "_"): show_value(v)
                if isinstance(v, (datetime, Path))
                else v
                for k, v in record.items()
            }
        )

    lines = [Messages.SHOW_JOB_TITLE_TEMPLATE.format(name=job.name)]

    for k, v in record.items():
        lines.append(f"    {k.replace('_', ' ')}: {show_value(v)}")

    return "\n".join(lines)


def cli_command_show(
    config: Config, *, json_lines: bool = False, job_names: list[str] | None = None
) -> None:
    job_dirs = select_jobs(config.config_dir, job_names)

    entries = []
    for job_dir in job_dirs:
        try:
            job = Job.load(job_dir)
            entries.append(show_job(job, config, json=json_lines))
        except Exception as e:  # noqa: BLE001, PERF203
            error_info = {"name": Job.job_name(job_dir), "error": str(e)}

            entries.append(
                jsonize(error_info)
                if json_lines
                else Messages.SHOW_ERROR_TEMPLATE.format(**error_info)
            )

    if entries:
        print(("\n" if json_lines else "\n\n").join(entries))  # noqa: T201


def cli() -> argparse.Namespace:
    parser = argparse.ArgumentParser(
        description="Run jobs on a regular basis.",
    )
    subparsers = parser.add_subparsers(required=True, title="commands")

    list_parser = subparsers.add_parser("list", help="list jobs")
    list_parser.set_defaults(subcommand="list")

    list_parser.add_argument(
        "-j",
        "--json-lines",
        action="store_true",
        help="output JSON Lines",
    )

    log_parser = subparsers.add_parser("log", help="show last log for job")
    log_parser.set_defaults(subcommand="log")

    log_parser.add_argument(
        "jobs", metavar="job", nargs="*", help="job for which to show logs"
    )

    log_parser.add_argument(
        "-j",
        "--json-lines",
        action="store_true",
        help="output JSON Lines",
    )

    run_parser = subparsers.add_parser("run", help="run jobs")
    run_parser.set_defaults(subcommand="run")

    run_parser.add_argument("jobs", metavar="job", nargs="*", help="job to run")

    run_parser.add_argument(
        "-f",
        "--force",
        action="store_true",
        help="run jobs regardless of when they last ran",
    )

    show_parser = subparsers.add_parser("show", help="show job information")
    show_parser.set_defaults(subcommand="show")

    show_parser.add_argument("jobs", metavar="job", nargs="*", help="job to show")

    show_parser.add_argument(
        "-j",
        "--json-lines",
        action="store_true",
        help="output JSON Lines",
    )

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
        cli_command_list(config, json_lines=args.json_lines)
    elif args.subcommand == "log":
        cli_command_log(config, json_lines=args.json_lines, job_names=args.jobs)
    elif args.subcommand == "run":
        run_session(config, force=args.force, job_names=args.jobs)
    elif args.subcommand == "show":
        cli_command_show(config, json_lines=args.json_lines, job_names=args.jobs)
    else:
        msg = "invalid command"
        raise ValueError(msg)


if __name__ == "__main__":
    main()
