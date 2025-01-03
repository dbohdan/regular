# Regular

> 🚧 **This project is in early development.**
> It is not ready for others to use.

**Regular** is a job scheduler like [cron](https://en.wikipedia.org/wiki/Cron) and [anacron](https://en.wikipedia.org/wiki/Anacron).

## Features

- Configuration using [Starlark](https://laurent.le-brun.eu/blog/an-overview-of-starlark), a small configuration language based on Python.
  You can use expressions like `hour in [9, 18] and minute == 0` to define when a job will run.
- Flexible scheduling based on current time and the last completed job
- The same shell on all platforms using [mvdan/sh](https://github.com/mvdan/sh)
- Jitter to mitigate the [thundering herd problem](https://en.wikipedia.org/wiki/Thundering_herd_problem)
- Job queues to configure what jobs run sequentially and in parallel
- Built-in logging and status reporting
- Built-in email notifications on localhost
- Hot reloading of job configuration on file change

## Installation

You will need Go 1.22 or later:

```shell
go install dbohdan.com/regular@latest
```

## Configuration

Jobs are defined in Starlark files named `job.star` in subdirectories of the config directory.
For example:

```starlark
# Run if at least a day has passed since the last run
# and it isn't the weekend.
def should_run(finished, timestamp, dow, **_):
    return dow not in [0, 6] and timestamp - finished >= one_day

# Random delay of up to 1 hour.
jitter = one_hour

# Shell script to run.
script = """
set -euo pipefail
backup.sh ~/docs /backup/docs
"""

# Queue name (the default is the name of the job directory).
queue = "backup"

# Write output to log files (default).
log = True

# When to send notifications: "always", "on-failure" (default), "never".
notify = "always"

# Allow multiple instances in queue (default).
duplicate = False

# Enable/disable the job (default).
enabled = True
```

Each job directory can also have an optional `job.env` file with environment variables:

```
PATH=${PATH}:${HOME}/bin
BACKUP_OPTS=--compress
```

## Usage

### General

- **regular** [_flags_] _command_
    - **-h**, **--help** Print help
    - **-V**, **--version** Print version number and exit
    - **-c**, **--config-dir** Path to config directory
    - **-s**, **--state-dir** Path to state directory
docs(*): remove '`' from comments

### Commands

Start the scheduler:

- **regular start**

Run specific jobs once:

- **regular run** [**--force**] [_job-names_...]

Check job status:

- **regular status** [**-l** _lines_] [_job-names_...]

View application log:

- **regular log** [**-l** _lines_]

List available jobs:

- **regular list**

## File locations

Default paths (override with **-c** and **-s**):

- Config: `~/.config/regular/`
  - Global environment: `~/.config/regular/global.env`
  - Job config: `~/.config/regular/<job>/job.star`
  - Job environment: `~/.config/regular/<job>/job.env`

- State: `~/.local/state/regular/`
  - App log: `~/.local/state/regular/app.log`
  - Database: `~/.local/state/regular/state.sqlite3`
  - Lock file: `~/.local/state/regular/app.lock`.
    When in use, this file prevents multiple instances of `regular start` from running at the same time.
  - Logs for the latest job: `~/.local/state/regular/<job>/{stdout,stderr}.log`.
    These logs and earlier logs are also stored in the database.

Job logs are truncated at 256 KiB.
There is currently no built-in way to remove old logs from the database.
You can use the [**sqlite3** command shell](https://www.sqlite.org/cli.html) to remove logs manually.

All files and directories are created with 0600 and 0700 permissions respectively.

## envfile package

The envfile package can parse `.env` files and manipulate their contents.
It can be used as a standalone library in your Go projects:

```shell
go get dbohdan.com/regular/envfile
```

envfile is mostly compatible with [GoDotEnv](https://github.com/joho/godotenv).
It is tested against test fixtures from GoDoEnv.
The only major difference is that attempting to substitute a nonexistent variable results in an error.
However, `.env` files have no formal specification, and differences in parsing around edge cases are to be expected.

### Features

- Parse `.env` files with quoted multiline values, backslash escape sequences, and comments
- Perform optional shell-style variable substitution
- Control substitution in the file by using either double/no quotes or single quotes
- Merge environments
- Convert between `[]string{"FOO=bar", ...}` and an environment map type

### Examples

Parse a `.env` file with variable substitution:

```go
content := strings.NewReader(`
# Set the base directory.
BASE=/opt
# Use substitution.
PATH=${BASE}/bin
`)

env, err := envfile.Parse(content, true, nil)
// env = map[string]string{"BASE": "/opt", "PATH": "/opt/bin"}
```

Load from a file:

```go
// Use the contents of os.Environ for subtitution.
substEnv := envfile.OS()
env, err := envfile.Load(".env", true, substEnv)
```

Convert environment strings:

```go
// From string slice to map.
env := envfile.EnvFromStrings([]string{"FOO=bar", "BAZ=qux"})

// Back to a string slice.
strings := env.Strings()
```

Merge multiple environments:

```go
merged := envfile.Merge(env1, env2, env3)
```

## License

MIT.
See the file [`LICENSE`](LICENSE).
