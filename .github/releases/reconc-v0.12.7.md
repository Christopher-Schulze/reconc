# reconc v0.12.7

Reconc 0.12.7 lets an operator claim stay effective for a bounded time.

## Operator claims

- `reconc hook claim <repo> operator:<name> --for DURATION` records an
  operator claim that stays effective across commits until the duration ends.
  The duration ranges from one minute to eight hours, and the terminal
  confirmation names it.
- Revocation, a switch of the active session and session cleanup still end a
  timed claim before it expires. An expired timed claim authorizes nothing.
- Without `--for`, an operator claim keeps authorizing only the changes up to
  the next commit. Grants recorded by 0.12.6 stay effective after the upgrade.
- `--for` is refused together with `--revoke`, for claims outside
  `operator:*` and for a duration outside the bounds, before the confirmation
  prompt. Agent tool calls that try to record an operator claim, with or
  without `--for`, are still refused.
- A timed claim reads no Git state. While no timed grant exists, the grant file
  keeps the format that 0.12.6 reads.
- The agent skill and the embedded agent guide tell agents to ask the operator
  once for a timed claim when several protected commits are planned.
