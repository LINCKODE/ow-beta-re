.PHONY: help generate lint libs build loginserver lobbyserver lobbylogic gameserver instance lobby games run run-login run-lobby run-logic run-game run-instance logic-watch game-watch instance-watch fmt vet test tidy clean

BIN := bin

help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-12s\033[0m %s\n", $$1, $$2}'

generate: ## Regenerate Go code from proto/ into gen/ (buf)
	buf generate

lint: ## Lint the protos
	buf lint

# The C++ systems the instance server links through cgo (systems/README.md "Integration").
# cmake + ninja rebuild incrementally, so this is cheap when nothing changed.
LIBS := ../systems/ecs ../systems/statescript ../systems/content ../systems/stu

libs: ## Build the C++ libraries the cgo wrappers link (systems/ecs, statescript, content, stu; incremental)
	@command -v cmake >/dev/null || { echo "make libs: cmake is not installed" >&2; exit 1; }
	@command -v ninja >/dev/null || { echo "make libs: ninja is not installed" >&2; exit 1; }
	@for d in $(LIBS); do \
		cmake -S $$d -B $$d/build -G Ninja >/dev/null && cmake --build $$d/build >/dev/null || exit 1; \
	done

build: libs loginserver lobbyserver lobbylogic gameserver instance ## Build all servers into bin/

loginserver: ## Build the login (Battle.net) server
	go build -o $(BIN)/loginserver ./cmd/loginserver

lobbyserver: ## Build the lobby gateway
	go build -o $(BIN)/lobbyserver ./cmd/lobbyserver

lobbylogic: ## Build the lobby logic backend
	go build -o $(BIN)/lobbylogic ./cmd/lobbylogic

gameserver: ## Build the R&D game server
	go build -o $(BIN)/gameserver ./cmd/gameserver

# A hash of the C++ archives the cgo wrappers link, stamped into the binary: Go does not track the archives'
# contents, so without it a rebuilt library is not relinked (T051: a stale Statescript runtime).
LIBS_HASH = $$(cat $(foreach d,$(LIBS),$(d)/build/*.a) | md5sum | cut -c1-12)

instance: libs ## Build the instance server (docs/InstanceServer.md)
	go build -ldflags "-X main.libs=$(LIBS_HASH)" -o $(BIN)/instance ./cmd/instance

# The two process groups (docs/InstanceServer.md "Process groups"): the lobby group holds the client's
# connection and stays up; the game servers start, stop and rebuild on their own. Each game server dials
# lobbylogic (retrying), so the start order doesn't matter, and the title screen's buttons follow whether the
# active one is linked. Extra flags: the *_ARGS variables, or server/<name>.args (read by scripts/watch.sh
# on every start; editing it restarts that binary).
GAMES ?= instance gameserver

lobby: loginserver lobbyserver lobbylogic ## Run the lobby group: login + lobby gateway + lobby logic (auto-rebuilt); LOGIN_ARGS/LOBBY_ARGS/LOGIC_ARGS; Ctrl-C stops the group
	@bash -c 'trap "kill \$$(jobs -p) 2>/dev/null" EXIT; \
		$(BIN)/loginserver $(LOGIN_ARGS) & \
		$(BIN)/lobbyserver $(LOBBY_ARGS) & \
		scripts/watch.sh lobbylogic $(LOGIC_ARGS) & \
		wait -n'

games: libs ## Run the game servers in GAMES (default: instance gameserver), each auto-rebuilt on Go / C++ archive / args-file changes and restarted if it dies; GAME_ARGS/INSTANCE_ARGS; Ctrl-C stops the group
	@bash -c 'trap "kill \$$(jobs -p) 2>/dev/null" EXIT; \
		for g in $(GAMES); do \
			case $$g in gameserver) a="$(GAME_ARGS)";; instance) a="$(INSTANCE_ARGS)";; *) a="";; esac; \
			scripts/watch.sh $$g $$a & \
		done; \
		wait -n'

run: build ## Run both groups (make lobby + make games) in one terminal; matches go to LOGIC_ARGS=--game-server=gameserver|instance (or !gameserver); Ctrl-C stops all
	@$(MAKE) --no-print-directory -j2 lobby games

run-login: loginserver ## Build and run the login server (pass flags with ARGS="...")
	$(BIN)/loginserver $(ARGS)

run-lobby: lobbyserver ## Build and run the lobby gateway (pass flags with ARGS="...")
	$(BIN)/lobbyserver $(ARGS)

run-logic: lobbylogic ## Build and run the lobby logic once (pass flags with ARGS="...")
	$(BIN)/lobbylogic $(ARGS)

run-game: gameserver ## Build and run the R&D game server once (pass flags with ARGS="...")
	$(BIN)/gameserver $(ARGS)

run-instance: instance ## Build and run the instance server once (pass flags with ARGS="...")
	$(BIN)/instance $(ARGS)

logic-watch: ## Run the lobby logic, rebuilding and restarting it on .go changes
	scripts/watch.sh lobbylogic $(ARGS)

game-watch: ## Run the R&D game server, rebuilding and restarting it on .go changes
	scripts/watch.sh gameserver $(ARGS)

instance-watch: libs ## Run the instance server, rebuilding and restarting it on .go changes
	scripts/watch.sh instance $(ARGS)

fmt: ## Format Go code
	go fmt ./...

vet: libs ## Run go vet
	go vet ./...

test: libs ## Run tests
	go test ./...

tidy: ## Tidy go.mod
	go mod tidy

clean: ## Remove built binaries
	rm -rf $(BIN)
