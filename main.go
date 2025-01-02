package main

import (
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/alecthomas/kong"
)

type RunCmd struct{}

type StatusCmd struct {
	LogLines int    `help:"Number of log lines to show" short:"l" default:"${defaultLogLines}"`
	JobName  string `arg:"" optional:"" default:"${allJobs}" help:"Show status for specific job only ('${allJobs}' for all)"`
}

type LogCmd struct {
	LogLines int `help:"Number of log lines to show" short:"l" default:"${defaultLogLines}"`
}

type CLI struct {
	Run    RunCmd    `cmd:"" help:"Run scheduler"`
	Status StatusCmd `cmd:"" help:"Show job status"`
	Log    LogCmd    `cmd:"" help:"Show application log"`

	Version    VersionFlag `short:"V" help:"Print version number and exit"`
	ConfigRoot string      `help:"Path to config directory" default:"${defaultConfigRoot}" type:"path"`
	StateRoot  string      `help:"Path to state directory" default:"${defaultStateRoot}" type:"path"`
}

type VersionFlag string

func (v VersionFlag) Decode(ctx *kong.DecodeContext) error {
	return nil
}

func (v VersionFlag) IsBool() bool {
	return true
}

func (v VersionFlag) BeforeApply(app *kong.Kong, vars kong.Vars) error {
	fmt.Println(version)
	app.Exit(0)

	return nil
}

func capitalizeFirst(s string) string {
	if s == "" {
		return s
	}

	r, size := utf8.DecodeRuneInString(s)
	if r == utf8.RuneError {
		return s
	}

	return string(unicode.ToUpper(r)) + s[size:]
}

func withLog(f func() error) {
	if err := f(); err != nil {
		if je, ok := err.(*JobError); ok {
			logJobPrintf(je.JobName, "%v", capitalizeFirst(je.Err.Error()))
		} else {
			log.Printf("%v", err)
		}
	}
}

type logWriter struct {
	tee io.StringWriter
}

func (writer *logWriter) Write(bytes []byte) (int, error) {
	timestamp := time.Now()
	formattedMsg := fmt.Sprintf("[%s] %s", timestamp.Format(timestampFormat), string(bytes))

	if writer.tee != nil {
		if _, err := writer.tee.WriteString(formattedMsg); err != nil {
			return 0, fmt.Errorf("failed to write to app log: %v\n", err)
		}
	}

	return fmt.Print(formattedMsg)
}

func logJobPrintf(job, format string, v ...any) {
	values := append([]any{job}, v...)
	log.Printf("[%s] "+format, values...)
}

func main() {
	log.SetFlags(0)

	db, err := openAppDB(defaultStateRoot)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to open app database: %v\n", err)
		os.Exit(1)
	}
	defer db.close()

	logPath := filepath.Join(defaultStateRoot, appLogFileName)
	logFile, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, filePerms)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to open app log file: %v\n", err)
		os.Exit(1)
	}
	defer logFile.Close()

	log.SetOutput(&logWriter{tee: logFile})

	cli := CLI{}
	ctx := kong.Parse(&cli,
		kong.Name("regular"),
		kong.Description("Run regular jobs."),
		kong.ConfigureHelp(kong.HelpOptions{
			Compact: true,
		}),
		kong.Exit(func(code int) {
			if code == 1 {
				code = 2
			}

			os.Exit(code)
		}),
		kong.Vars{
			"allJobs":           allJobs,
			"defaultConfigRoot": defaultConfigRoot,
			"defaultLogLines":   strconv.Itoa(defaultLogLines),
			"defaultStateRoot":  defaultStateRoot,
		},
	)

	config := Config{
		ConfigRoot: cli.ConfigRoot,
		StateRoot:  cli.StateRoot,
	}

	err = ctx.Run(config)
	if err != nil {
		log.Fatal(err)
	}
}
