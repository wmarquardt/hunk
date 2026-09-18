# Searching commit history

`hunk log` opens the read-only commit history. Use a path after the command to
limit the history to commits touching that path, and use `--max-count` (or
`-n`) to control how much history is loaded. The history view reads the commit
subject and author from Git, so reviewing a commit never changes the working
tree or index.
