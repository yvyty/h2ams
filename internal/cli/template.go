package cli

// configTemplate is written by `h2ams init`. It must always pass config.Parse.
const configTemplate = `# h2ams configuration
# Docs: run "h2ams --help"
version: 1

# Environment started by "h2ams up" when none is given.
default_environment: dev

environments:
  dev:
    apps: [api, web]

apps:
  api:
    command: go run ./cmd/api
    cwd: ./api
    env:
      PORT: "8080"
    restart:
      policy: on-failure   # always | on-failure | never
      max_attempts: 5
      backoff: 1s

  web:
    command: npm run dev
    cwd: ./web
    restart:
      policy: always

# A container app: set docker instead of command.
#  db:
#    docker:
#      image: postgres:16
#      ports: ["5432:5432"]
#      volumes: ["./data:/var/lib/postgresql/data"]
#    env:
#      POSTGRES_PASSWORD: dev

# Web dashboard on http://127.0.0.1:9999 (status, actions, live logs).
# dashboard:
#   enabled: true
#   port: 9999

# Desktop notification when an app crash-loops or gives up.
# notifications:
#   crash_loop: true
`
