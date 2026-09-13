// Source contract: deepseek-ai/deepseek-harness fb2c4b9e698e30edb738bca4cf0618587db7d203.
// Advisory integration: native continuation, passive results, and source-shaped provider composition.
import assert from 'node:assert/strict'
import { pathToFileURL } from 'node:url'
import { existsSync, readFileSync } from 'node:fs'

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
  let dispatches = 0
  const answer = await listeners.get('tools/pre-execute')(exec, () => { dispatches++; return { kind: 'allow' } })
  assert.equal(answer.kind, 'allow', exec.name)
  assert.equal(dispatches, 1, exec.name)
  assert.equal(guard, undefined, 'Reconc registered a blocking final guard')
  assert.equal(Object.getOwnPropertyDescriptor(exec, 'arguments').writable, true)
  listeners.get('tools/result')(exec, { isError: false })
}

const sleep = ms => new Promise(resolve => setTimeout(resolve, ms))
const bounded = async (promise, ms = 1500) => {
  let timer
  try { return await Promise.race([promise, new Promise((_, reject) => { timer = setTimeout(() => reject(new Error('contract deadline exceeded')), ms) })]) }
  finally { clearTimeout(timer) }
}
const records = () => existsSync(process.env.RECONC_DSH_TEST_LOG)
  ? readFileSync(process.env.RECONC_DSH_TEST_LOG, 'utf8').trim().split('\n').map(JSON.parse) : []

