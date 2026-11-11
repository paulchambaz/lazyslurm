# lazyslurm

lazyslurm is a terminal UI and a command line for Slurm clusters, in the spirit of lazygit and lazydocker. Opened from a git repository, it pushes the code, submits jobs, follows them, pulls the results, retries and cancels. Nothing is installed on the cluster: everything goes through `ssh` and `rsync`.

A job runs in an immutable snapshot of the commit you submitted, not in the folder you keep editing, so a push or another submission never changes a job that is queued or running. Snapshots are hardlinks, so they cost almost nothing, and they disappear once their jobs are over and their results are back. The local repository is the source of truth.

The UI and the command line do the same things: the UI is built on the same functions, so a script or an agent can do all that you do by hand.

## Installation

### Nix

```sh
nix profile install github:paulchambaz/lazyslurm
```

Or add `github:paulchambaz/lazyslurm` as an input of your flake and install its `packages.default`. The man page is installed with it.

### Manual

See the [**Building**](#building) section.

## Usage

Write `.slurm/config` at the root of your repository:

```toml
[default]
host    = "mycluster"
push    = ["src", "slurm", "pyproject.toml"]
keep    = [".venv", "outputs"]
pull    = ["outputs/results"]
scripts = ["slurm/*.sh"]

[hosts.mycluster]
target = "mycluster:myproject"
```

`mycluster` is a host of your `~/.ssh/config` and `myproject` is the folder lazyslurm owns there. Add `.slurm/state/` to your `.gitignore`, then commit and run `lazyslurm`. The panels are hosts, jobs and snapshots on the left, the details and a log on the right, and the keys are one letter:

| Key | Action |
| --- | --- |
| `P` | push the code |
| `s` | submit a script, picked from `scripts` |
| `p` | pull the results, of the selected job in the Jobs panel |
| `r` `c` | retry the failed tasks, cancel a job |
| `L` | read the log of a job |
| `:` | run a command on a node, as `exec` does |
| `d` | drop a snapshot |
| `C` | clean the outputs already pulled |
| `0`..`4` `tab` | go to a panel, or to the next one |
| `?` | the full list of keys |
| `q` | quit |

The same, one step at a time:

```sh
lazyslurm push
lazyslurm submit --yes slurm/train.sh
lazyslurm status
lazyslurm pull
```

The commands that change the cluster (`submit`, `retry`, `cancel`, `drop`, `clean`) ask `[y/N]` on a terminal. Without one they do nothing and exit 3, unless `--yes` is given. `--json` prints JSON Lines, and `-H HOST` chooses the host.

```sh
lazyslurm submit --yes --sbatch "--array=0-9%3" --note "lr sweep" slurm/train.sh --lr 3e-4
lazyslurm logs -f 24173638_0
lazyslurm retry --yes
lazyslurm exec python -m pytest -x
lazyslurm snapshots
```

If `ssh` asks for a passphrase, a password or a code, the UI shows a small masked popup, like lazygit does. The commands never prompt: open a `ControlMaster` first if the cluster needs it. Everything is detailed in the [`man`](./lazyslurm.1.scd) page (`man lazyslurm` once installed).

## How it works

On each host, lazyslurm owns one folder, the target:

```
work/        a mirror of the push paths, the only thing rsync talks to
<commit>/    a snapshot of work/ for one commit, made with hardlinks
keep/        the one real copy of the venv, the outputs, the caches
```

In `work/` and in every snapshot, each `keep` path is a symlink into `keep/`, so a script that writes `outputs/x` writes outside the snapshot and its results outlive it. Fifteen commits that share a 1 GB file hold it once. `pull` brings the results back, copies the Slurm logs, and deletes the snapshots whose jobs are all final, unless a job wrote a file there that nothing else holds: the snapshot is then kept and the file listed. If `sacct` or `squeue` fails, nothing is deleted.

A script needs nothing from lazyslurm: no library, no header, no special folder. It runs in a folder that looks like a normal checkout, with its `#SBATCH` lines untouched, and may read `GIT_COMMIT`.

By default `push`, `submit`, `exec` and `retry` refuse a tree with uncommitted changes, so that every job is a commit you can come back to. `allow_dirty = true` lifts that for the ones who want it.

## Configuration

Everything but `push` and a `target` is optional. A key of `[hosts.NAME]` replaces the same key of `[default]`.

- `push`, `keep` and `pull` are the paths sent, shared once per host, and brought back. A `pull` path is a `keep` path or inside one.
- `exclude` names what is not sent, and `scripts` globs what the UI offers when submitting.
- `srun` and `prelude` are the options and the setup of the `exec` command, and `sbatch` is added to every `submit`. Name the GPU type in `srun`.
- `clean` lists what `lazyslurm clean` may delete on the cluster, as `outputs/*/{job}`: the subfolders named after a Slurm job id. A run is deleted only when its job is final, is in your journal, and an rsync dry run shows it is already in your repository, so the worst case is to delete what you have. Run it by hand first, then from `post_pull`.
- `pre_push`, `post_push`, `pre_pull` and `post_pull` are hooks: shell commands that run around the operation, on the cluster for `post_push` and `pre_pull`, here for the others. They see `GIT_COMMIT`, `CLUSTER` and, in the UI, `COLUMNS`, the width of the log panel. `pull JOB` also gives `LAZYSLURM_JOB`.

A configuration that does it all:

```toml
[default]
host      = "mycluster"
push      = ["src", "slurm", "pyproject.toml", "uv.lock"]
exclude   = ["__pycache__"]
keep      = [".venv", "outputs"]
pull      = ["outputs/logs", "outputs/weights"]
clean     = ["outputs/*/{job}"]
scripts   = ["slurm/*.sh"]
prelude   = "source .venv/bin/activate"
post_push = "bash slurm/setup.sh"
post_pull = "python -m tools.progress && lazyslurm clean --yes"

[hosts.mycluster]
target = "mycluster:myproject"
srun   = "--partition=gpu --gres=gpu:a6000:1 --cpus-per-task=4 --mem=16G --time=00:10:00"
```

`target` is `ALIAS:DIR` for a cluster behind ssh, or an absolute `DIR` when you are on the login node.

## Requirements

Here: `git`, `rsync`, `tar` and `ssh`. On the cluster: `bash`, `rsync`, `flock`, `find`, `du`, `cp`, the Slurm clients, accounting enabled for `sacct`, and a filesystem with hardlinks. The popup for passwords needs OpenSSH 8.4 or later.

## Building

### Nix

```sh
git clone https://github.com/paulchambaz/lazyslurm.git
cd lazyslurm
nix develop # a shell with every dev tool
nix build
just --list # the dev commands
```

### Manual

You need `go`, `scdoc` for the man page and `just` for the dev commands.

```sh
just build
just test
just lint
just install # binary and man page in ~/.local
```

## Contribution

Contributions are welcome: feature suggestions, bug reports or code, through issues and pull requests on GitHub.

## License

<a href="https://www.gnu.org/licenses/gpl-3.0.en.html">
  <img src="https://www.gnu.org/graphics/gplv3-127x51.png" alt="GNU GPLv3" />
</a>

lazyslurm is free software. You can use, study, modify and distribute it under the
terms of the [GNU General Public License](https://www.gnu.org/licenses/gpl-3.0.en.html),
version 3.
