# git quill review
Output docs/gifs/review.gif
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
Type "source docs/tapes/setup.sh review"
Enter
Sleep 500ms
Show

Type "git quill review"
Enter
Sleep 5s
Type "git quill review --strict >/dev/null; echo exit=$?"
Enter
Sleep 3s