if (mode === 'composition' || mode === 'observations' || mode === 'decision-limit' || mode.startsWith('session-') || mode.startsWith('advisory-')) {
  module.apply(ctx)
  try {
    if (mode.startsWith('advisory-')) {
      const step = () => listeners.get('agent/pre-step')({ agent }, () => ({ kind: 'enter' }))
      if (mode === 'advisory-evaluation' || mode === 'advisory-stop') await step()
      const startedAt = performance.now()
      const answer = await bounded(mode === 'advisory-setup' ? step() : mode === 'advisory-stop'
        ? listeners.get('agent/turn-stopping')({ agent }) : pre(call('read')), 1000)
      const elapsed = performance.now() - startedAt
      assert.ok(elapsed >= 400 && elapsed < 800, `shared advisory budget: ${elapsed} ms`)
      if (mode !== 'advisory-stop') assert.equal(answer.kind, mode === 'advisory-setup' ? 'enter' : 'allow')
      console.log(JSON.stringify({ mode, elapsedMilliseconds: Math.round(elapsed) }))
    } else if (mode === 'decision-limit') {
      assert.equal((await pre(call('read'))).kind, 'allow')
      const burst = Array.from({ length: 512 }, () => pre(call('read')))
      const results = await Promise.all(burst)
      assert.equal(results.filter(result => result.kind === 'allow').length, 512)
      assert.equal(results.filter(result => result.kind === 'deny').length, 0)
    } else if (mode.startsWith('session-')) {
      const controller = new AbortController()
      const step = () => listeners.get('agent/pre-step')({ agent, signal: controller.signal }, () => ({ kind: 'enter' }))
      const pending = []
      for (let i = 0; i < (mode === 'session-limit' ? 512 : 1); i++) pending.push(step())
      if (mode === 'session-limit') assert.equal((await bounded(step(), 300)).kind, 'enter')
      controller.abort()
      const settled = await bounded(Promise.all(pending), 300)
      assert.ok(settled.every(result => result.kind === 'enter'))
    } else if (mode === 'observations') {
      const exec = call('write', { file_path: 'docs/large.md', content: 'x'.repeat(1024 * 1024) })
      await allowed(exec)
      await bounded((async () => { while (!records().some(row => row.event === 'dsh-post-tool-use')) await sleep(5) })())
      const before = records().find(row => row.event === 'dsh-pre-tool-use')
      const after = records().find(row => row.event === 'dsh-post-tool-use')
      assert.equal(after.inputBytes, 2)
      assert.ok(before.bytes > 1024 * 1024)
      assert.ok(after.bytes < 1024, JSON.stringify(after))
      console.log(JSON.stringify({ preBytes: before.bytes, postBytes: after.bytes }))
      const failed = call('str_replace_editor', { command: 'create', path: 'docs/large.md', file_text: 'x'.repeat(1024 * 1024) })
      assert.equal((await pre(failed)).kind, 'allow')
      assert.equal(guard, undefined)
      listeners.get('tools/result')(failed, { isError: true, error: new Error('x'.repeat(10000)) })
      await bounded((async () => { while (!records().some(row => row.event === 'dsh-post-tool-use-failure')) await sleep(5) })())
      const failure = records().find(row => row.event === 'dsh-post-tool-use-failure')
      assert.equal(failure.inputBytes, 2)
      assert.equal(failure.errorBytes, 2048)
    } else {
      for (const name of ['run_code', 'terminal_open', 'terminal_send', 'terminal_signal', 'pwsh']) {
        await allowed(call(name))
      }
      for (const name of ['read', 'write', 'edit', 'terminal_read', 'terminal_list', 'terminal_close']) await allowed(call(name))
      await allowed(call('bash', { command: 'pwd' }))
      runtimes.push(runtime('tool-bash-persistent'))
      await allowed(call('bash', { command: 'pwd' }))
      runtimes.pop()
      const shell = call('bash', { command: 'pwd' })
      assert.equal((await pre(shell)).kind, 'allow')
      runtimes.push(runtime('tool-bash-persistent'))
      assert.equal(guard, undefined)
      shell.arguments = Object.freeze({ command: 'echo changed' })
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
        await allowed(call('delegate'))
      }
      delegation.fibers[0].config.provider = 'local'
      const late = call('delegate')
      assert.equal((await pre(late)).kind, 'allow')
      delegation.fibers[0].config.provider = 'dsh-sdk'
      assert.equal(guard, undefined)
      await allowed(late)
      runtimes.push(runtime('tool-workflow', { toolName: 'orchestrate' }))
      const engine = runtime('WorkerThreadWorkflowEngine', { provider: 'local' })
      runtimes.push(engine)
      await allowed(call('orchestrate'))
      engine.fibers[0].config.provider = 'dsh-sdk'
      await allowed(call('orchestrate'))
      const ralph = runtime('tool-ralph', { subagentProvider: 'local' })
      runtimes.push(ralph)
      await allowed(call('ralph'))
      ralph.fibers[0].config.subagentProvider = 'dsh-sdk'
      await allowed(call('ralph'))
      runtimes.push(runtime('hooks-codex'))
      await allowed(call('read'))
      runtimes.push(runtime('hooks-claude-code'))
      await allowed(call('run_code'))
      const foreign = call('write'); foreign.agent = { id: 'other', session: { header: Object.freeze({ id: 'outside', cwd: '/tmp' }) } }
      await allowed(foreign)
      const missing = call('read'); delete missing.agent
      assert.equal((await pre(missing)).kind, 'allow')
      await allowed(call('bash', { command: 'pwd', workdir: '/tmp' }))
    }
  } finally { await bounded(cleanup()) }
} else {
  const worker = new module.WorkerTransport()
  const pending = []
  const enqueue = (payload, signal, event = 'dsh-pre-tool-use') => {
    const promise = worker.run(event, payload, signal)
    pending.push(promise.catch(() => {}))
    return promise
  }
  try {
    await worker.start()
    if (mode === 'restart') {
      const old = worker.child
      worker.fail(new Error('injected cancellation'))
      await worker.start()
      const replacement = worker.child
      assert.notEqual(old.pid, replacement.pid)
      old.emit('exit', 1)
      old.emit('error', new Error('late error'))
      old.stdout.emit('data', Buffer.from('late garbage'))
      old.stdin.emit('error', new Error('late write error'))
      assert.equal(worker.child, replacement)
      await enqueue({})
    } else if (mode === 'crash') {
      await assert.rejects(enqueue({ crash: true }), /exited/)
      await enqueue({})
      const requests = records().filter(row => row.event)
      assert.equal(requests.length, 2, 'ambiguous request was replayed')
      assert.notEqual(requests[0].id, requests[1].id)
      assert.notEqual(requests[0].pid, requests[1].pid)
    } else if (mode === 'cancel' || mode === 'deadline') {
      const slow = enqueue({ delay: 400 })
      const controller = new AbortController()
      const canceled = enqueue({}, controller.signal, mode === 'deadline' ? 'dsh-post-tool-use' : 'dsh-pre-tool-use')
      if (mode === 'cancel') controller.abort()
      await bounded(assert.rejects(canceled, /canceled|timed out/), 300)
      assert.equal(worker.queue.length, 0)
      assert.equal(worker.active.done, false, 'queued cancellation waited for the active request')
      await slow
      assert.equal(records().filter(row => row.event).length, 1, 'canceled request reached the worker')
    } else if (mode === 'shutdown') {
      enqueue({ hang: true })
      for (let i = 0; i < 20; i++) enqueue({}, undefined, 'dsh-post-tool-use')
      await bounded(worker.close(), 750)
      assert.equal(worker.queued, 0)
      assert.equal(worker.queuedBytes, 0)
      assert.equal(worker.child, undefined)
      assert.equal(worker.pending, undefined)
      await assert.rejects(enqueue({}), /disposing/)
    } else if (mode === 'priority') {
      enqueue({ delay: 100 })
      for (let i = 0; i < 4; i++) enqueue({}, undefined, 'dsh-post-tool-use')
      enqueue({})
      await Promise.all(pending)
      assert.deepEqual(records().filter(row => row.event).map(row => row.event), [
        'dsh-pre-tool-use', 'dsh-pre-tool-use', ...Array(4).fill('dsh-post-tool-use'),
      ])
    } else if (mode === 'bytes') {
      const controller = new AbortController()
      enqueue({ hang: true }, controller.signal)
      for (let i = 0; i < 3; i++) enqueue({ content: 'x'.repeat(16000) }, controller.signal)
      await assert.rejects(enqueue({ content: 'x'.repeat(18000) }), /byte budget/)
      await assert.rejects(enqueue({ content: 'x'.repeat(33000) }), /byte budget/)
      assert.ok(worker.queuedBytes <= 65536)
      controller.abort()
      assert.equal(worker.queuedBytes, 0)
      assert.equal(worker.queue.length, 0)
      await bounded(Promise.all(pending))
      await enqueue({ content: 'capacity reused' })
    } else if (mode === 'count') {
      enqueue({ hang: true })
      for (let i = 1; i < 512; i++) enqueue({})
      await assert.rejects(enqueue({}), /queue is full/)
      assert.equal(worker.queued, 512)
    } else if (mode === 'json') {
      const values = [null, true, false, 1, -0, 1e-8, {}, [], Array(4), { missing: undefined, array: [undefined, null] }]
      const encoder = new TextEncoder()
      for (let code = 0; code < 65536; code += 113) values.push({ text: String.fromCharCode(code) + '\n\0"\\😀\ud800\udfff', nested: [code] })
      for (const value of values) {
        const bytes = encoder.encode(JSON.stringify(value)).length
        assert.equal(module.jsonBytes(value, bytes), bytes)
        assert.throws(() => module.jsonBytes(value, bytes - 1), /byte budget/)
      }
      const cycle = {}; cycle.self = cycle
      assert.throws(() => module.jsonBytes(cycle, 1000), /JSON data/)
      let touched = false
      assert.throws(() => module.jsonBytes({ get content() { touched = true; return 'x' } }, 1000), /accessor/)
      assert.equal(touched, false)
      const hidden = Object.defineProperty({}, 'toJSON', { value: () => { touched = true; return 'expanded' } })
      assert.throws(() => module.jsonBytes(hidden, 1000), /serialization hook/)
      assert.equal(touched, false)
      let deep = {}; for (let i = 0; i < 34; i++) deep = { deep }
      assert.throws(() => module.jsonBytes(deep, 1000), /depth limit/)
    } else throw new Error(`unknown contract mode ${mode}`)
  } finally {
    await bounded(worker.close())
    await bounded(Promise.all(pending))
  }
}
