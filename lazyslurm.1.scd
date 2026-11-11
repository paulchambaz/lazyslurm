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

Without a command, the UI opens. See *THE UI*.

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
	step stops the rest. The states of the jobs are read first, and at the
	end the snapshots that are done are deleted: every job of the commit is
	final, no job in the queue works inside it, and it holds no stray file (a
	file that is not in the commit, not under a *keep* path, not matched by
	*exclude* and not a collected log). A snapshot kept for stray files is
	reported. If *sacct* or *squeue* fails, nothing is deleted.

*retry* [_JOB_]
	Resubmit the failed tasks of _JOB_, or of the newest job of the host. A
	task failed when it is in a final state other than _COMPLETED_. For an
	array, the failed task numbers are given as *--array*, which replaces the
	script's own *--array* (so a *%limit* is lost). The job runs the same
	script, arguments, *sbatch* options and commit as before, whatever HEAD is.
	If the snapshot of that commit is gone, it is rebuilt from the local
	repository with *git archive*, *tar* and *rsync*, hardlinked to *work/*.
	*post_push* runs in the snapshot. The journal line has *retry_of* and
	*array*. Confirmed. The host is the one of the job.

*cancel* _JOB_[_\_TASK_]
	Run *scancel* on an active job or task, after checking it is in the queue.
	There is no default job. Confirmed. The host is the one of the job.

*logs* [*-f*] [*--err*] _JOB_[_\_TASK_]
	Print the standard output of a job, or with *--err* its standard error.
	It is read from the path *scontrol* reports, else from the snapshot on the
	target, else from _.slurm/logs/COMMIT/_ after a pull. With *-f* the file
	is followed until interrupted; the job must have started.

*exec* [*--*] _COMMAND_...
	Send the files like *push*, then run _COMMAND_ in *work/* through *srun*
	with the *srun* options of the host, after the *prelude*. One argument is
	a shell command line, several are quoted one by one. Refused without a
	*srun* key. The exit status is the command's. Not journaled, not
	confirmed.

*snapshots*
	One line per snapshot of the host, oldest first: commit, size, active jobs
	over total jobs, and state (_active_, _final_ or _stray_). The size is what
	the snapshot costs beyond *work/* and the older snapshots. Stray files are
	listed below it.

*drop* _COMMIT_
	Delete a snapshot, given by a hash prefix. Refused when a job is active in
	it. Stray files are not a reason to refuse. Confirmed.

# THE UI

Three panels on the left: *1* Hosts, *2* Jobs of the selected host, newest
first, and *3* Snapshots. The main view shows the selection; the log panel
shows every command lazyslurm runs and the output of hooks. The UI calls the
same functions as the commands above, one task at a time, in the background.
It refreshes every 120 seconds and on *R*.

*1* *2* *3*, *[* *]*, *tab*
	Go to a panel.

*j* *k*, arrows
	Move. *J* and *K* scroll the main view.

*enter*
	On a host, select it. On a job, read its log.

*/*
	Filter the jobs by job id, script or note.

*P* *p*
	Push, pull.

*s*
	Submit: pick a script from *scripts*, then type its arguments, the sbatch
	options and a note, then confirm.

*:*
	Type a command and run it in _work/_ with *srun*, as *exec* does. The UI
	steps aside, shows the output, and comes back on *enter*.

*r* *c*
	Retry, cancel the selected job, after a confirmation.

*l*
	Read the log of the selected job, and the state of each of its tasks.

*d*
	Drop the selected snapshot, after a confirmation.

*?*
	Show the keys.

*q*, *ctrl-c*
	Quit.

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
	Do not ask for confirmation. Without it, a terminal gets a _[y/N]_ prompt
	(an answer other than _y_ exits 1), and without a terminal the commands
	that change the cluster do nothing and exit with status 3.

# EXIT STATUS

0 on success, 1 on an error or a declined prompt, 2 on a usage error, 3 when
a confirmation was needed and not given. *exec* exits with the status of its
command.

# FILES

_.slurm.toml_
	How the project uses Slurm: hosts, what to push and pull.

_.slurm/jobs.jsonl_
	Log of the submissions, one JSON object per line.

_.slurm/seen_
	Jobs already shown in a final state.

# SEE ALSO

*ssh*(1), *rsync*(1), *sbatch*(1), *sacct*(1), *scancel*(1), *srun*(1)

# AUTHORS

Paul Chambaz, https://github.com/paulchambaz/lazyslurm
