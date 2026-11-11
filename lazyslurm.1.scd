LAZYSLURM(1) "github.com/paulchambaz/lazyslurm" "General Commands Manual"

# NAME

*lazyslurm* - terminal UI and command line for Slurm clusters

# SYNOPSIS

*lazyslurm* [*-hV*] [*-H* _HOST_]++
*lazyslurm* *push* [*--json*]++
*lazyslurm* *submit* [*--yes*] [*--sbatch* _OPTS_] [*--note* _TEXT_] [*--json*] _SCRIPT_ [_ARGS_...]++
*lazyslurm* *status* [*--all*] [*--json*] [_JOB_]++
*lazyslurm* *pull* [*--json*] [_JOB_]++
*lazyslurm* *retry* [*--yes*] [*--json*] [_JOB_]++
*lazyslurm* *cancel* [*--yes*] [*--json*] _JOB_[_\_TASK_]++
*lazyslurm* *logs* [*-f*] [*--err*] _JOB_[_\_TASK_]++
*lazyslurm* *exec* [*--*] _COMMAND_...++
*lazyslurm* *snapshots* [*--json*]++
*lazyslurm* *drop* [*--yes*] [*--json*] _COMMIT_++
*lazyslurm* *clean* [*--yes*] [*--json*]

# DESCRIPTION

*lazyslurm* is to Slurm what lazygit is to git. Opened from a git repository,
it sends the code to a cluster, submits jobs, follows them, brings the results
back, retries what failed and cancels what must stop. Without a command it
opens a terminal UI. With a command it does the same thing once, prints to the
standard output and exits, so a script or an agent can use it: the UI is built
on the same functions as the commands.

The local repository is the source of truth. *lazyslurm* keeps no job id and no
run id of its own beyond a small journal of what it submitted. Nothing is
installed on the cluster: it only needs *ssh*, *rsync* and the Slurm clients
that are already there, and it works over the ControlMaster of your
_~/.ssh/config_.

What it adds to plain *sbatch* is reproducibility. A job does not run in the
folder you keep editing: it runs in a snapshot of the commit you submitted, so
pushing again, or submitting something else, never changes a job that is queued
or running. Snapshots share their files through hardlinks, so they cost almost
nothing, and they are deleted by themselves once their jobs are over and their
results are back.

A script does not need to know any of this. It runs in a folder that looks like
an ordinary checkout, and its *#SBATCH* lines are untouched.

# OPTIONS

*-h*, *--help*
	Print a short help and exit.

*-V*, *--version*
	Print the version and exit.

*-H* _HOST_
	Use this host of _.slurm/config_, instead of the *CLUSTER* environment
	variable or the default host.

*--json*
	Print JSON Lines instead of text.

*--yes*
	Do not ask for confirmation. Without it, a terminal gets a _[y/N]_ prompt
	(an answer other than _y_ exits 1), and without a terminal the commands
	that change the cluster do nothing and exit with status 3.

# GETTING STARTED

In the root of your repository, write _.slurm/config_ (see *CONFIGURATION*). A
small one is enough:

```
[default]
host  = "mycluster"
push  = ["src", "slurm", "pyproject.toml"]
keep  = [".venv", "outputs"]
pull  = ["outputs/results"]

[hosts.mycluster]
target = "mycluster:myproject"
```

_mycluster_ is a host of your _~/.ssh/config_, and _myproject_ is the folder
lazyslurm owns there. Add _.slurm/state/_ to your _.gitignore_, since it holds
what lazyslurm writes on this machine.

Commit, then try it in this order, each step being safe to repeat:

	lazyslurm push
	lazyslurm submit --yes slurm/train.sh
	lazyslurm status
	lazyslurm pull

Or just run *lazyslurm* and press *P*, *s* and *p*.

The first *submit* is the proof that everything works: the job shows in
*lazyslurm status*, and *lazyslurm snapshots* lists its snapshot. Run *lazyslurm
exec nvidia-smi* first if you want to check the allocation options of *srun*.

# HOW IT WORKS

For each host, *lazyslurm* owns one folder, the target, and nothing outside it:

_work/_
	A mutable mirror of the files under the *push* paths. *push* is the only
	thing that touches it, with *rsync* and never more than the push paths.

_COMMIT/_
	A snapshot: an immutable copy of _work/_ for one commit, named by the full
	hash, made with hardlinks. A job runs in its snapshot.

