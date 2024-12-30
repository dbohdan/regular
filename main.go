package main

import (
	"fmt"
	"log"
	"os"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/alecthomas/kong"
)

type RunCmd struct{}

type StatusCmd struct {
	LogLines int `help:"Number of log lines to show" short:"l" default:"${defaultLogLines}"`
}

type CLI struct {
	Run    RunCmd    `cmd:"" help:"Run scheduler"`
	Status StatusCmd `cmd:"" help:"Show job status"`

	ConfigRoot string `help:"Path to config directory" default:"${defaultConfigRoot}" type:"path"`
	StateRoot  string `help:"Path to state directory" default:"${defaultStateRoot}" type:"path"`
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

type logWriter struct{}

func (writer logWriter) Write(bytes []byte) (int, error) {
	timestamp := time.Now().Format(timestampFormat)
	return fmt.Printf("[%s] %s", timestamp, string(bytes))
}

func logJobPrintf(job, format string, v ...any) {
	values := append([]any{job}, v...)
	log.Printf("[%s] "+format, values...)
}

func main() {
	log.SetFlags(0)
	log.SetOutput(new(logWriter))

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
			"defaultConfigRoot": defaultConfigRoot,
			"defaultLogLines":   defaultLogLines,
			"defaultStateRoot":  defaultStateRoot,
		},
	)

	config := Config{
		ConfigRoot: cli.ConfigRoot,
		StateRoot:  cli.StateRoot,
	}

	err := ctx.Run(config)
	if err != nil {
		log.Fatal(err)
	}
}
