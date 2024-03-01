from __future__ import annotations

import concurrent.futures
from pathlib import Path

from regular import Config, FileDirNames, JobResult, JobResultLocked, JobResultRan, JobResultTooEarly
from regular import run_job, run_session

TEST_DIR = Path(__file__).parent


def job_path(configs_subdir: str, job_name: str) -> Path:
    return TEST_DIR / "configs" / configs_subdir / FileDirNames.JOBS / job_name


def config_and_log(
    configs_subdir: str, state_dir: Path
) -> tuple[Config, list[JobResult]]:
    result_log = []

    def test_notify(result: JobResult) -> None:
        result_log.append(result)

    return (
        Config(
            config_dir=TEST_DIR / "configs" / configs_subdir,
            notifiers=[test_notify],
            state_dir=state_dir,
        ),
        result_log,
    )


class TestRegular:
    def test_session_basic(self, tmp_path) -> None:
        config, _ = config_and_log("basic", tmp_path)

        results = run_session(config)

        assert results == [
            JobResultRan(name="bar", exit_status=0, stdout="bar\n", stderr=""),
            JobResultRan(name="foo", exit_status=0, stdout="foo\n", stderr=""),
        ]

        results = run_session(config)

        assert results == [
            JobResultTooEarly(name="bar"),
            JobResultTooEarly(name="foo"),
        ]

    def test_session_failure(self, tmp_path) -> None:
        config, _ = config_and_log("failure", tmp_path)

        results = run_session(config)

        assert results == [
            JobResultRan(
                name="failure", exit_status=99, stdout="failure\n", stderr="nope\n"
            ),
        ]

    def test_session_notify(self, tmp_path) -> None:
        config, log = config_and_log("notify", tmp_path)

        results = run_session(config)

        assert results == [
            JobResultRan(
                name="always-notify", exit_status=0, stdout="always\n", stderr=""
            ),
            JobResultRan(
                name="never-notify",
                exit_status=99,
                stdout="You should not see this message.\n",
                stderr="",
            ),
        ]

        assert log == [
            JobResultRan(
                name="always-notify", exit_status=0, stdout="always\n", stderr=""
            ),
        ]

    def test_session_wait(self, tmp_path) -> None:
        config, _ = config_and_log("wait", tmp_path)
        wait_job = job_path("wait", "wait")

        def run_wait_job(_: int) -> JobResult:
            return run_job(wait_job, config=config)

        with concurrent.futures.ThreadPoolExecutor(max_workers=2) as executor:
            results = executor.map(run_wait_job, range(2))

        assert list(results) == [JobResultRan(name='wait', exit_status=0, stdout='', stderr=''), JobResultLocked(name='wait')]
