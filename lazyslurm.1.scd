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
	Runs the *pre_push* hook locally before, and *post_push* on the target
	after, in *work/*.

*submit* [*--sbatch* _OPTS_] [*--note* _TEXT_] _SCRIPT_ [_ARGS_...]
	Submit _SCRIPT_, a tracked file inside the *push* paths given relative to
	the repository root, as an *sbatch* job running the commit at HEAD. The
	tree under the *push* paths must be clean. The files are sent, an
	immutable hardlink snapshot named after the commit is made on the target,
	and *sbatch* runs there. The job is appended to the journal. Needs *--yes*:
	without it, nothing is done and the exit status is 3. *post_push* runs in
	the snapshot.

*status* [*--all*] [_JOB_]
	List the jobs of the journal, newest first, with their states counted
	(for example _2 COMPLETED  1 RUNNING_). With a host selected, the states
	come from *sacct* and *squeue*; otherwise the list is offline and shows all
	hosts. A job shown in a final state is marked as seen and hidden next time
	unless *--all* is given.

*pull*
	Run *pre_pull* on the target in *work/*, bring back the *pull* paths from
	*keep/* into the repository (nothing is deleted locally, missing paths are
	skipped), copy the _slurm-\*.out_ and _slurm-\*.err_ files of the snapshots
	to _.slurm/logs/COMMIT/_, then run *post_pull* locally. The first failing
	step stops the rest.

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
	Do not ask for confirmation. Commands that need it exit with status 3 and do
	nothing without it.

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
