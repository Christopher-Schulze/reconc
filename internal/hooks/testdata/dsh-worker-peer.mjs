// Protocol peer for adapter fault injection, not a policy or host substitute.
import { createInterface } from 'node:readline'

for await (const line of createInterface({ input: process.stdin })) {
  const request = JSON.parse(line)
  const response = { format_version: 1, type: request.type === 'shutdown' ? 'shutdown' : 'response', id: request.id, code: 0 }
  process.stdout.write(JSON.stringify(response) + '\n')
  if (request.type === 'shutdown') process.exit(0)
}
