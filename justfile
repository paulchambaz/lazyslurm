run *ARGS:
  go run ./src {{ ARGS }}

build:
  mkdir -p bin && go build -o bin/lazyslurm ./src

test:
  go test ./...

fmt:
  gofmt -w .

lint:
  test -z "$(gofmt -l .)"
  go vet ./...

man:
  scdoc < lazyslurm.1.scd > lazyslurm.1

install prefix=(env_var('HOME') + '/.local'): build man
  install -Dm755 bin/lazyslurm {{ prefix }}/bin/lazyslurm
  install -Dm644 lazyslurm.1 {{ prefix }}/share/man/man1/lazyslurm.1
