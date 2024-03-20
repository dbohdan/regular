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
from datetime import datetime, timedelta, timezone
from pathlib import Path
from typing import TYPE_CHECKING, Any, Callable

import portalocker
from platformdirs import PlatformDirs

from regular import notify
from regular.basis import (
    APP_AUTHOR,
    APP_NAME,
    QUEUE_LOCK_WAIT,
    Config,
    Defaults,
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
    lock_path = job.state_dir / FileDirNames.RUNNING_LOCK
    lock_path.parent.mkdir(parents=True, exist_ok=True)

    try:
        with (
            portalocker.Lock(
                job.state_dir / FileDirNames.RUNNING_LOCK,
                fail_when_locked=True,
                mode="a",
            ),
            run_in_queue(job.queue_dir, job.name),
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

    if not force and not job.is_due():
        return JobResultSkipped(name=job.name)

    jitter_seconds = parse_duration(job.jitter).total_seconds()
    time.sleep(random.random() * jitter_seconds)  # noqa: S311

    with suppress(FileNotFoundError):
        job.exit_status_file.unlink()

    job.last_start_update(os.getpid())

    with job.stdout_file.open("w") as f_out, job.stderr_file.open("w") as f_err:
        completed = sp.run(
            [job.dir / job.filename],
            check=False,
            cwd=job.dir,
            env=os.environ | config.defaults.env | job.env,
            stdout=f_out,
            stderr=f_err,
        )

    job.exit_status_file.write_text(str(completed.returncode))

    return JobResultCompleted(
        name=job.name,
        exit_status=completed.returncode,
        stdout=job.stdout(),
        stderr=job.stderr(),
    )


def available_jobs(config_dir: Path, /) -> list[Path]:
    return [
        item
        for item in sorted(config_dir.iterdir())
        if item.is_dir() and item.name not in FileDirNames.IGNORED_JOBS
    ]


def select_jobs(
    config_dir: Path, /, job_name_filter: list[str] | None = None
) -> list[Path]:
    return (
        available_jobs(config_dir)
        if job_name_filter is None
        else [config_dir / job_name for job_name in job_name_filter]
    )


class JSONEncoder(json.JSONEncoder):
    def default(self, o) -> str:
        if isinstance(o, Notify):
            return o.value

        if isinstance(o, timedelta):
            return str(o)

        return super().default(o)


def jsonize(data: Any) -> str:
    return json.dumps(data, cls=JSONEncoder, ensure_ascii=False)


def cli_command_list(
    config: Config,
    *,
    json_lines: bool = False,
    print_func: Callable[[str], None] = print,
) -> None:
    for job_dir in available_jobs(config.config_root):
        name = Job.job_name(job_dir)
        print_func(jsonize(name) if json_lines else name)


def local_datetime(timestamp: float) -> datetime:
    return datetime.fromtimestamp(timestamp, tz=timezone.utc).astimezone()


def cli_command_log(
    config: Config,
    *,
    job_name_filter: list[str] | None = None,
    json_lines: bool = False,
    print_func: Callable[[str], None] = print,
) -> None:
    job_dirs = select_jobs(config.config_root, job_name_filter)

    for job_dir in job_dirs:
        job = Job.load(job_dir, config.state_root)

        record = {"name": job.name, "logs": []}

        for log_file in (job.stdout_file, job.stderr_file):
            with suppress(FileNotFoundError):
                record["logs"].append(
                    {
                        "filename": log_file.name,
                        "modified": show_value(
                            local_datetime(log_file.stat().st_mtime)
                        ),
                        "contents": log_file.read_text(),
                    }
                )

        text = "\n".join(
            Messages.LOG_FILE_TEMPLATE.format(
                filename=log["filename"],
                mtime=log["modified"],
                contents=log["contents"],
            )
            for log in record["logs"]
        )

        print_func(
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
    config: Config, *, force: bool = False, job_name_filter: list[str] | None = None
) -> list[JobResult]:
    def run_job_with_config(job_dir: Path) -> JobResult:
        try:
            job = Job.load(job_dir, config.state_root)
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

    job_dirs_to_run = select_jobs(config.config_root, job_name_filter)

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


def show_job(
    job: Job,
    *,
    log_lines: int,
    json: bool = False,
) -> str:
    indent = " " * 8

    record = dict(vars(job))

    if record["env"]:
        record["env"] = (
            load_env(job.dir / "env", subst=False)
            if json
            else "\n" + textwrap.indent((job.dir / "env").read_text(), indent)
        )

    if not json:
        del record["name"]
        del record["state_root"]

    last_start = job.last_start()

    if last_start:
        record[Messages.SHOW_LAST_START] = local_datetime(last_start)

        job_is_running = is_running(job.state_dir)
        record[Messages.SHOW_IS_RUNNING] = job_is_running

        try:
            if job_is_running:
                run_time = time.time() - last_start
            else:
                run_time = job.exit_status_file.stat().st_mtime - last_start

            record[Messages.SHOW_RUN_TIME] = timedelta(seconds=round(run_time))
        except FileNotFoundError:
            record[Messages.SHOW_RUN_TIME] = Messages.SHOW_UNKNOWN
    else:
        record[Messages.SHOW_LAST_START] = Messages.SHOW_UNKNOWN
        record[Messages.SHOW_IS_RUNNING] = Messages.SHOW_UNKNOWN
        record[Messages.SHOW_RUN_TIME] = Messages.SHOW_UNKNOWN

    exit_status = job.exit_status()
    record[Messages.SHOW_EXIT_STATUS] = (
        Messages.SHOW_UNKNOWN if exit_status is None else exit_status
    )

    if log_lines != 0:
        for load_log in (job.stdout, job.stderr):
            with suppress(FileNotFoundError):
                log = load_log()
                tail = log.lines[-log_lines:] if log_lines > 0 else log.lines
                record[Path(log.filename).stem] = (
                    tail if json else textwrap.indent("\n".join(["", *tail]), indent)
                )

    record[Messages.SHOW_IS_DUE] = job.is_due()

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


def is_running(job_state_dir: Path, /) -> bool:
    try:
        with portalocker.Lock(
            job_state_dir / FileDirNames.RUNNING_LOCK,
            fail_when_locked=True,
            mode="r",
        ):
            return False
    except portalocker.AlreadyLocked:
        return True


def cli_command_show(
    config: Config,
    *,
    job_name_filter: list[str] | None = None,
    json_lines: bool = False,
    log_lines: int = -1,
    print_func: Callable[[str], None] = print,
) -> None:
    job_dirs = select_jobs(config.config_root, job_name_filter)

    entries = []
    for job_dir in job_dirs:
        try:
            job = Job.load(job_dir, config.state_root)
            entries.append(show_job(job, json=json_lines, log_lines=log_lines))
        except Exception as e:  # noqa: BLE001, PERF203
            error_info = {"name": Job.job_name(job_dir), "error": str(e)}

            entries.append(
                jsonize(error_info)
                if json_lines
                else Messages.SHOW_ERROR_TEMPLATE.format(**error_info)
            )

    for i, entry in enumerate(entries):
        print_func(entry if json_lines or i == len(entries) - 1 else f"{entry}\n")


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
    run_subparsers = run_parser.add_subparsers(required=True, title="subcommands")

    run_due_parser = run_subparsers.add_parser("due", help="run jobs that are due")
    run_due_parser.set_defaults(force=False)
    run_due_parser.add_argument("jobs", metavar="job", nargs="*", help="job to run")
    run_due_parser.add_argument(
        "-a",
        "--all",
        action="store_true",
        help="run all jobs",
    )

    run_now_parser = run_subparsers.add_parser("now", help="run jobs immediately")
    run_now_parser.set_defaults(force=True)
    run_now_parser.add_argument("jobs", metavar="job", nargs="*", help="job to run")
    run_now_parser.add_argument(
        "-a",
        "--all",
        action="store_true",
        help="run all jobs",
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
    show_parser.add_argument(
        "-l",
        "--log-lines",
        default=Defaults.LOG_LINES,
        help="how many log files to show",
        type=int,
    )

    return parser.parse_args()


def main() -> None:
    args = cli()

    dirs = PlatformDirs(APP_NAME, APP_AUTHOR)

    config_root = Path(os.environ.get(EnvVars.CONFIG_ROOT, dirs.user_config_path))
    state_root = Path(os.environ.get(EnvVars.STATE_ROOT, dirs.user_state_path))

    for directory in (config_root, state_root):
        directory.mkdir(parents=True, exist_ok=True)

    config = Config.load_env(config_root, [notify.notify_user_by_email], state_root)

    if args.subcommand == "list":
        cli_command_list(config, json_lines=args.json_lines)
    elif args.subcommand == "log":
        cli_command_log(
            config, json_lines=args.json_lines, job_name_filter=args.jobs or None
        )
    elif args.subcommand == "run":
        run_session(
            config, force=args.force, job_name_filter=None if args.all else args.jobs
        )
    elif args.subcommand == "show":
        cli_command_show(
            config,
            job_name_filter=args.jobs or None,
            json_lines=args.json_lines,
            log_lines=args.log_lines,
        )
    else:
        msg = "invalid command"
        raise ValueError(msg)


if __name__ == "__main__":
    main()
