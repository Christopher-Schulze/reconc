// Managed by reconc. Project-local DeepSeek Harness policy extension.
// The host's final guard is synchronous; Go owns all policy decisions.

import { spawn } from 'node:child_process'
import { randomUUID } from 'node:crypto'
import { realpathSync } from 'node:fs'
import { dirname, isAbsolute, join, relative, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

const repo = realpathSync(join(dirname(fileURLToPath(import.meta.url)), '..'))
const routeBudgets = {"dsh-post-tool-use":{"timeoutMilliseconds":5000,"maxOutputBytes":8192,"errorPolicy":"allow","timeoutPolicy":"allow"},"dsh-post-tool-use-failure":{"timeoutMilliseconds":5000,"maxOutputBytes":8192,"errorPolicy":"allow","timeoutPolicy":"allow"},"dsh-pre-tool-use":{"timeoutMilliseconds":10000,"maxOutputBytes":8192,"errorPolicy":"block","timeoutPolicy":"block"},"dsh-session-start":{"timeoutMilliseconds":5000,"maxOutputBytes":8192,"errorPolicy":"allow","timeoutPolicy":"allow"},"dsh-stop":{"timeoutMilliseconds":5000,"maxOutputBytes":8192,"errorPolicy":"allow","timeoutPolicy":"allow","maxContinuations":1}}
const decoder = new TextDecoder('utf-8', { fatal: true })
const maxFrameBytes = 128 * 1024
const maxRequestBytes = 64 * 1024 * 1024 + 64 * 1024
const maxPendingCalls = 512

const text = (value, fallback) => typeof value === 'string' && value.trim() ? value.trim() : fallback

const workerCommand = () => {
  const wrapper = join(repo, 'tools/reconc/bin/hook')
  return process.platform === 'win32'
    ? ['sh', wrapper, '__worker_v1__', repo]
    : [wrapper, '__worker_v1__', repo]
}

const isRepoRoot = (cwd) => {
  if (typeof cwd !== 'string' || !isAbsolute(cwd)) return false
  try {
    return relative(repo, realpathSync(cwd)) === ''
  } catch {
    return false
  }
}

const executionIdentity = (exec) => {
  const agent = exec.agent
  const header = agent?.session?.header
  if (!header || !Object.isFrozen(header) || !isRepoRoot(header.cwd)) return undefined
  if (typeof header.id !== 'string' || !header.id || typeof agent.id !== 'string' || !agent.id ||
      typeof exec.callId !== 'string' || !exec.callId || typeof exec.rootCallId !== 'string' || !exec.rootCallId ||
      typeof exec.token !== 'symbol' || (exec.parent !== undefined && typeof exec.parent !== 'symbol')) return undefined
  if (typeof exec.name !== 'string' || !exec.name || !exec.arguments || typeof exec.arguments !== 'object' ||
      !Object.isFrozen(exec.arguments) || !exec.signal || typeof exec.signal.addEventListener !== 'function') return undefined
  if (exec.name === 'pwsh') return undefined
  if (exec.name === 'bash' && exec.arguments.workdir !== undefined) {
    if (typeof exec.arguments.workdir !== 'string' ||
        !isRepoRoot(resolve(header.cwd, exec.arguments.workdir))) return undefined
  }
  return {
    agent,
    agentId: agent.id,
    session: header.id,
    cwd: header.cwd,
    call: exec.callId,
    rootCall: exec.rootCallId,
    token: exec.token,
    parent: exec.parent,
    name: exec.name,
    arguments: exec.arguments,
    signal: exec.signal,
  }
}

// These providers share tool names with very different execution contracts.
// Re-read configuration at both policy entry and final dispatch, including
// renamed delegation tools. A patch on the parent does not protect a process.
const compositionConflict = (ctx, name) => {
  if (name === 'run_code') return 'Reconc cannot inspect arbitrary DSH code; set DSH_TOOLS_MODE=native and use read/write/edit/bash'
  if (['terminal_open', 'terminal_send', 'terminal_signal'].includes(name)) {
    return 'Reconc cannot bind raw terminal state; use the one-shot bash tool from the repository root'
  }
  if (!ctx.registry || typeof ctx.registry.values !== 'function') return 'Reconc cannot inspect the active DSH plugin registry'
  const providers = new Set()
  const external = new Set()
  const requested = []
  const engines = []
  let workflow = name === 'workflow'
  let delegation = ['subagent', 'fork', 'ralph'].includes(name)
  for (const runtime of ctx.registry.values()) {
    if (!runtime.fibers?.length) continue
    if (runtime.name === 'hooks-codex' || runtime.name === 'hooks-claude-code') {
      return `Reconc DSH native policy conflicts with active ${runtime.name}; disable one integration`
    }
    if (name === 'bash' && runtime.name === 'tool-bash-persistent') {
      return 'Reconc requires one-shot tool-bash; tool-bash-persistent retains unbound cwd and shell state'
    }
    for (const fiber of runtime.fibers) {
      const config = fiber.config || {}
      if (runtime.name === 'subagent-spawn-in-process' || runtime.name === 'subagent-fork-in-process') {
        providers.add(config.providerName || (runtime.name === 'subagent-spawn-in-process' ? 'spawn' : 'fork'))
      } else if (runtime.name?.startsWith('subagent-') && config.providerName) {
        external.add(config.providerName)
      }
      if (runtime.name === 'tool-subagent' && name === (config.toolName || 'subagent')) {
        delegation = true
        requested.push(config.provider)
      }
      if (runtime.name === 'tool-ralph' && name === 'ralph') requested.push(config.subagentProvider || 'spawn')
      if (runtime.name === 'tool-workflow' && name === (config.toolName || 'workflow')) workflow = true
      if (runtime.name === 'WorkerThreadWorkflowEngine') engines.push(config.provider || 'spawn')
    }
  }
  if (workflow) requested.push(...engines)
  if ((delegation || workflow) && (!requested.length || requested.some(provider => !providers.has(provider) || external.has(provider)))) {
    return 'Reconc requires a registered in-process spawn/fork provider for delegation; external children need their own protected runtime'
  }
  return undefined
}

class WorkerTransport {
  constructor() {
    this.child = undefined
    this.pending = undefined
    this.buffer = Buffer.alloc(0)
    this.serial = Promise.resolve()
    this.queued = 0
    this.nextId = 0
    this.closed = false
  }

  fail(reason) {
    const pending = this.pending
    this.pending = undefined
    if (pending) {
      clearTimeout(pending.timer)
      pending.signal?.removeEventListener('abort', pending.abort)
      pending.reject(reason)
    }
    const child = this.child
    this.child = undefined
    this.buffer = Buffer.alloc(0)
    if (child) {
      child.stdin.destroy()
      child.kill('SIGKILL')
    }
  }

  receive(chunk) {
    this.buffer = Buffer.concat([this.buffer, chunk])
    if (this.buffer.length > maxFrameBytes) {
      this.fail(new Error('Reconc worker response exceeded its frame limit'))
      return
    }
    const newline = this.buffer.indexOf(10)
    if (newline < 0) return
    const frame = this.buffer.subarray(0, newline)
    this.buffer = this.buffer.subarray(newline + 1)
    if (this.buffer.length !== 0) {
      this.fail(new Error('Reconc worker emitted unsolicited frames'))
      return
    }
    const pending = this.pending
    if (!pending) {
      this.fail(new Error('Reconc worker emitted an unsolicited response'))
      return
    }
    let response
    try {
      response = JSON.parse(decoder.decode(frame))
      if (!response || typeof response !== 'object' || Array.isArray(response) ||
          response.format_version !== 1 || response.type !== pending.type || response.id !== pending.id ||
          !Number.isSafeInteger(response.code) || response.code < 0 || response.code > 255 ||
          (response.stdout !== undefined && typeof response.stdout !== 'string') ||
          (response.stderr !== undefined && typeof response.stderr !== 'string') ||
          (response.error !== undefined && typeof response.error !== 'string') ||
          Object.keys(response).some(key => !['format_version', 'type', 'id', 'code', 'stdout', 'stderr', 'error'].includes(key))) {
        throw new Error('Reconc worker response contract drifted')
      }
    } catch (error) {
      this.fail(error)
      return
    }
    this.pending = undefined
    clearTimeout(pending.timer)
    pending.signal?.removeEventListener('abort', pending.abort)
    pending.resolve(response)
  }

  exchange(type, fields, milliseconds, signal, responseType = 'response') {
    if (!this.child || this.pending) return Promise.reject(new Error('Reconc worker is unavailable or busy'))
    if (signal?.aborted) return Promise.reject(new Error('Reconc worker request canceled'))
    const id = `${type}-${++this.nextId}`
    const frame = JSON.stringify({ format_version: 1, type, id, ...fields }) + '\n'
    if (Buffer.byteLength(frame) > maxRequestBytes) return Promise.reject(new Error('Reconc worker request exceeds its frame limit'))
    return new Promise((resolve, reject) => {
      const abort = () => this.fail(new Error('Reconc worker request canceled'))
      const timer = setTimeout(() => this.fail(new Error('Reconc worker request timed out')), milliseconds)
      this.pending = { id, type: responseType, signal, abort, timer, resolve, reject }
      signal?.addEventListener('abort', abort, { once: true })
      this.child.stdin.write(frame, error => {
        if (error) this.fail(error)
      })
    })
  }

  async start(signal) {
    if (this.child) return
    const [binary, ...args] = workerCommand()
    const child = spawn(binary, args, { cwd: repo, stdio: ['pipe', 'pipe', 'pipe'] })
    this.child = child
    this.buffer = Buffer.alloc(0)
    child.stdout.on('data', chunk => this.receive(chunk))
    child.stderr.on('data', () => {})
    child.on('error', error => this.fail(error))
    child.on('exit', () => this.fail(new Error('Reconc worker exited')))
    const response = await this.exchange('ping', {}, 2500, signal)
    if (response.code !== 0 || response.stdout || response.stderr || response.error) {
      this.fail(new Error('Reconc worker handshake was not clean'))
      throw new Error('Reconc worker handshake was not clean')
    }
  }

  run(event, payload, signal) {
    if (this.queued >= maxPendingCalls) return Promise.reject(new Error('Reconc DSH worker queue is full'))
    this.queued++
    const operation = async () => {
      if (this.closed) throw new Error('Reconc DSH extension is disposing')
      await this.start(signal)
      const budget = routeBudgets[event]
      const response = await this.exchange('request', { event, repo, payload }, budget.timeoutMilliseconds, signal)
      if (response.error || response.code !== 0) {
        throw new Error(text(response.error || response.stderr, `Reconc ${event} failed`))
      }
      return response.stdout || ''
    }
    const pending = this.serial.then(operation, operation).finally(() => { this.queued-- })
    this.serial = pending.then(() => undefined, () => undefined)
    return pending
  }

  async close() {
    this.closed = true
    await this.serial
    if (this.child) {
      try { await this.exchange('shutdown', {}, 200, undefined, 'shutdown') } catch {}
    }
    this.fail(new Error('Reconc DSH extension disposed'))
  }
}

export const inject = ['tools', 'systemPrompt']

export function apply(ctx) {
  const transport = new WorkerTransport()
  const started = new Map()
  const decisions = new Map()
  const observations = new Set()
  const stopState = new WeakMap()
  let disposing = false
  let observationOverloadReported = false

  const releaseDecision = (identity) => {
    if (decisions.get(identity.token) === identity) decisions.delete(identity.token)
    if (identity.abort) identity.signal.removeEventListener('abort', identity.abort)
  }

  const startSession = (identity) => {
    let pending = started.get(identity.session)
    if (!pending) {
      if (started.size >= maxPendingCalls) return Promise.reject(new Error('Reconc DSH session capacity exceeded'))
      pending = transport.run('dsh-session-start', {
        hook_event_name: 'session_start', session_id: identity.session, cwd: identity.cwd,
      }).catch(error => {
        started.delete(identity.session)
        throw error
      })
      started.set(identity.session, pending)
    }
    return pending
  }

  const context = ctx.systemPrompt.context({
    name: 'reconc-policy',
    order: 125,
    text: 'This repository uses Reconc policy. Run `reconc agent-intro` for its compact command guide before making changes. A denied tool call must not be retried through another route. Verify task completion with Reconc and the repository checks.',
  })

  const preStep = ctx.on('agent/pre-step', async (step, next) => {
    const header = step.agent?.session?.header
    if (disposing || !header || !Object.isFrozen(header) || !isRepoRoot(header.cwd) ||
        typeof header.id !== 'string' || !header.id || step.signal?.aborted) return { kind: 'reject' }
    try {
      await startSession({ session: header.id, cwd: header.cwd })
      if (step.signal?.aborted) return { kind: 'reject' }
      return next()
    } catch {
      return { kind: 'reject' }
    }
  })

  const pre = ctx.on('tools/pre-execute', async (exec, next) => {
    const identity = executionIdentity(exec)
    const conflict = compositionConflict(ctx, exec.name)
    if (conflict) return { kind: 'deny', reason: conflict }
    if (disposing || !identity || decisions.size >= maxPendingCalls) {
      return { kind: 'deny', reason: 'Reconc cannot bind this DSH tool call to a protected session' }
    }
    try {
      await startSession(identity)
      const output = await transport.run('dsh-pre-tool-use', {
        hook_event_name: 'tools/pre-execute',
        session_id: identity.session,
        cwd: identity.cwd,
        tool_name: identity.name,
        tool_input: identity.arguments,
        tool_call_id: identity.call,
        root_call_id: identity.rootCall,
        agent_id: identity.agent.id,
      }, exec.signal)
      if (output) {
        const decision = JSON.parse(output)
        if (decision?.decision !== 'block' || typeof decision.reason !== 'string' || !decision.reason.trim()) {
          throw new Error('Reconc returned an invalid DSH pre-tool decision')
        }
        return { kind: 'deny', reason: decision.reason.trim() }
      }
      if (exec.signal.aborted) return { kind: 'deny', reason: 'Reconc DSH tool call was canceled' }
      identity.abort = () => releaseDecision(identity)
      decisions.set(exec.token, identity)
      exec.signal.addEventListener('abort', identity.abort, { once: true })
      if (exec.signal.aborted) {
        releaseDecision(identity)
        return { kind: 'deny', reason: 'Reconc DSH tool call was canceled' }
      }
      const downstream = await next()
      if (downstream?.kind === 'deny') releaseDecision(identity)
      return downstream
    } catch (error) {
      if (identity) releaseDecision(identity)
      return { kind: 'deny', reason: text(error?.message, 'Reconc could not evaluate this DSH tool call') }
    }
  })

  const guard = ctx.tools.guard(exec => {
    const conflict = compositionConflict(ctx, exec.name)
    if (conflict) return conflict
    const identity = decisions.get(exec.token)
    if (disposing || !identity || identity.agent !== exec.agent || identity.session !== exec.agent?.session?.header?.id ||
        identity.agentId !== exec.agent?.id || identity.cwd !== exec.agent?.session?.header?.cwd ||
        identity.call !== exec.callId || identity.rootCall !== exec.rootCallId || identity.token !== exec.token ||
        identity.parent !== exec.parent || identity.name !== exec.name || identity.arguments !== exec.arguments ||
        identity.signal !== exec.signal || exec.signal?.aborted) {
      return 'Reconc DSH tool decision is missing or no longer matches this execution'
    }
    try {
      for (const key of ['token', 'callId', 'rootCallId', 'name', 'arguments', 'agent', 'parent']) {
        if (Object.hasOwn(exec, key)) Object.defineProperty(exec, key, { writable: false, configurable: false })
      }
      identity.guarded = true
      return undefined
    } catch {
      return 'Reconc could not protect this DSH tool execution identity'
    }
  })

  const result = ctx.on('tools/result', (exec, outcome) => {
    const identity = decisions.get(exec.token)
    if (identity) releaseDecision(identity)
    if (!identity?.guarded || disposing) return
    if (observations.size >= maxPendingCalls) {
      if (!observationOverloadReported) {
        process.stderr.write('reconc dsh observation: pending result limit reached\n')
        observationOverloadReported = true
      }
      return
    }
    const event = outcome.isError ? 'dsh-post-tool-use-failure' : 'dsh-post-tool-use'
    const pending = transport.run(event, {
      hook_event_name: 'tools/result',
      session_id: identity.session,
      cwd: identity.cwd,
      tool_name: identity.name,
      tool_input: identity.arguments,
      tool_call_id: identity.call,
      root_call_id: identity.rootCall,
      is_error: outcome.isError === true,
      error: outcome.error?.message,
      result_observed: true,
    }).catch(error => {
      process.stderr.write(`reconc dsh observation: ${text(error?.message, 'worker unavailable')}\n`)
    }).finally(() => observations.delete(pending))
    observations.add(pending)
  })

  const stop = ctx.on('agent/turn-stopping', async ({ agent, turn, signal }) => {
    const header = agent?.session?.header
    if (disposing || !header || !Object.isFrozen(header) || !isRepoRoot(header.cwd) ||
        typeof header.id !== 'string' || !header.id || signal?.aborted) return
    const active = stopState.get(agent)?.turn === turn
    try {
      await startSession({ session: header.id, cwd: header.cwd })
      const output = await transport.run('dsh-stop', {
        hook_event_name: 'agent/turn-stopping', session_id: header.id, cwd: header.cwd,
        agent_id: agent.id, stop_hook_active: active,
      }, signal)
      if (!output || active || signal?.aborted) return
      const decision = JSON.parse(output)
      const reason = decision?.decision === 'block' ? decision.reason :
        decision?.continue === true ? decision.additionalContext : undefined
      if (typeof reason !== 'string' || !reason.trim()) throw new Error('invalid Reconc DSH stop decision')
      agent.steer({
        id: randomUUID(), role: 'user', content: [{ type: 'text', text: reason.trim() }],
        source: { kind: 'plugin', plugin: 'reconc', form: 'notice', summary: 'Reconc requires another check' },
      })
      stopState.set(agent, { turn })
    } catch (error) {
      process.stderr.write(`reconc dsh stop observation: ${text(error?.message, 'worker unavailable')}\n`)
    }
  })

  const disposed = ctx.on('agent/disposed', ({ agent }) => {
    const session = agent?.session?.header?.id
    if (typeof session === 'string') started.delete(session)
    stopState.delete(agent)
    for (const identity of decisions.values()) {
      if (identity.agent === agent) releaseDecision(identity)
    }
  })

  const service = ctx.provide('reconcGuard', { repo })
  ctx.effect(() => async () => {
    disposing = true
    service()
    result()
    stop()
    disposed()
    pre()
    preStep()
    guard()
    context()
    for (const identity of decisions.values()) identity.signal.removeEventListener('abort', identity.abort)
    decisions.clear()
    await Promise.allSettled(observations)
    await transport.close()
    started.clear()
  }, 'reconc.dsh.worker')
}
