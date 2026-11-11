#!/usr/bin/env bash
# Opens lazyslurm on a throwaway project with a fake Slurm, no cluster needed.
set -eu

here=$(cd "$(dirname "$0")" && pwd)
bin=${LAZYSLURM:-$here/../bin/lazyslurm}
[ -x "$bin" ] || bin=$(command -v lazyslurm)

box=$(mktemp -d)
trap 'rm -rf "$box"' EXIT
export FAKE_SLURM=$box/slurm
mkdir -p "$FAKE_SLURM/live" "$FAKE_SLURM/state" "$box/bin" "$box/cluster" "$box/other"

cat > "$box/bin/slurm" <<'FAKE'
#!/bin/sh
d=$FAKE_SLURM
now=$(date +%s)
states() {
  if [ -e "$d/state/$1" ]; then cat "$d/state/$1"; return; fi
  age=$((now - $(cut -d'|' -f1 "$d/live/$1")))
  if [ "$age" -lt 5 ]; then echo "|PENDING"
  elif [ "$age" -lt 25 ]; then echo "|RUNNING"
  else echo "|COMPLETED"; fi
}
case $(basename "$0") in
sbatch)
  n=$(cat "$d/next" 2>/dev/null || echo 4100)
  echo $((n + 1)) > "$d/next"
  echo "$now|$(pwd)" > "$d/live/$n"
  mkdir -p "outputs/results/$n"
  awk -v n="$n" 'BEGIN { for (i = 1; i <= 5; i++) printf "step %d  loss %.3f\n", i * 2000, 2.4 / (1 + i * 0.7) }' > "slurm-$n.out"
  printf '{"job": %s, "return_iqm": 41.7}\n' "$n" > "outputs/results/$n/metrics.json"
  echo "$n"
  ;;
sacct)
  ids=$(echo "$*" | sed -n 's/.*-j \([^ ]*\).*/\1/p' | tr ',' ' ')
  for id in $ids; do
    [ -e "$d/live/$id" ] && states "$id" | sed "s/^/$id/"
  done
  ;;
squeue)
  for f in "$d"/live/*; do
    [ -e "$f" ] || continue
    id=$(basename "$f")
    states "$id" | grep -E 'PENDING|RUNNING' | while IFS= read -r row; do
      case "$*" in
      *%Z*) echo "$id${row%%|*}|$(cut -d'|' -f2 "$f")" ;;
      *) echo "$id${row%%|*}" ;;
      esac
    done
  done
  ;;
scancel)
  for id in "$@"; do
    id=${id%%_*}
    printf '|CANCELLED\n' > "$d/state/$id"
  done
  ;;
srun)
  while [ "${1#-}" != "$1" ]; do shift; done
  exec "$@"
  ;;
esac
FAKE
chmod +x "$box/bin/slurm"
for name in sbatch sacct squeue scontrol scancel srun; do ln -s slurm "$box/bin/$name"; done
export PATH=$box/bin:$PATH

project=$box/rl-demo
mkdir -p "$project/src" "$project/slurm" "$project/.slurm"
cd "$project"
git init -q
git config user.name "Ada Demo"
git config user.email "ada@example.org"
printf '/outputs/\n/.slurm/state/\n' > .gitignore
printf '[project]\nname = "rl-demo"\nversion = "0.1.0"\n' > pyproject.toml
cat > .slurm/config <<CONFIG
[default]
host    = "gpu"
push    = ["src", "slurm", "pyproject.toml"]
keep    = [".venv", "outputs"]
pull    = ["outputs/results"]
clean   = ["outputs/*/{job}"]
scripts = ["slurm/*.sh"]

[hosts.gpu]
target = "$box/cluster"
srun   = "--partition=gpu --gres=gpu:a6000:1 --time=00:10:00"

[hosts.cpu]
target = "$box/other"
srun   = "--partition=cpu --time=00:10:00"
CONFIG
cat > slurm/train.sh <<'SCRIPT'
#!/bin/bash
#SBATCH --job-name=train
#SBATCH --partition=gpu
#SBATCH --gres=gpu:a6000:1
#SBATCH --cpus-per-task=4
#SBATCH --mem=16G
#SBATCH --time=02:00:00

python -m rl.train --algo "${1:-sac}" --seed "${SLURM_ARRAY_TASK_ID:-0}"
SCRIPT
cat > slurm/eval.sh <<'SCRIPT'
#!/bin/bash
#SBATCH --job-name=eval
#SBATCH --partition=gpu
#SBATCH --gres=gpu:a6000:1
#SBATCH --time=00:30:00

python -m rl.evaluate --episodes 100
SCRIPT
printf 'LR = 3e-4\n' > src/train.py

commit() {
  git add -A
  GIT_AUTHOR_DATE=$2 GIT_COMMITTER_DATE=$2 git commit -q -m "$1"
}
state() { printf '%s\n' "${@:2}" > "$FAKE_SLURM/state/$1"; }
quiet() { "$bin" "$@" > /dev/null; }

commit "Add the training script" "2026-11-02T10:00:00"
quiet submit --yes --note "baseline sac" slurm/train.sh sac
state 4100 "|COMPLETED"

echo 'LR = 1e-4' > src/train.py
commit "Use a smaller learning rate" "2026-11-04T15:30:00"
quiet submit --yes --sbatch "--array=0-3" --note "lr sweep" slurm/train.sh sac
state 4101 "_0|COMPLETED" "_1|COMPLETED" "_2|COMPLETED" "_3|FAILED"

echo 'SCHEDULE = "cosine"' >> src/train.py
commit "Add a cosine schedule" "2026-11-07T09:10:00"
quiet submit --yes --note "cosine" slurm/train.sh td3
state 4102 "|COMPLETED"

echo 'EPISODES = 100' >> src/train.py
commit "Add an evaluation script" "2026-11-09T18:45:00"
quiet submit --yes --note "eval of the cosine run" slurm/eval.sh
state 4103 "|CANCELLED"

echo 'BATCH = 512' >> src/train.py
commit "Raise the batch size" "2026-11-10T11:20:00"
quiet submit --yes --sbatch "--array=0-5%3" --note "batch 512" slurm/train.sh sac
state 4104 "_0|COMPLETED" "_1|COMPLETED" "_2|RUNNING" "_3|RUNNING" "_4|RUNNING" "_5|PENDING"

"$bin" "$@"
