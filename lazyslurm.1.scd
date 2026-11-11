LAZYSLURM(1) "github.com/paulchambaz/lazyslurm" "General Commands Manual"

# NAME

*lazyslurm* - terminal UI for Slurm clusters

# SYNOPSIS

*lazyslurm* [*-hV*] [*-H* _HOST_] [*--json*] [*--yes*] [_COMMAND_]

# DESCRIPTION

*lazyslurm* is a terminal UI for Slurm clusters, in the spirit of lazygit and
lazydocker. It is opened from a repository and works on it: push the code,
submit, follow, pull the results, retry and cancel.

The local repository is the source of truth. Nothing is installed on the
clusters: everything goes through *ssh* and *rsync*.

Without a command, the UI opens; it only draws a window for now. Press *q* or
*ctrl-c* to quit.

# COMMANDS

*push*
	Send the tracked and untracked, not ignored, files under the *push* paths
	to *work/* on the target, and remove from it the files no longer in the
	set. Local changes are sent. Files that did not change are not sent again.

*status* [*--all*] [_JOB_]
	List the jobs of the journal, newest first, for the selected host or for
	all hosts when none is selected. Jobs already seen are hidden unless
	*--all* is given.

# OPTIONS

*-h*
	Print a short help and exit.

*-V*
	Print the version and exit.

*-H* _HOST_
	Use this host of _.slurm.toml_, instead of the *CLUSTER* environment
	variable or the default host.

*--json*
	Print JSON Lines.

*--yes*
	Do not ask for confirmation.

# FILES

_.slurm.toml_
	How the project uses Slurm: hosts, what to push and pull.

_.slurm/jobs.jsonl_
	Log of the submissions, one JSON object per line.

_.slurm/seen_
	Jobs already shown in a final state.

# SEE ALSO

*ssh*(1), *rsync*(1), *sbatch*(1), *sacct*(1)

# AUTHORS

Paul Chambaz, https://github.com/paulchambaz/lazyslurm
