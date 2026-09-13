// Protocol peer for adapter fault injection, not a policy or host substitute.
import { createInterface } from 'node:readline'
import { appendFileSync } from 'node:fs'

for await (const line of createInterface({ input: process.stdin })) {
  const request = JSON.parse(line)
  appendFileSync(process.env.RECONC_DSH_TEST_LOG, JSON.stringify({
    event: request.event, id: request.id, pid: process.pid, bytes: Buffer.byteLength(line),
    inputBytes: Buffer.byteLength(JSON.stringify(request.payload?.tool_input || {})),
    errorBytes: Buffer.byteLength(request.payload?.error || ''),
  }) + '\n')
  if (request.payload?.hang) continue
  const mode = process.env.RECONC_DSH_TEST_MODE
  if ((mode === 'route-setup' && request.event === 'dsh-session-start') ||
      (mode === 'route-evaluation' && request.event === 'dsh-pre-tool-use') ||
      (mode === 'route-stop' && request.event === 'dsh-stop')) continue
  if (mode === 'route-combined' && request.event) await new Promise(resolve => setTimeout(resolve, 450))
  if (request.payload?.crash) process.exit(17)
  if (process.env.RECONC_DSH_TEST_MODE?.startsWith('session-') && request.event === 'dsh-session-start') {
    await new Promise(resolve => setTimeout(resolve, 400))
  }
  if (request.payload?.delay) await new Promise(resolve => setTimeout(resolve, request.payload.delay))
  const response = { format_version: 1, type: request.type === 'shutdown' ? 'shutdown' : 'response', id: request.id, code: 0 }
  if (mode === 'decisions' && request.event === 'dsh-pre-tool-use') {
    const result = request.payload.tool_input.response
    if (result === 'denied') { response.code = 2; response.stderr = 'policy denied' }
    if (result === 'failed') { response.code = 1; response.error = 'worker failed' }
    if (result === 'warning') response.stderr = 'policy warning'
    if (result === 'block-json') response.stdout = JSON.stringify({ decision: 'block', reason: 'native block' })
    if (result === 'malformed') response.stdout = '{'
    if (result === 'unknown') response.stdout = JSON.stringify({ advisory: true, reason: 'obsolete decision' })
  }
  process.stdout.write(JSON.stringify(response) + '\n')
  if (request.type === 'shutdown') process.exit(0)
}
