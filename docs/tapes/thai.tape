# Thai commit messages
# The font fallback (Ayuthaya/Thonburi) ships with macOS; on Linux install a
# Thai font such as Noto Sans Thai and add it to the list so tone marks render.
Output docs/gifs/thai.gif
Set Shell "bash"
Set FontSize 15
Set Width 1200
Set Height 640
Set Padding 18
Set Theme "Catppuccin Mocha"
Set TypingSpeed 35ms
Set WindowBar Colorful
Set FontFamily "JetBrains Mono, Ayuthaya, Thonburi, monospace"
Require git-quill

Hide
Type "source docs/tapes/setup.sh thai"
Enter
Sleep 500ms
Show

Type "git quill commit --lang th --emoji"
Enter
Sleep 4s
Type "a"
Sleep 1.5s
Type "git log -1 --format=%B"
Enter
Sleep 3s
