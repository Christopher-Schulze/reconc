# reconc v0.12.8

Reconc 0.12.8 keeps the update progress on one line.

## Update progress

- On a terminal the running update phase stays short of the terminal width,
  so the terminal never wraps it and the in-place redraw keeps one line. When
  the window is too narrow, the transfer speed and remaining time are left out
  first, then the byte counts; the progress bar and percentage remain.
- A terminal that does not report its size counts as 80 columns. Output
  without a terminal still prints only the finished phase lines.
- The update display is drawn by the installed binary, so the one-line
  progress appears from the first update that 0.12.8 performs.
