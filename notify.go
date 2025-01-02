package main

import (
	"fmt"
	"os/user"
	"strings"

	mail "github.com/xhit/go-simple-mail/v2"
)

const (
	smtpServer   = "127.0.0.1"
	smtpPort     = 25
	fromUsername = "regular"

	errorText      = "Error: %v\n\n"
	exitStatusText = "Exit status: %v\n\n"
	failureSubject = "Job %q failed"
	successSubject = "Job %q succeeded"
)

type notifyMode string

const (
	notifyAlways    notifyMode = "always"
	notifyNever     notifyMode = "never"
	notifyOnFailure notifyMode = "on-failure"
)

type notifyWhenDone func(string, CompletedJob) error

func parseNotifyMode(mode string) (notifyMode, error) {
	switch mode {
	case string(notifyAlways):
		return notifyAlways, nil
	case string(notifyNever):
		return notifyNever, nil
	case string(notifyOnFailure), "":
		return notifyOnFailure, nil
	default:
		return "", fmt.Errorf("unknown notify mode: %v", mode)
	}
}

func notifyIfNeeded(notify notifyWhenDone, mode notifyMode, jobName string, completed CompletedJob) error {
	if mode == notifyNever {
		return nil
	}

	if !(mode == notifyAlways || mode == notifyOnFailure && !completed.IsSuccess()) {
		return nil
	}

	return notify(jobName, completed)
}

func localUserAddress(username string) string {
	return username + "@localhost"
}

func notifyUserByEmail(jobName string, completed CompletedJob) error {
	db, err := openAppDB(defaultStateRoot)
	if err != nil {
		return fmt.Errorf("failed to open database: %v", err)
	}
	defer db.close()

	subject, text, err := formatMessage(db, jobName, completed)
	if err != nil {
		return fmt.Errorf("failed to format notification message: %v", err)
	}

	currentUser, err := user.Current()
	if err != nil {
		return fmt.Errorf("failed to get current user: %v", err)
	}

	server := mail.NewSMTPClient()
	server.Host = smtpServer
	server.Port = smtpPort
	server.Username = currentUser.Username

	smtpClient, err := server.Connect()
	if err != nil {
		return fmt.Errorf("failed to connect to SMTP server: %v\n", err)
	}

	email := mail.NewMSG()
	email.SetFrom(localUserAddress(fromUsername)).
		AddTo(localUserAddress(currentUser.Username)).
		SetSubject(subject).
		SetBody(mail.TextPlain, text)

	if err := email.Send(smtpClient); err != nil {
		return fmt.Errorf("failed to send email: %v\n", err)
	}

	return nil
}

func formatMessage(db *appDB, jobName string, completed CompletedJob) (string, string, error) {
	subjectTemplate := successSubject
	if !completed.IsSuccess() {
		subjectTemplate = failureSubject
	}
	subject := fmt.Sprintf(subjectTemplate, jobName)

	var sb strings.Builder
	if completed.Error != "" {
		sb.WriteString(fmt.Sprintf(errorText, completed.Error))
	} else if completed.ExitStatus != 0 {
		sb.WriteString(fmt.Sprintf(exitStatusText, completed.ExitStatus))
	}

	if db != nil {
		for _, logName := range []string{"stdout", "stderr"} {
			lines, err := db.getJobLogs(jobName, logName, defaultLogLines)
			if err != nil {
				return "", "", fmt.Errorf("error reading log: %w", err)
			}

			if len(lines) == 0 {
				continue
			}

			sb.WriteString(logName + ":\n")

			for _, line := range lines {
				sb.WriteString("> " + line + "\n")
			}
		}
	}

	return subject, sb.String(), nil
}
