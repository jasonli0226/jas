# Record with: vhs demo.tape
# Needs vhs, ttyd and ffmpeg, a Go toolchain, and a stored OpenRouter key
# (`jas auth add openrouter`). The key is copied into a throwaway JAS_HOME in
# the hidden setup; never shown. Makes real model calls on a throwaway copy of
# bench/fixture.
Output docs/demo.gif
Set Shell "bash"
Set FontSize 15
Set Width 1200
Set Height 720
Set TypingSpeed 35ms

# Setup (not recorded): the built binary with a real version, a fresh fixture
# repo, an isolated jas home (config plus the stored auth key) and HOME.
# Note: never write a literal "key=value" env assignment for the API key
# here — a redactor rewrites it on save. The auth copy below avoids one.
Hide
Type `REAL_HOME=$HOME && go build -trimpath -ldflags "-s -w -X main.version=$(git describe --tags --abbrev=0)" -o bin/jas ./cmd/jas && export PATH="$PWD/bin:$PATH" DEMO=$(mktemp -d)/pingsvc && mkdir -p "$DEMO" && export JAS_HOME=$(mktemp -d) HOME=$(mktemp -d) && printf '{"provider":"openrouter","model":"deepseek/deepseek-v4-flash"}' > "$JAS_HOME/config.json" && install -m 600 "$REAL_HOME"/.jas/auth.json "$JAS_HOME/auth.json" && cp -R bench/fixture/. "$DEMO" && cd "$DEMO" && git init -q && git add -A && git -c user.name=demo -c user.email=demo@example.com commit -qm init && clear`
Enter
Sleep 2s
Show

# The inline chat: ask for a change and watch it work.
Type "jas"
Enter
Sleep 2s
Type "extract the retry loop in main.go into a Retry helper in pkg/api with unit tests, and use it from main"
Enter
Sleep 30s

# Verify it yourself, without leaving the chat.
Type "!go test ./..."
Enter
Sleep 4s
Type "/quit"
Enter
Sleep 2s

# The change is an ordinary diff in the repo.
Type "git diff --stat"
Enter
Sleep 4s
