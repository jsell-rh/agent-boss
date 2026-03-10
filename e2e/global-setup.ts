import { execSync, spawn } from 'child_process'
import fs from 'fs'
import path from 'path'
import { fileURLToPath } from 'url'
import { TEST_PORT } from './constants.ts'

const __dirname = path.dirname(fileURLToPath(import.meta.url))
const PROJECT_ROOT = path.resolve(__dirname, '..')
const BINARY = '/tmp/boss-e2e'
const DATA_DIR = '/tmp/boss-e2e-data'

function waitForServer(url: string, timeoutMs = 30_000): Promise<void> {
  return new Promise((resolve, reject) => {
    const deadline = Date.now() + timeoutMs
    const check = () => {
      fetch(url)
        .then(() => resolve())
        .catch(() => {
          if (Date.now() > deadline) {
            reject(new Error(`Server did not start within ${timeoutMs}ms`))
          } else {
            setTimeout(check, 200)
          }
        })
    }
    check()
  })
}

export default async function globalSetup() {
  // Skip build if SKIP_BUILD=1 or binary already exists (manual pre-build)
  const skipBuild = process.env.SKIP_BUILD === '1' || fs.existsSync(BINARY)

  if (!skipBuild) {
    console.log('\n[E2E] Building frontend...')
    execSync('cd frontend && npm ci && npm run build', {
      cwd: PROJECT_ROOT,
      stdio: 'inherit',
      shell: true,
    })

    console.log('[E2E] Building Go binary...')
    execSync(`go build -o ${BINARY} ./cmd/boss/`, {
      cwd: PROJECT_ROOT,
      stdio: 'inherit',
    })
  } else {
    console.log(`\n[E2E] Using pre-built binary: ${BINARY}`)
  }

  // Prepare clean data directory
  if (fs.existsSync(DATA_DIR)) {
    fs.rmSync(DATA_DIR, { recursive: true })
  }
  fs.mkdirSync(DATA_DIR, { recursive: true })

  console.log(`[E2E] Starting server on port ${TEST_PORT}...`)
  const proc = spawn(BINARY, ['serve'], {
    env: {
      ...process.env,
      DATA_DIR,
      COORDINATOR_PORT: String(TEST_PORT),
    },
    detached: false,
    stdio: ['ignore', 'pipe', 'pipe'],
  })

  proc.stdout?.on('data', (d: Buffer) => process.stdout.write(`[server] ${d}`))
  proc.stderr?.on('data', (d: Buffer) => process.stderr.write(`[server] ${d}`))

  // Save PID and config for teardown and restart tests
  fs.writeFileSync('/tmp/boss-e2e.pid', String(proc.pid))
  fs.writeFileSync('/tmp/boss-e2e-data-dir', DATA_DIR)
  fs.writeFileSync('/tmp/boss-e2e-binary', BINARY)
  fs.writeFileSync('/tmp/boss-e2e-port', String(TEST_PORT))

  await waitForServer(`http://localhost:${TEST_PORT}/spaces`)
  console.log('[E2E] Server ready.\n')
}
