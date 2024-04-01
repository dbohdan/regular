from __future__ import annotations

import getpass
import smtplib
from dataclasses import dataclass
from email.message import EmailMessage
from typing import TYPE_CHECKING

from regular.basis import (
    APP_NAME,
    SMTP_SERVER,
    Config,
    FileDirNames,
    JobResult,
    JobResultCompleted,
    JobResultError,
    Messages,
    Notify,
)

if TYPE_CHECKING:
    from pathlib import Path


@dataclass
class ResultMessage:
    pass


@dataclass
class ResultMessageEmpty(ResultMessage):
    pass


@dataclass
class ResultMessageFull(ResultMessage):
    title: str
    text: str


def notify_user(result: JobResult, *, config: Config) -> None:
    for notifier in config.notifiers:
        notifier(result)


def result_message(result: JobResult) -> ResultMessage:
    if isinstance(result, JobResultCompleted):
        return result_message_completed(result)

    if isinstance(result, JobResultError):
        return result_message_error(result)

    return ResultMessageEmpty()


def result_message_completed(result: JobResultCompleted) -> ResultMessageFull:
    title_template = (
        Messages.RESULT_COMPLETED_TITLE_SUCCESS
        if result.exit_status == 0
        else Messages.RESULT_COMPLETED_TITLE_FAILURE
    )

    title = title_template.format(name=result.name, exit_status=result.exit_status)

    content = Messages.RESULT_COMPLETED_TEXT.format(
        name=result.name,
        exit_status=result.exit_status,
        stdout="\n".join(result.stdout.lines),
        stderr="\n".join(result.stderr.lines),
    )

    return ResultMessageFull(title, content)


def result_message_error(result: JobResultError) -> ResultMessageFull:
    title = Messages.RESULT_ERROR_TITLE.format(
        name=result.name,
    )

    text = Messages.RESULT_ERROR_TEXT.format(
        name=result.name,
        log=result.log,
        message=result.message,
    )

    return ResultMessageFull(title, text)


def email_message(subject: str, text: str) -> EmailMessage:
    msg = EmailMessage()

    msg["Subject"] = subject
    msg["From"] = APP_NAME
    msg["To"] = getpass.getuser()

    msg.set_content(text)

    return msg


def notify_user_by_email(result: JobResult) -> None:
    res_msg = result_message(result)
    if not isinstance(res_msg, ResultMessageFull):
        return
    email_msg = email_message(res_msg.title, res_msg.text)

    smtp = smtplib.SMTP(SMTP_SERVER)
    smtp.send_message(email_msg)
    smtp.quit()


def notify_user_if_necessary(
    job_dir: Path, *, config: Config, result: JobResult
) -> None:
    notify = Notify.load(job_dir / FileDirNames.NOTIFY)

    if notify != Notify.NEVER and (
        (isinstance(result, JobResultCompleted) and result.exit_status != 0)
        or isinstance(result, JobResultError)
        or notify == Notify.ALWAYS
    ):
        notify_user(
            result,
            config=config,
        )
