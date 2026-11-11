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
	Send the tracked files under the *push* paths to *work/* on the target, and
	remove from it the files no longer in the set. Refused when the tree under
	the *push* paths is not clean, untracked files included, so *work/* always
	holds a commit, unless *allow_dirty* is set: the tracked files are then
	sent as they are in the working tree, untracked ones are still not sent. Files that did not change are not sent again.
	Runs the *pre_push* hook locally before, and *post_push* on the target
	after, in *work/*.

*submit* [*--sbatch* _OPTS_] [*--note* _TEXT_] _SCRIPT_ [_ARGS_...]
	Submit _SCRIPT_, a tracked file inside the *push* paths given relative to
	the repository root, as an *sbatch* job running the commit at HEAD. The
	tree under the *push* paths must be clean, unless *allow_dirty* is set: the
	snapshot is then named after a commit that *git stash create* makes of the
	tracked files as they are, with no ref. The files are sent, an
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
	to _.slurm/state/logs/COMMIT/_, then run *post_pull* locally. The first failing
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
	target, else from _.slurm/state/logs/COMMIT/_ after a pull. With *-f* the file
	is followed until interrupted; the job must have started.

*exec* [*--*] _COMMAND_...
	Send the files like *push* (same clean tree rule, also lifted by *allow_dirty*), then run _COMMAND_ in *work/* through *srun*
	with the *srun* options of the host, after the *prelude*. One argument is
	a shell command line, several are quoted one by one. Refused without a
	*srun* key. The exit status is the command's. Not journaled, not
	confirmed.

*snapshots*
	One line per snapshot of the host, oldest first: commit, size, active jobs
	over total jobs, and state (_active_, _final_ or _stray_). The size is what
	the snapshot costs beyond *work/* and the older snapshots. Stray files are
	listed below it.

*clean*
	Delete from _keep/_ on the target the outputs already in the repository, as
	listed by the *clean* key of the host: each entry is a path _DIR/{job}_, where
	_DIR_ may hold *\**, *?* and *[...]* (_outputs/\*/{job}_), each matching inside one
	path component but not in the *keep* part. Every subfolder of a matching
	_DIR_ named after a Slurm job id is one run, so the
	scripts must write under the id (_$SLURM_ARRAY_JOB_ID_ for an array). A run
	is deleted only when it is a real directory, its job is in the journal of
	the host, every task of the job is in a final state, and an *rsync* dry run
	of it to the repository would transfer nothing. Failed and cancelled runs
	are deleted like the others. Anything else is left alone. If *sacct* or
	*squeue* fails, nothing is deleted. Lists the runs with their sizes.
	Confirmed. Hooks get *CLUSTER*, so *post_pull* can run
	_lazyslurm clean --yes_.

*drop* _COMMIT_
	Delete a snapshot, given by a hash prefix. Refused when a job is active in
	it. Stray files are not a reason to refuse. Confirmed.

# THE UI

Three panels on the left: *0* Hosts, *1* Jobs of the selected host, newest
first, and *2* Snapshots. The main view *3* shows the selection; the log
panel *4*
shows the results of the tasks, the output of hooks and the errors, and no
command lines. A job line reads the job id, the name of its script, and its
task counts at the right between brackets and separated by slashes, in this
order: running (cyan), waiting (yellow), completed (green), cancelled
(magenta) and failed (red). Counts that are zero are left out. While a task
runs, its name and an animated dot appear at the bottom left, before the keys.
The UI calls the same functions as the commands above, one task at a time, in
the background. It refreshes every 120 seconds with a single command on the
cluster.

*0* *1* *2* *3* *4*
	Go to the hosts, the jobs, the snapshots, the main view or the log.

*[* *]*, *tab*
	Go to the previous or the next one of these five, all around.

*j* *k*, arrows
	Move. *J* and *K* scroll the main view.

In the main view and in the log, *j* and *k* scroll it. *esc* or a panel
key leaves it. While the log has the focus it grows to two thirds of the
right column.

*enter*
	On a host, select it. On a job, read its log.

*/*
	Filter the jobs by job id, script or note, as you type on the bottom line.
	*enter* keeps the filter, *esc* clears it.

*R*
	Refresh now.

*P* *p*
	Push, pull.

*s*
	Submit. A picker lists the files that match *scripts*, the most recently
	modified first, and shows the selected one, where the *#SBATCH* lines are.
	Type to filter by fuzzy match (the letters in order, the closest match first),
	*up* and *down* to move, *enter* to pick. *tab* or a click
	moves to the preview to read it: *j* *k* *h* *l*, the arrows or the wheel
	scroll it, *tab* or *esc* comes back. Then type the arguments, the sbatch
	options and a note, then confirm.

*:*
	Type a command and run it in _work/_ with *srun*, as *exec* does. Past
	commands are listed below the prompt, kept in _.slurm/state/history_; *tab* moves
	to the list and *enter* copies one into the prompt. The UI steps aside,
	shows the output, and comes back on *enter*.

*r* *c*
	Retry, cancel the selected job, after a confirmation.

*L*
	Read the log of the selected job, and the state of each of its tasks.

*d*
	Drop the selected snapshot, after a confirmation.

*C*
	Clean: list the outputs *clean* would delete in the log panel, then delete
	them after a confirmation.

*?*
	Show the keys.

*q*, *ctrl-c*
	Quit.

When *ssh* asks for a key passphrase, a password, a PIN or a code, the UI
opens a popup to answer, with the input masked (*enter* answers, *esc*
cancels and makes *ssh* fail). It sets *SSH_ASKPASS* and
*SSH_ASKPASS_REQUIRE*, so it needs OpenSSH 8.4 or later. The commands never
prompt: they run *ssh* with *BatchMode=yes*, so open a master connection
first if the cluster needs a password.

The mouse works too: a click focuses a panel and selects a line, a double
click does what *enter* does, and the wheel moves the selection of a list or
scrolls the main view. A click on the log panel focuses it, and *j* *k* or the
wheel scroll it; *esc* goes back. Clicks on views under a popup are ignored.

# OPTIONS

*-h*
	Print a short help and exit.

*-V*
	Print the version and exit.

*-H* _HOST_
	Use this host of _.slurm/config_, instead of the *CLUSTER* environment
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

_.slurm/config_
	How the project uses Slurm: hosts, what to push and pull. Meant to be
	tracked by git, with any hook script the project keeps in _.slurm/_.

_.slurm/state/jobs.jsonl_
	Log of the submissions, one JSON object per line.

_.slurm/state/seen_
	Jobs already shown in a final state.

_.slurm/state/history_
	Commands typed after *:* in the UI, one per line.

_.slurm/state/_
	Everything lazyslurm writes that is local to the machine: the journal,
	_seen_, _history_ and the collected logs. Not worth tracking: add
	_.slurm/state/_ to the _.gitignore_ of the project.

# SEE ALSO

*ssh*(1), *rsync*(1), *sbatch*(1), *sacct*(1), *scancel*(1), *srun*(1)

# AUTHORS

Paul Chambaz, https://github.com/paulchambaz/lazyslurm
