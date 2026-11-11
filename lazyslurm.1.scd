LAZYSLURM(1) "github.com/paulchambaz/lazyslurm" "General Commands Manual"

# NAME

*lazyslurm* - terminal UI for Slurm clusters

# SYNOPSIS

*lazyslurm* [*-hV*]

# DESCRIPTION

*lazyslurm* is a terminal UI for Slurm clusters, in the spirit of lazygit and
lazydocker. It is opened from a repository and works on it: push the code,
submit, follow, pull the results, retry and cancel.

The local repository is the source of truth. Nothing is installed on the
clusters: everything goes through *ssh* and *rsync*.

This version only draws a window. Press *q* or *ctrl-c* to quit.

# OPTIONS

*-h*
	Print a short help and exit.

*-V*
	Print the version and exit.

# FILES

_.slurm.toml_
	How the project uses Slurm: hosts, what to push and pull.

_outputs/slurms.jsonl_
	Log of the submissions, one JSON object per line.

# SEE ALSO

*ssh*(1), *rsync*(1), *sbatch*(1), *sacct*(1)

# AUTHORS

Paul Chambaz, https://github.com/paulchambaz/lazyslurm