_keep/_
	The one real copy of the paths listed in *keep*: a virtualenv, the
	outputs, a cache. In _work/_ and in every snapshot, each of them is a
	symlink into _keep/_, so a script that writes _outputs/x_ writes to _keep/_
	and the data outlives the snapshot.

Because snapshots are hardlinks, fifteen commits that share a 1 GB file hold
that file once. A snapshot only costs what differs from _work/_ and from the
older snapshots, and *snapshots* shows exactly that.

A snapshot is deleted by *pull*, and only when every job of its commit is in a
final state, no job in the queue works inside it, and it holds no stray file: a
file that is not in the commit, not under a *keep* path, not matched by
*exclude* and not a collected log. A job that wrote its results next to its
script instead of under a *keep* path is therefore never lost silently: the
snapshot is kept and the stray files are listed.

# COMMANDS

*push*
	Send the tracked files under the *push* paths to _work/_ on the target, and
	remove from it the files no longer in the set. Refused when the tree under
	the *push* paths is not clean, untracked files included, so _work/_ always
	holds a commit, unless *allow_dirty* is set: the tracked files are then sent
	as they are in the working tree, untracked ones are still not sent. Files
	that did not change are not sent again. Runs the *pre_push* hook locally
	before, and *post_push* on the target after, in _work/_.

*submit* [*--sbatch* _OPTS_] [*--note* _TEXT_] _SCRIPT_ [_ARGS_...]
	Submit _SCRIPT_, a tracked file inside the *push* paths given relative to
	the repository root, as an *sbatch* job running the commit at HEAD. The
	tree under the *push* paths must be clean, unless *allow_dirty* is set: the
	snapshot is then named after a commit that *git stash create* makes of the
	tracked files as they are, with no ref. The files are sent, a snapshot named
	after the commit is made on the target, and *sbatch* runs there. The job is
	appended to the journal. Needs *--yes*: without it, nothing is done and the
	exit status is 3. *post_push* runs in the snapshot.

*status* [*--all*] [_JOB_]
	List the jobs of the journal, newest first, with their states counted (for
	example _2 COMPLETED  1 RUNNING_). With a host selected, the states come
	from *sacct* and *squeue*; otherwise the list is offline and shows all
	hosts. A job shown in a final state is marked as seen and hidden next time
	unless *--all* is given.

*pull* [_JOB_]
	Run *pre_pull* on the target in _work/_, bring back the *pull* paths from
	_keep/_ into the repository (nothing is deleted locally, missing paths are
	skipped), copy the _slurm-\*.out_ and _slurm-\*.err_ files of the snapshots
	to _.slurm/state/logs/COMMIT/_, then run *post_pull* locally. The first
	failing step stops the rest. The states of the jobs are read first, and at
	the end the snapshots that are done are deleted, by the rules of *HOW IT
	WORKS*. A snapshot kept for stray files is reported. If *sacct* or *squeue*
	fails, nothing is deleted.
	With _JOB_, a job of the journal of the host, nothing changes except that
	*pre_pull* and *post_pull* see its id in *LAZYSLURM_JOB*, so that a hook can
	look at one job, for example a finished one.

*retry* [_JOB_]
	Resubmit the failed tasks of _JOB_, or of the newest job of the host. A task
	failed when it is in a final state other than _COMPLETED_. For an array, the
	failed task numbers are given as *--array*, which replaces the script's own
	*--array* (so a *%limit* is lost). The job runs the same script, arguments,
	*sbatch* options and commit as before, whatever HEAD is. If the snapshot of
	that commit is gone, it is rebuilt from the local repository with *git
	archive*, *tar* and *rsync*, hardlinked to _work/_. *post_push* runs in the
	snapshot. The journal line has *retry_of* and *array*. Confirmed. The host
	is the one of the job.

*cancel* _JOB_[_\_TASK_]
	Run *scancel* on an active job or task, after checking it is in the queue.
	There is no default job. Confirmed. The host is the one of the job.

*logs* [*-f*] [*--err*] _JOB_[_\_TASK_]
	Print the standard output of a job, or with *--err* its standard error. It
	is read from the path *scontrol* reports, else from the snapshot on the
	target, else from _.slurm/state/logs/COMMIT/_ after a pull. With *-f* the
	file is followed until interrupted; the job must have started.

*exec* [*--*] _COMMAND_...
	Send the files like *push* (same clean tree rule, also lifted by
	*allow_dirty*), then run _COMMAND_ in _work/_ through *srun* with the *srun*
	options of the host, after the *prelude*. One argument is a shell command
	line, several are quoted one by one. Refused without a *srun* key. The exit
	status is the command's. Not journaled, not confirmed.

