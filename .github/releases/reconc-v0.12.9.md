# reconc v0.12.9

Reconc 0.12.9 keeps update progress on one line when the terminal is resized
and handles early-closing hook input streams without an uncaught error.

## Update progress

- The update display follows changes to the terminal width while a transfer
  is running, keeping the progress line within the available space.

## Hook transport

- OpenCode, Kilo Code, Oh My Pi and Pi adapters handle buffered input when a
  child process exits before reading it. Transport errors remain fail-closed
  without an uncaught broken-pipe error.
- Transport success tests use the production time budget; deadline tests
  retain their deliberately short timeout.
