from __future__ import annotations

import json
import os
import re
import time
from concurrent.futures import ThreadPoolExecutor
from pathlib import Path
from typing import Callable

import pytest
from regular import (
    Config,
    Job,
    JobResult,
    JobResultCompleted,
    JobResultError,
    JobResultLocked,
    JobResultSkipped,
    cli_command_list,
    cli_command_log,
    cli_command_show,
    run_job,
    run_session,
)
from regular.basis import FileDirNames, load_env, parse_env
from regular.main import QUEUE_LOCK_WAIT

TEST_DIR = Path(__file__).parent


def cli_output_logger() -> tuple[Callable[[str], None], list[str]]:
    out_log = []

    def print_to_log(text: str) -> None:
        out_log.append(text)

    return (print_to_log, out_log)


def config_and_log(
    configs_subdir: str, state_root: Path
) -> tuple[Config, list[JobResult]]:
    result_log = []

    def test_notify(result: JobResult) -> None:
        result_log.append(result)

    return (
        Config.load_env(
            config_root=TEST_DIR / "configs" / configs_subdir,
            notifiers=[test_notify],
            state_root=state_root,
        ),
        result_log,
    )


def job_path(configs_subdir: str, job_name: str) -> Path:
    return TEST_DIR / "configs" / configs_subdir / job_name


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

    def test_session_basic_force(self, tmp_path) -> None:
        config, _ = config_and_log("basic", tmp_path)

        results1 = run_session(config)
        results2 = run_session(config, force=True)

        assert results1 == results2

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
        os_var = "<(***)>"
        os.environ["OS_VAR"] = os_var

        results = run_session(config)

        assert results == [
            JobResultCompleted(
                name="greet", exit_status=0, stdout="Hello, world!\n", stderr=""
            ),
            JobResultCompleted(
                name="os-var", exit_status=0, stdout=f"{os_var}\n", stderr=""
            ),
            JobResultCompleted(
                name="override",
                exit_status=0,
                stdout="Message overridden.\n",
                stderr="",
            ),
        ]

    def test_session_error_notify(self, tmp_path) -> None:
        config, log = config_and_log("error-notify", tmp_path)

        results = run_session(config)

        assert len(results) == 2
        assert len(log) == 1
        assert log[0].name == "missing-script"

    def test_session_failure(self, tmp_path) -> None:
        config, _ = config_and_log("failure", tmp_path)

        results = run_session(config)

        assert results == [
            JobResultCompleted(
                name="failure", exit_status=99, stdout="failure\n", stderr="nope\n"
            ),
        ]

    def test_session_filename(self, tmp_path) -> None:
        config, _ = config_and_log("filename", tmp_path)

        results = run_session(config)

        assert results == [
            JobResultCompleted(name="foo", exit_status=0, stdout="run.sh\n", stderr=""),
        ]

    def test_session_invalid_jitter(self, tmp_path) -> None:
        config, _ = config_and_log("invalid-jitter", tmp_path)

        results = run_session(config)

        assert len(results) == 1
        assert isinstance(results[0], JobResultError)
        assert results[0].message == "invalid duration: 'nah'"

    def test_session_invalid_schedule(self, tmp_path) -> None:
        config, _ = config_and_log("invalid-schedule", tmp_path)

        results = run_session(config)

        assert len(results) == 1
        assert isinstance(results[0], JobResultError)
        assert results[0].message == "invalid duration: 'no'"

    def test_session_no_script(self, tmp_path) -> None:
        config, _ = config_and_log("no-script", tmp_path)

        results = run_session(config)

        assert len(results) == 2
        for i in range(2):
            result = results[i]
            assert isinstance(result, JobResultError)
            assert re.search(r"No such file or directory", result.message)

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
            JobResultCompleted(
                name="notify-on-error", exit_status=0, stdout="", stderr=""
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

    def test_job_jitter(self, tmp_path) -> None:
        config, _ = config_and_log("jitter", tmp_path)
        jitter_job = job_path("jitter", "jitter")

        def time_job() -> float:
            start_time = time.time()
            run_job(Job.load(jitter_job, config.state_root), config)
            return time.time() - start_time

        times = [time_job() - QUEUE_LOCK_WAIT for _ in range(20)]

        # The jitter is a uniformly-distributed random variable.
        # The mean of `times` is therefore approximately a random variable
        # with the Bates probability distribution.
        # For it, F(0.75) - F(0.25) ≈ 0.99994,
        # where F is the cumulative distribution function.
        # The following assertion
        # should be true around 9999 times out of 10000.
        assert 0.025 < sum(sorted(times)) / len(times) < 0.075

    def test_job_wait(self, tmp_path) -> None:
        config, _ = config_and_log("wait", tmp_path)
        wait_job = job_path("wait", "wait")

        def run_wait_job(_: int) -> JobResult:
            return run_job(Job.load(wait_job, config.state_root), config)

        with ThreadPoolExecutor(max_workers=2) as executor:
            results = executor.map(run_wait_job, range(2))

        assert set(results) == {
            JobResultCompleted(name="wait", exit_status=0, stdout="", stderr=""),
            JobResultLocked(name="wait"),
        }

    def test_load_env(self) -> None:
        config, _ = config_and_log("env", TEST_DIR)
        env_file = config.config_root / FileDirNames.DEFAULTS / "env"

        env = load_env(env_file)

        assert env == {
            "PART": "Hello, ",
            "MESSAGE": "Hello, world!",
        }

    def test_parse_env_blank(self) -> None:
        assert parse_env("\n   \t \n\n\n") == {}

    def test_parse_env_comment(self) -> None:
        assert parse_env("# A=B\n# foo") == {}

    def test_parse_env_simple_var(self) -> None:
        assert parse_env("A=B\n FOO =\tBAR  ") == {"A": "B", "FOO": "BAR"}

    def test_parse_env_subst(self) -> None:
        assert parse_env(
            "H-E-L-L-O=Hello\n\n"
            "__w0rld__=world\n"
            "GREETING=${H-E-L-L-O}, ${__w0rld__}"
        ) == {
            "H-E-L-L-O": "Hello",
            "__w0rld__": "world",
            "GREETING": "Hello, world",
        }

    def test_parse_env_substsubst_env(self) -> None:
        assert parse_env("DIR=${HOME}/foo", subst_env={"HOME": "/home/user"}) == {
            "DIR": "/home/user/foo"
        }

    def test_parse_env_subst_nonexistent(self) -> None:
        with pytest.raises(KeyError):
            parse_env("foo=${no-such-var}")

    def test_parse_env_subst_off(self) -> None:
        assert parse_env("foo=${no-such-var}", subst=False) == {"foo": "${no-such-var}"}

    def test_parse_env_quotes(self) -> None:
        assert parse_env(
            'spaces= "   " \n'
            "tabs\t=\t'\t\t\t'\t\n"
            'subst="${spaces}${tabs}"\n'
            "no_subst='${spaces}${tabs}'"
        ) == {
            "spaces": "   ",
            "tabs": "\t\t\t",
            "subst": "   \t\t\t",
            "no_subst": "${spaces}${tabs}",
        }

    def test_cli_list(self, tmp_path) -> None:
        config, _ = config_and_log("basic", tmp_path)
        print_to_log, out_log = cli_output_logger()

        cli_command_list(config, print_func=print_to_log)

        assert out_log == ["bar", "foo"]

    def test_cli_list_jsonl(self, tmp_path) -> None:
        config, _ = config_and_log("basic", tmp_path)
        print_to_log, out_log = cli_output_logger()

        cli_command_list(config, json_lines=True, print_func=print_to_log)

        assert out_log == ['"bar"', '"foo"']

    def test_cli_log(self, tmp_path) -> None:
        config, _ = config_and_log("basic", tmp_path)
        print_to_log, out_log = cli_output_logger()

        run_session(config)
        cli_command_log(config, print_func=print_to_log)

        bar, foo = out_log
        assert re.match(
            r"bar\n  stdout.log \([^)]+\):\nbar\n\n  stderr.log \([^)]+\):\n", bar
        )
        assert re.match(
            r"foo\n  stdout.log \([^)]+\):\nfoo\n\n  stderr.log \([^)]+\):\n", foo
        )

    def test_cli_log_jsonl(self, tmp_path) -> None:
        config, _ = config_and_log("basic", tmp_path)
        print_to_log, out_log = cli_output_logger()

        run_session(config)
        cli_command_log(config, json_lines=True, print_func=print_to_log)

        assert len(out_log) == 2

        jsonl = [json.loads(line) for line in out_log]

        for i, name in ((0, "bar"), (1, "foo")):
            for j in range(2):
                jsonl[i]["logs"][j]["modified"] = "MOD_DATETIME"

            assert jsonl[i] == {
                "name": name,
                "logs": [
                    {
                        "filename": "stdout.log",
                        "modified": "MOD_DATETIME",
                        "contents": f"{name}\n",
                    },
                    {
                        "filename": "stderr.log",
                        "modified": "MOD_DATETIME",
                        "contents": "",
                    },
                ],
            }

    def test_cli_show(self, tmp_path) -> None:
        config, _ = config_and_log("basic", tmp_path)
        print_to_log, out_log = cli_output_logger()

        run_session(config)
        cli_command_show(config, print_func=print_to_log)

        bar, foo = out_log
        assert re.match(r"bar\n.*schedule: 1m\n", bar, re.DOTALL)
        assert re.match(r"foo\n.*schedule: 5 s\n", foo, re.DOTALL)

    def test_cli_show_jsonl(self, tmp_path) -> None:
        config, _ = config_and_log("basic", tmp_path)
        print_to_log, out_log = cli_output_logger()

        run_session(config)
        cli_command_show(config, json_lines=True, print_func=print_to_log)

        assert len(out_log) == 2

        bar, foo = (json.loads(line) for line in out_log)
        assert bar["name"] == "bar"
        assert bar["schedule"] == "1m"
        assert foo["name"] == "foo"
        assert foo["schedule"] == "5 s"
