# Record with: vhs demo.tape  (requires https://github.com/charmbracelet/vhs)
Output demo.gif
Set Shell "bash"
Set FontSize 16
Set Width 1100
Set Height 600

Type "jas --version"
Enter
Sleep 1s
Type "jas run --approve=mutating 'add a header comment to main.go explaining the retry logic'"
Enter
Sleep 3s
Type "y"
Enter
Sleep 15s
Type "git diff --stat"
Enter
Sleep 2s
