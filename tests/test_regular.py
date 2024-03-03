from __future__ import annotations

import time
from concurrent.futures import ThreadPoolExecutor
from pathlib import Path

from regular import (
    Config,
    JobResult,
    JobResultCompleted,
    JobResultLocked,
    JobResultSkipped,
    run_job,
    run_session,
)

TEST_DIR = Path(__file__).parent


def job_path(configs_subdir: str, job_name: str) -> Path:
    return TEST_DIR / "configs" / configs_subdir / job_name


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
            JobResultCompleted(name="bar", exit_status=0, stdout="bar\n", stderr=""),
            JobResultCompleted(name="foo", exit_status=0, stdout="foo\n", stderr=""),
        ]

        results = run_session(config)

        assert results == [
            JobResultSkipped(name="bar"),
            JobResultSkipped(name="foo"),
        ]

    def test_session_cwd(self, tmp_path) -> None:
        config, _ = config_and_log("cwd", tmp_path)

        results = run_session(config)

        assert isinstance(results[0], JobResultCompleted)
        assert results[0].stdout.strip().endswith("configs/cwd/cwd")

    def test_session_concurrent(self, tmp_path) -> None:
        config, _ = config_and_log("concurrent", tmp_path)
        start_time = time.time()
        results = run_session(config)
        duration = time.time() - start_time

        assert len(results) == 10
        assert 2 < duration < 3

    def test_session_env(self, tmp_path) -> None:
        config, _ = config_and_log("env", tmp_path)

        results = run_session(config)

        assert results == [
            JobResultCompleted(
                name="greet", exit_status=0, stdout="Hello, world!\n", stderr=""
            ),
            JobResultCompleted(
                name="override",
                exit_status=0,
                stdout="Message overridden.\n",
                stderr="",
            ),
        ]

    def test_session_failure(self, tmp_path) -> None:
        config, _ = config_and_log("failure", tmp_path)

        results = run_session(config)

        assert results == [
            JobResultCompleted(
                name="failure", exit_status=99, stdout="failure\n", stderr="nope\n"
            ),
        ]

    def test_session_notify(self, tmp_path) -> None:
        config, log = config_and_log("notify", tmp_path)

        results = run_session(config)

        assert results == [
            JobResultCompleted(
                name="always-notify", exit_status=0, stdout="always\n", stderr=""
            ),
            JobResultCompleted(
                name="never-notify",
                exit_status=99,
                stdout="You should not see this message.\n",
                stderr="",
            ),
        ]

        assert log == [
            JobResultCompleted(
                name="always-notify", exit_status=0, stdout="always\n", stderr=""
            ),
        ]

    def test_session_queue(self, tmp_path) -> None:
        config, _ = config_and_log("queue", tmp_path)
        start_time = time.time()
        results = run_session(config)
        duration = time.time() - start_time

        assert results == [
            JobResultCompleted(name="bar-1", exit_status=0, stdout="", stderr=""),
            JobResultCompleted(name="bar-2", exit_status=0, stdout="", stderr=""),
            JobResultCompleted(name="foo-1", exit_status=0, stdout="", stderr=""),
            JobResultCompleted(name="foo-2", exit_status=0, stdout="", stderr=""),
            JobResultCompleted(name="foo-3", exit_status=0, stdout="", stderr=""),
        ]
        assert 3 < duration < 4

    def test_job_wait(self, tmp_path) -> None:
        config, _ = config_and_log("wait", tmp_path)
        wait_job = job_path("wait", "wait")

        def run_wait_job(_: int) -> JobResult:
            return run_job(wait_job, config=config)

        with ThreadPoolExecutor(max_workers=2) as executor:
            results = executor.map(run_wait_job, range(2))

        assert set(results) == {
            JobResultCompleted(name="wait", exit_status=0, stdout="", stderr=""),
            JobResultLocked(name="wait"),
        }
