unit:
    go test ./...

integration:
    #!/bin/sh
    set -eu
    cleanup() {
        exit_code=$?
        trap - EXIT
        docker compose --env-file /dev/null -p garmin-daily-integration down --volumes --remove-orphans
        exit "$exit_code"
    }
    trap cleanup EXIT
    docker compose --env-file /dev/null -p garmin-daily-integration up --detach --wait db
    TEST_DATABASE_URL='postgres://garmin_daily:dev-only-password@localhost:15432/garmin_daily?sslmode=disable' go test -tags integration -v ./...

docker-build:
    docker build --pull --tag garmin-daily:local .

show_dockerignore:
    #!/bin/sh
    set -eu
    LC_ALL=C
    export LC_ALL

    test_dir="$(mktemp -d)"
    trap 'rm -rf "$test_dir"' 0 1 2 3 15
    mkdir -p "$test_dir/context"

    docker build --file - --progress=quiet --output "type=local,dest=$test_dir/context" . >/dev/null <<-'EOF'
    # syntax=docker/dockerfile:1
    #check=skip=CopyIgnoredFile
    FROM scratch
    COPY . /context
    EOF

    find "$test_dir/context/context" -type f -print |
      sed "s#$test_dir/context/context/##" |
      sort > "$test_dir/included"

    cat "$test_dir/included"
    printf '\n%s\n' '---'
    printf 'Total files:\t%s\n' "$(wc -l < "$test_dir/included" | awk '{print $1}')"
    printf 'Total size:\t%s\n' "$(du -sh "$test_dir/context/context" | awk '{print $1}')"
