# reconc v0.12.9

Reconc 0.12.9 keeps update progress on one line when the terminal is resized
and keeps a hook's own result when the hook exits before reading its input.

## Update progress

- The update display follows changes to the terminal width while a transfer
  is running, keeping the progress line within the available space.

## Hook transport

- OpenCode, Kilo Code, Oh My Pi and Pi adapters hand the hook payload to the
  child process when it starts. A hook that exits before reading its input
  keeps its own exit status and output instead of being reported as a
  broken-pipe failure. Transport errors remain fail-closed.
- Transport success tests use the production time budget; deadline tests
  retain their deliberately short timeout.
