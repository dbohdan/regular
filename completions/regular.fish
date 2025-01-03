# fish shell completions for Regular.

complete -c regular -f

# Global options.
complete -c regular -s V -l version -d "Print version number and exit"
complete -c regular -s c -l config-dir -d "Path to config directory" -r
complete -c regular -s s -l state-dir -d "Path to state directory" -r

# Commands.
complete -c regular -n "not __fish_seen_subcommand_from list log run start status" -a list -d "List available jobs"
complete -c regular -n "not __fish_seen_subcommand_from list log run start status" -a log -d "Show application log"
complete -c regular -n "not __fish_seen_subcommand_from list log run start status" -a run -d "Run jobs once"
complete -c regular -n "not __fish_seen_subcommand_from list log run start status" -a start -d "Start scheduler"
complete -c regular -n "not __fish_seen_subcommand_from list log run start status" -a status -d "Show job status"

# Command-specific options.
complete -c regular -n "__fish_seen_subcommand_from log status" -s l -l log-lines -d "Number of log lines to show"
complete -c regular -n "__fish_seen_subcommand_from run" -s f -l force -d "Run jobs regardless of schedule"

# A helper function for job name completion.
function __regular_list_jobs
    regular list
end

# Add job name completion for relevant commands.
complete -c regular -n "__fish_seen_subcommand_from run status" -a "(__regular_list_jobs)" -d "Job name"
