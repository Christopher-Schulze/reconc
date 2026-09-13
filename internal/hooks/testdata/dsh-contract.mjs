// Source contract: deepseek-ai/deepseek-harness fb2c4b9e698e30edb738bca4cf0618587db7d203.
// Cordis Registry.Runtime.name/fibers and Fiber.config; Tools.guard is synchronous.
import assert from 'node:assert/strict'
import { pathToFileURL } from 'node:url'

const module = await import(pathToFileURL(Bun.argv[2]).href)
const repo = Bun.argv[3]
const mode = Bun.argv[4]
const listeners = new Map()
const runtimes = []
let guard, cleanup
const ctx = {
  registry: { values: () => runtimes.values() },
  on(name, listener) { listeners.set(name, listener); return () => listeners.delete(name) },
  tools: { guard(listener) { guard = listener; return () => { guard = undefined } } },
  systemPrompt: { context() { return () => {} } },
  provide() { return () => {} },
  effect(factory) { cleanup = factory() },
}
const agent = { id: 'parent', session: { header: Object.freeze({ id: 'session', cwd: repo }) } }
let serial = 0
const call = (name, args = {}) => ({
  name, arguments: Object.freeze(args), agent, token: Symbol(), callId: `call-${++serial}`,
  rootCallId: `root-${serial}`, signal: new AbortController().signal,
})
const runtime = (name, config = {}) => ({ name, fibers: [{ config }] })
const pre = exec => listeners.get('tools/pre-execute')(exec, () => ({ kind: 'allow' }))
const allowed = async exec => {
  assert.equal((await pre(exec)).kind, 'allow', exec.name)
  assert.equal(guard(exec), undefined, exec.name)
  listeners.get('tools/result')(exec, { isError: false })
}

module.apply(ctx)
try {
  if (mode !== 'composition') throw new Error(`unknown contract mode ${mode}`)
  for (const name of ['run_code', 'terminal_open', 'terminal_send', 'terminal_signal', 'pwsh']) {
    assert.equal((await pre(call(name))).kind, 'deny', name)
    assert.equal(typeof guard(call(name)), 'string', name)
  }
  for (const name of ['read', 'write', 'edit', 'terminal_read', 'terminal_list', 'terminal_close']) await allowed(call(name))
  await allowed(call('bash', { command: 'pwd' }))
  runtimes.push(runtime('tool-bash-persistent'))
  assert.match((await pre(call('bash', { command: 'pwd' }))).reason, /one-shot/)
  runtimes.pop()
  const shell = call('bash', { command: 'pwd' })
  assert.equal((await pre(shell)).kind, 'allow')
  runtimes.push(runtime('tool-bash-persistent'))
  assert.match(guard(shell), /persistent/)
  runtimes.pop()

  runtimes.push(runtime('subagent-spawn-in-process', { providerName: 'local' }))
  const delegation = runtime('tool-subagent', { toolName: 'delegate', provider: 'local' })
  runtimes.push(delegation)
  await allowed(call('delegate'))
  const child = call('write')
  child.agent = { id: 'child', session: { header: Object.freeze({ id: 'child-session', cwd: repo }) } }
  child.parent = Symbol('parent')
  await allowed(child)
  for (const provider of ['dsh-sdk', 'codex', 'claude-code', 'acp', 'unknown']) {
    delegation.fibers[0].config.provider = provider
    assert.match((await pre(call('delegate'))).reason, /in-process/)
  }
  delegation.fibers[0].config.provider = 'local'
  const late = call('delegate')
  assert.equal((await pre(late)).kind, 'allow')
  delegation.fibers[0].config.provider = 'dsh-sdk'
  assert.match(guard(late), /in-process/)
  runtimes.push(runtime('tool-workflow', { toolName: 'orchestrate' }))
  const engine = runtime('WorkerThreadWorkflowEngine', { provider: 'local' })
  runtimes.push(engine)
  await allowed(call('orchestrate'))
  engine.fibers[0].config.provider = 'dsh-sdk'
  assert.match((await pre(call('orchestrate'))).reason, /in-process/)
  const ralph = runtime('tool-ralph', { subagentProvider: 'local' })
  runtimes.push(ralph)
  await allowed(call('ralph'))
  ralph.fibers[0].config.subagentProvider = 'dsh-sdk'
  assert.match((await pre(call('ralph'))).reason, /in-process/)
  runtimes.push(runtime('hooks-codex'))
  assert.match((await pre(call('read'))).reason, /conflicts/)
} finally {
  await cleanup()
}