*snapshots*
	One line per snapshot of the host, oldest first: commit, size, active jobs
	over total jobs, and state (_active_, _final_ or _stray_). The size is what
	the snapshot costs beyond _work/_ and the older snapshots. Stray files are
	listed below it.

*clean*
	Delete from _keep/_ on the target the outputs already in the repository, as
	listed by the *clean* key of the host. See *CLEANING*. Lists the runs with
	their sizes. Confirmed. Hooks get *CLUSTER*, so *post_pull* can run _lazyslurm
	clean --yes_.

*drop* _COMMIT_
	Delete a snapshot, given by a hash prefix. Refused when a job is active in
	it. Stray files are not a reason to refuse. Confirmed.

# CLEANING

Results pile up on the cluster, and *pull* never deletes anything there. *clean*
does, under rules that make it hard to lose anything. Its worst case is to
delete what already is in your repository.

Each entry of the *clean* key is a path _DIR/{job}_, where _DIR_ is a *keep*
path or lies inside one, and may hold *\**, *?* and *[...]*, each matching inside
one path component but never in the *keep* part (_outputs/\*/{job}_). Every
subfolder of a matching _DIR_ that is named after a Slurm job id is one run, so
the scripts must write under the id (_$SLURM_ARRAY_JOB_ID_ for an array).

A run is deleted only when all of this holds:

- it is a real directory, not a symlink, and it stays inside _keep/_;
- its job is in the journal of the host, so a run started elsewhere is never
  touched;
- every task of the job is in a final state, which is read from *sacct* and
  *squeue*, and if either fails nothing is deleted;
- an *rsync* dry run of it to the repository would transfer nothing: it was
  pulled, and is unchanged since.

Failed and cancelled runs are deleted like the others, since they are pulled
too. A run that is not finished is left, and a later *clean* takes it, which is
why running it from *post_pull* is enough. A path that is not in *pull*, such as
graphs or videos you chose to leave on the cluster, is never in the repository
and so is never deleted.

# THE UI

Three panels on the left: *0* Hosts, *1* Jobs of the selected host, newest
first, and *2* Snapshots. The main view *3* shows the selection; the log panel
*4* shows the results of the tasks, the output of hooks and the errors, and no
command lines. A job line reads the job id, the name of its script, and its
task counts, right-aligned, between brackets and separated by slashes, in this
order: running (cyan), waiting (yellow), completed (green), cancelled (magenta)
and failed (red). Counts that are zero are left out. While a task runs, its
name and an animated dot appear at the bottom left, before the keys. The UI
calls the same functions as the commands above, one task at a time, in the
background. It refreshes every 120 seconds with a single command on the
cluster.

*0* *1* *2* *3* *4*
	Go to the hosts, the jobs, the snapshots, the main view or the log.

*[* *]*, *tab*
	Go to the previous or the next one of these five, all around.

*j* *k*, arrows
	Move. *J* and *K* scroll the main view.

In the main view and in the log, *j* and *k* scroll it. *esc* or a panel key
leaves it. While the log has the focus it grows to two thirds of the right
column, and the snapshots panel grows only while it has the focus.

*enter*
	On a host, select it. On a job, read its log.

