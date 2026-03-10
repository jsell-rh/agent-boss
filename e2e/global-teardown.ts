import fs from 'fs'

export default async function globalTeardown() {
  const pidFile = '/tmp/boss-e2e.pid'
  if (fs.existsSync(pidFile)) {
    const pid = parseInt(fs.readFileSync(pidFile, 'utf-8').trim(), 10)
    try {
      process.kill(pid, 'SIGTERM')
      console.log(`[E2E] Server (pid ${pid}) stopped.`)
    } catch {
      // Already dead
    }
    fs.unlinkSync(pidFile)
  }
}
