import { test as base, expect } from '@playwright/test'
import fs from 'fs'
import { spawn } from 'child_process'
import { TEST_PORT, BASE_URL } from '../constants.ts'

export { expect, BASE_URL }

// ── API helpers ─────────────────────────────────────────────────────────────

export interface ApiHelper {
  get(path: string): Promise<Response>
  post(path: string, body: unknown, agentName?: string): Promise<Response>
  postText(path: string, body: string, agentName?: string): Promise<Response>
  put(path: string, body: unknown, agentName?: string): Promise<Response>
  patch(path: string, body: unknown, agentName?: string): Promise<Response>
  del(path: string, agentName?: string): Promise<Response>
  getJSON<T>(path: string): Promise<T>
  postJSON<T>(path: string, body: unknown, agentName?: string): Promise<T>
  putJSON<T>(path: string, body: unknown, agentName?: string): Promise<T>
}

function makeApi(baseUrl: string): ApiHelper {
  const url = (path: string) => `${baseUrl}${path}`

  const get = (path: string, extraHeaders?: Record<string, string>) => fetch(url(path), {
    headers: { Accept: 'application/json', ...extraHeaders },
  })
  const del = (path: string, agentName?: string) =>
    fetch(url(path), {
      method: 'DELETE',
      headers: agentName ? { 'X-Agent-Name': agentName } : {},
    })
  const post = (path: string, body: unknown, agentName?: string) =>
    fetch(url(path), {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json',
        ...(agentName ? { 'X-Agent-Name': agentName } : {}),
      },
      body: JSON.stringify(body),
    })
  const postText = (path: string, body: string, agentName?: string) =>
    fetch(url(path), {
      method: 'POST',
      headers: {
        'Content-Type': 'text/plain',
        ...(agentName ? { 'X-Agent-Name': agentName } : {}),
      },
      body,
    })
  const put = (path: string, body: unknown, agentName?: string) =>
    fetch(url(path), {
      method: 'PUT',
      headers: {
        'Content-Type': 'application/json',
        ...(agentName ? { 'X-Agent-Name': agentName } : {}),
      },
      body: JSON.stringify(body),
    })
  const patch = (path: string, body: unknown, agentName?: string) =>
    fetch(url(path), {
      method: 'PATCH',
      headers: {
        'Content-Type': 'application/json',
        ...(agentName ? { 'X-Agent-Name': agentName } : {}),
      },
      body: JSON.stringify(body),
    })

  const getJSON = async <T>(path: string): Promise<T> => {
    const r = await get(path)
    if (!r.ok) throw new Error(`GET ${path} → ${r.status}: ${await r.text()}`)
    return r.json() as Promise<T>
  }
  const postJSON = async <T>(path: string, body: unknown, agentName?: string): Promise<T> => {
    const r = await post(path, body, agentName)
    if (!r.ok) throw new Error(`POST ${path} → ${r.status}: ${await r.text()}`)
    return r.json() as Promise<T>
  }
  const putJSON = async <T>(path: string, body: unknown, agentName?: string): Promise<T> => {
    const r = await put(path, body, agentName)
    if (!r.ok) throw new Error(`PUT ${path} → ${r.status}: ${await r.text()}`)
    return r.json() as Promise<T>
  }

  return { get, post, postText, put, patch, del, getJSON, postJSON, putJSON }
}

// ── Server restart helper ───────────────────────────────────────────────────

export interface ServerHelper {
  restart(): Promise<void>
  dataDir: string
}

function waitForServer(url: string, timeoutMs = 30_000): Promise<void> {
  return new Promise((resolve, reject) => {
    const deadline = Date.now() + timeoutMs
    const check = () => {
      fetch(url)
        .then(() => resolve())
        .catch(() => {
          if (Date.now() > deadline) reject(new Error('Server restart timed out'))
          else setTimeout(check, 200)
        })
    }
    check()
  })
}

function makeServer(): ServerHelper {
  const dataDir = fs.readFileSync('/tmp/boss-e2e-data-dir', 'utf-8').trim()
  const binary = fs.readFileSync('/tmp/boss-e2e-binary', 'utf-8').trim()

  return {
    dataDir,
    async restart() {
      const pidFile = '/tmp/boss-e2e.pid'

      // Kill existing server
      if (fs.existsSync(pidFile)) {
        const pid = parseInt(fs.readFileSync(pidFile, 'utf-8').trim(), 10)
        try { process.kill(pid, 'SIGTERM') } catch { /* already dead */ }
        await new Promise(r => setTimeout(r, 800))
      }

      // Restart with same data dir (persistence test)
      const proc = spawn(binary, ['serve'], {
        env: { ...process.env, DATA_DIR: dataDir, COORDINATOR_PORT: String(TEST_PORT) },
        detached: false,
        stdio: 'ignore',
      })
      fs.writeFileSync(pidFile, String(proc.pid))
      await waitForServer(`http://localhost:${TEST_PORT}/spaces`)
    },
  }
}

// ── Custom test fixture ─────────────────────────────────────────────────────

type Fixtures = {
  api: ApiHelper
  server: ServerHelper
  space: string
  cleanupSpaces: string[]
}

export const test = base.extend<Fixtures>({
  api: async ({}, use) => {
    await use(makeApi(BASE_URL))
  },

  server: async ({}, use) => {
    await use(makeServer())
  },

  // A unique space name per test; automatically cleaned up in teardown.
  space: async ({ api }, use) => {
    const name = `test-${Date.now()}-${Math.random().toString(36).slice(2, 7)}`
    await api.postText(`/spaces/${encodeURIComponent(name)}/contracts`, '')
    await use(name)
    try { await api.del(`/spaces/${encodeURIComponent(name)}/`) } catch { /* ignore */ }
  },

  cleanupSpaces: async ({ api }, use) => {
    const names: string[] = []
    await use(names)
    for (const name of names) {
      try { await api.del(`/spaces/${encodeURIComponent(name)}/`) } catch { /* ignore */ }
    }
  },
})
