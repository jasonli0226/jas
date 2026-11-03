# Record with: vhs demo.tape
# Needs vhs, ttyd and ffmpeg, a built bin/jas, and OPENROUTER_API_KEY in the
# environment (handed to jas as JAS_API_KEY in the hidden setup; never shown).
# Makes real model calls on a throwaway copy of bench/fixture.
Output docs/demo.gif
Set Shell "bash"
Set FontSize 15
Set Width 1200
Set Height 720
Set TypingSpeed 35ms

# Setup (not recorded): a fresh fixture repo, an isolated jas home with a
# minimal config, the built binary on PATH.
Hide
Type `export JAS_API_KEY="$OPENROUTER_API_KEY" PATH="$PWD/bin:$PATH" DEMO=$(mktemp -d) && export HOME=$(mktemp -d) JAS_HOME=$(mktemp -d) && printf '{"provider":"openrouter","model":"deepseek/deepseek-v4-flash"}' > "$JAS_HOME/config.json" && cp -R bench/fixture/. "$DEMO" && cd "$DEMO" && git init -q && git add -A && git -c user.name=demo -c user.email=demo@example.com commit -qm init && clear`
Enter
Sleep 1s
Show

# The inline chat: ask for a change and watch it work.
Type "jas"
Enter
Sleep 2s
Type "add a header comment to main.go explaining the retry logic"
Enter
Sleep 60s
Type "/quit"
Enter
Sleep 1s

# The change is an ordinary diff in the repo.
Type "git diff --stat"
Enter
Sleep 3s
