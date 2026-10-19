# git quill pr
Output docs/gifs/pr.gif
Set Shell "bash"
Set FontSize 15
Set Width 1200
Set Height 640
Set Padding 18
Set Theme "Catppuccin Mocha"
Set TypingSpeed 35ms
Set WindowBar Colorful
Set Height 760
Require git-quill

Hide
Type "source docs/tapes/setup.sh pr"
Enter
Sleep 500ms
Show

Type "git log --oneline main..HEAD"
Enter
Sleep 1.5s
Type "git quill pr"
Enter
Sleep 5s
