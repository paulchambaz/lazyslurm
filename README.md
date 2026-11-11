# lazyslurm

lazyslurm is a terminal UI for Slurm clusters, in the spirit of lazygit and lazydocker. Opened from a repository, it pushes the code, submits jobs, follows them, pulls the results, retries and cancels. The local repository is the source of truth, and nothing is installed on the clusters: everything goes through `ssh` and `rsync`.

This is an early version: the UI only draws a window, and the command line does everything the UI will.

## Installation

### Nix

```sh
nix profile install github:paulchambaz/lazyslurm
```

Or add `github:paulchambaz/lazyslurm` as an input of your flake and install its `packages.default`. The man page is installed with it.

### Manual

See the [**Building**](#building) section.

## Usage

```sh
lazyslurm
lazyslurm push [--json] [-H HOST]
lazyslurm submit [--yes] [--sbatch OPTS] [--note TEXT] [--json] [-H HOST] SCRIPT [ARGS...]
lazyslurm status [--all] [--json] [-H HOST] [JOB]
lazyslurm pull [--json] [-H HOST]
lazyslurm retry [--yes] [--json] [-H HOST] [JOB]
lazyslurm cancel [--yes] [--json] [-H HOST] JOB[_TASK]
lazyslurm logs [-f] [--err] [-H HOST] JOB[_TASK]
lazyslurm exec [-H HOST] [--] COMMAND...
lazyslurm snapshots [--json] [-H HOST]
lazyslurm drop [--yes] [--json] [-H HOST] COMMIT
```

Commands that change the cluster (`submit`, `retry`, `cancel`, `drop`) ask `[y/N]` on a terminal. Without one they do nothing and exit 3, unless `--yes` is given.

Press `q` or `ctrl-c` to quit. Everything else is in the [`man`](./lazyslurm.1.scd) page (`man lazyslurm` once installed).

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
