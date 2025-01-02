package main

import (
	"fmt"
	"testing"
)

func TestParseNotifyMode(t *testing.T) {
	tests := []struct {
		input    string
		expected notifyMode
		wantErr  bool
	}{
		{"always", notifyAlways, false},
		{"never", notifyNever, false},
		{"on-failure", notifyOnFailure, false},
		{"", notifyOnFailure, false},
		{"invalid", "", true},
	}

	for _, tt := range tests {
		t.Run(fmt.Sprintf("parseNotifyMode(%q)", tt.input), func(t *testing.T) {
			got, err := parseNotifyMode(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("parseNotifyMode(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
				return
			}
			if got != tt.expected {
				t.Errorf("parseNotifyMode(%q) = %v, want %v", tt.input, got, tt.expected)
			}
		})
	}
}

func TestNotifyIfNeeded(t *testing.T) {
	var notified bool
	mockNotify := func(jobName string, completed CompletedJob) error {
		notified = true
		return nil
	}

	tests := []struct {
		name         string
		mode         notifyMode
		job          CompletedJob
		shouldNotify bool
	}{
		{
			name:         "always mode success",
			mode:         notifyAlways,
			job:          CompletedJob{ExitStatus: 0},
			shouldNotify: true,
		},
		{
			name:         "always mode failure",
			mode:         notifyAlways,
			job:          CompletedJob{ExitStatus: 1},
			shouldNotify: true,
		},
		{
			name:         "never mode success",
			mode:         notifyNever,
			job:          CompletedJob{ExitStatus: 0},
			shouldNotify: false,
		},
		{
			name:         "never mode failure",
			mode:         notifyNever,
			job:          CompletedJob{ExitStatus: 1},
			shouldNotify: false,
		},
		{
			name:         "on-failure mode success",
			mode:         notifyOnFailure,
			job:          CompletedJob{ExitStatus: 0},
			shouldNotify: false,
		},
		{
			name:         "on-failure mode failure",
			mode:         notifyOnFailure,
			job:          CompletedJob{ExitStatus: 1},
			shouldNotify: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			notified = false
			err := notifyIfNeeded(mockNotify, tt.mode, "test-job", tt.job)
			if err != nil {
				t.Errorf("notifyIfNeeded() error = %v", err)
			}
			if notified != tt.shouldNotify {
				t.Errorf("notifyIfNeeded() notified = %v, want %v", notified, tt.shouldNotify)
			}
		})
	}
}

func TestFormatMessage(t *testing.T) {
	tests := []struct {
		name        string
		job         CompletedJob
		wantSubject string
		wantBody    string
		wantError   bool
	}{
		{
			name:        "success case",
			job:         CompletedJob{ExitStatus: 0},
			wantSubject: `Job "test-job" succeeded`,
			wantBody:    "",
			wantError:   false,
		},
		{
			name:        "failure with exit status",
			job:         CompletedJob{ExitStatus: 1},
			wantSubject: `Job "test-job" failed`,
			wantBody:    "Exit status: 1\n\n",
			wantError:   false,
		},
		{
			name:        "failure with error message",
			job:         CompletedJob{Error: "test error"},
			wantSubject: `Job "test-job" failed`,
			wantBody:    "Error: test error\n\n",
			wantError:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			subject, body, err := formatMessage(nil, "test-job", tt.job)
			if (err != nil) != tt.wantError {
				t.Errorf("formatMessage() error = %v, wantError %v", err, tt.wantError)
				return
			}
			if subject != tt.wantSubject {
				t.Errorf("formatMessage() subject = %v, want %v", subject, tt.wantSubject)
			}
			if body != tt.wantBody {
				t.Errorf("formatMessage() body = %q, want %q", body, tt.wantBody)
			}
		})
	}
}
