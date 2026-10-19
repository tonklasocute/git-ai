# Secrets are redacted before anything leaves the machine
Output docs/gifs/redaction.gif
Set Shell "bash"
Set FontSize 15
Set Width 1200
Set Height 640
Set Padding 18
Set Theme "Catppuccin Mocha"
Set TypingSpeed 35ms
Set WindowBar Colorful
Require git-quill

Hide
Type "source docs/tapes/setup.sh redaction"
Enter
Sleep 500ms
Show

Type "cat .env.production"
Enter
Sleep 1.5s
Type "git quill commit --dry-run --show-prompt 2>&1 | grep -v '^- ' | grep -E 'REDACTED|redacted|•|sent'"
Enter
Sleep 6s