*/*
	Filter the jobs by job id, script or note, as you type on the bottom line.
	*enter* keeps the filter, *esc* clears it.

*R*
	Refresh now.

*P* *p*
	Push, pull. In the Jobs panel, *p* pulls for the selected job: the hooks of
	the pull see its id in *LAZYSLURM_JOB*, and everything is pulled all the
	same.

*s*
	Submit. A picker lists the files that match *scripts*, the most recently
	modified first, and shows the selected one, where the *#SBATCH* lines are.
	Type to filter by fuzzy match (the letters in order, the closest match
	first), *up* and *down* to move, *enter* to pick. *tab* or a click moves to
	the preview to read it: *j* *k* *h* *l*, the arrows or the wheel scroll it,
	*tab* or *esc* comes back. Then type the arguments, the sbatch options and a
	note, then confirm.

*:*
	Type a command and run it in _work/_ with *srun*, as *exec* does. Past
	commands are listed below the prompt, kept in _.slurm/state/history_; *tab*
	moves to the list and *enter* copies one into the prompt. The UI steps
	aside, shows the output, and comes back on *enter*.

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

The mouse works too: a click focuses a panel and selects a line, a double click
does what *enter* does, and the wheel moves the selection of a list or scrolls
the main view. Clicks on views under a popup are ignored.

When *ssh* asks for a key passphrase, a password, a PIN or a code, the UI opens
a popup to answer, with the input masked (*enter* answers, *esc* cancels and
makes *ssh* fail). It sets *SSH_ASKPASS* and *SSH_ASKPASS_REQUIRE*, so it needs
OpenSSH 8.4 or later.

# CONFIGURATION

_.slurm/config_ is a TOML file at the root of the repository, tracked by git so
that the whole team shares it. *lazyslurm* finds the root with
*git rev-parse --show-toplevel*, so it works from any subfolder. The *[default]* table holds
what is shared and each *[hosts.NAME]* table one cluster. A key of a host
replaces the same key of *[default]* as a whole: arrays are replaced, never
merged. Any key may be in either table, except *host*, which is only in
*[default]*, and *target*, which is only in a host.

An unknown key, a wrong type or a bad path stops the load with a message that
names the key. Every path is relative to the repository root, and none may be
absolute or contain _.._.

*host*
	The default host. Optional when exactly one host is defined.

*target*
	Required for each host. _ALIAS:DIR_ puts the project in _DIR_ on the ssh host
	_ALIAS_ of _~/.ssh/config_, relative to the remote home unless it is absolute
	or starts with _~/_. A plain _DIR_, absolute or starting with _~/_, means
	Slurm is on this machine: *ssh* is not used and *rsync* copies between local
	folders, which is how *lazyslurm* feels the same on a login node.

*push*
	Required. The paths sent to the target. Nothing else is ever sent. Two
	paths among *push* and *keep* may not be equal or nested.

*exclude*
	Names not sent, and ignored when looking for stray files in a snapshot.
	Globs matched against each path component, with no slash. _.git_ is always
	excluded.

*keep*
	Paths that exist once per host, shared by _work/_ and every snapshot, and
	never sent: a virtualenv, the outputs, a cache.

*pull*
	Paths brought back by *pull*. Each one is a *keep* path or lies inside one.

*clean*
	What *clean* may delete, as _DIR/{job}_. See *CLEANING*.

*allow_dirty*
	_true_ lets *push*, *exec*, *submit* and *retry* work on a tree with
	uncommitted changes. The default is _false_, so that every job is a commit
	you can come back to. Only tracked files are sent. Hooks see _GIT_DIRTY=1_.
	A snapshot of such a tree is named after a commit made by *git stash create*,
	which no ref keeps, so *git gc* may remove it after a few weeks: a *retry*
	later then cannot rebuild a snapshot that is gone.

*scripts*
	Globs of what the UI offers when submitting. The command line accepts any
	pushed file.

*prelude*
	A command placed before every *exec* command, joined with *&&*. Not applied
	to scripts.

*srun*
	The options of the short allocation that *exec* uses. Without it, *exec* is
	refused. Name the GPU type: _gpu:1_ alone may land on a small slice.

*sbatch*
	Options added to every *submit*, before those of the command line.

*pre_push*, *post_push*, *pre_pull*, *post_pull*
	Hooks. See *HOOKS*.

The *srun*, *sbatch* and hook strings go to a shell as they are. *lazyslurm*
never splits them.

# SCRIPTS

A script run by *submit* can rely on this:

- It runs with its snapshot as the working directory, which looks like a normal
  checkout of the *push* set at that commit. Relative paths work.
- Its *#SBATCH* lines are untouched. *lazyslurm* only adds
  *--export=ALL,GIT_COMMIT=*_HASH_ and what you pass with *--sbatch* or the
  *sbatch* key. It adds no *--output*, *--error*, *--chdir* or *--job-name*.
- It may read *GIT_COMMIT*, and may ignore it.
- Writing under a *keep* path is safe, since the data outlives the snapshot.

And it must know that anything it writes elsewhere lives in the snapshot and is
deleted with it, after being shown as stray files. The default logs,
_slurm-ID.out_ and _slurm-ID.err_, are collected by *pull*.

# HOOKS

A hook is a shell command that *lazyslurm* runs around an operation. It knows
nothing about what a hook does: build a virtualenv after a push, copy files
local to a node before a pull, print the progress of the runs after it.

*pre_push*
	Local, in the repository root, before sending.

*post_push*
	On the target, in the snapshot after *submit* and *retry*, else in _work/_,
	after sending. It runs after every push and every submit, so it must be
	fast when nothing changed.

*pre_pull*
	On the target, in _work/_, before pulling.

*post_pull*
	Local, in the repository root, after pulling.

Hooks on the target run as *bash -lc*, so *PATH* and *module load* behave as in
a login; local ones run as *bash -c*. A hook that fails stops the command, and
its exit code and output are shown.

A hook sees *GIT_COMMIT*, *GIT_DIRTY* (_1_ only with *allow_dirty*) and
*CLUSTER*, the host it runs for. The pull hooks also see *LAZYSLURM_JOB* when a
job was asked for.

Hooks run by the UI have no terminal: their output goes to the log panel, and
*COLUMNS* holds its width, which Python's _shutil.get_terminal_size()_ and most
programs read. On the command line a hook writes straight to the terminal and
*COLUMNS* is left alone.

# CONNECTION

The commands run *ssh* with *BatchMode=yes*, so they never prompt. If the
cluster asks for a password or a code, open a master connection first with
*ControlMaster* and *ControlPersist* in _~/.ssh/config_, and *lazyslurm* reuses
it. The UI instead lets *ssh* ask through its popup. While *:* runs a command
with the UI suspended, *ssh* asks on the terminal.

A refresh sends at most one *sacct* and one *squeue* per host, every 120
seconds, with no retry loop: a failure is reported and tried again at the next
one, so the login node stays quiet.

Needed here: *git*, *rsync*, *tar* (for *retry*) and *ssh* for a host with an
alias. Needed on the target: *bash*, *rsync*, *flock*, *find*, *du*, *cp*,
the Slurm clients, accounting enabled for *sacct*, and a filesystem with
hardlinks, with _work/_, _keep/_ and the snapshots on the same one.

# ENVIRONMENT

*CLUSTER*
	The host to use when *-H* is not given.

*LAZYSLURM_JOB*
	Set by *lazyslurm* for the pull hooks of a pull of one job.

*GIT_COMMIT*, *GIT_DIRTY*, *COLUMNS*
	Set by *lazyslurm* for hooks, as described in *HOOKS*. *GIT_COMMIT* is also
	exported to the jobs.

# FILES

_.slurm/config_
	How the project uses Slurm: hosts, what to push and pull. Meant to be tracked
	by git, with any hook script the project keeps in _.slurm/_.

_.slurm/state/_
	Everything *lazyslurm* writes that is local to the machine. Not worth
	tracking: add it to the _.gitignore_ of the project.

_.slurm/state/jobs.jsonl_
	The journal of the submissions, one JSON object per line.

_.slurm/state/seen_
	Jobs already shown in a final state.

_.slurm/state/history_
	Commands typed after *:* in the UI, one per line.

_.slurm/state/logs/COMMIT/_
	The _slurm-\*.out_ and _slurm-\*.err_ files collected by *pull*.

# EXAMPLES

Send the code, run a script and follow its output:

	lazyslurm push
	lazyslurm submit --yes --note "lr sweep" slurm/train.sh --lr 3e-4
	lazyslurm logs -f 24173638_0

Submit an array with options of its own, on another host:

	lazyslurm -H gpu2 submit --yes --sbatch "--array=0-9%3 --time=02:00:00" slurm/train.sh

Resubmit what failed in the newest job, then collect the results:

	lazyslurm retry --yes
	lazyslurm pull

Run a command on an allocated node, with the code of your working tree:

	lazyslurm exec python -m pytest -x

Free the space of what is already pulled, after a look at the plan:

	lazyslurm clean
	lazyslurm clean --yes

Pull, show the progress of the runs, then clean up, from the configuration:

	post_pull = "python -m tools.progress && lazyslurm clean --yes"

# EXIT STATUS

0 on success, 1 on an error or a declined prompt, 2 on a usage error, 3 when a
confirmation was needed and not given. *exec* exits with the status of its
command.

# SAFETY

*lazyslurm* deletes on a cluster only a few things, each by rules that favor
doing nothing: the leftovers of a failed snapshot, the snapshots of *pull* and
*drop*, the outputs of *clean* and nothing else. *rsync --delete* only touches
the *push* paths inside _work/_. Everything that changes the cluster is
confirmed. A refused state, an unknown one or a failing *sacct* is read as
"still active", and active things are never deleted.

# SEE ALSO

*ssh*(1), *ssh_config*(5), *rsync*(1), *sbatch*(1), *sacct*(1), *squeue*(1),
*scancel*(1), *srun*(1), *git*(1)

# AUTHORS

Paul Chambaz, https://github.com/paulchambaz/lazyslurm
