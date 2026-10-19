# git quill commit: generate, regenerate, choose, accept
Output docs/gifs/commit.gif
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
Type "source docs/tapes/setup.sh commit"
Enter
Sleep 500ms
Show

Type "git status --short"
Enter
Sleep 1.2s
Type "git quill commit -n 3"
Enter
Sleep 3.5s
Type "r"
Sleep 3.5s
Type "c"
Sleep 1.2s
Down
Sleep 700ms
Down
Sleep 900ms
Enter
Sleep 1.2s
Type "a"
Sleep 1.5s
Type "git log --oneline -1"
Enter
Sleep 2.5s
