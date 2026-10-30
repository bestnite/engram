import { spawn, spawnSync, type ChildProcess } from 'node:child_process';
import { existsSync, mkdtempSync, rmSync } from 'node:fs';
import { createServer } from 'node:net';
import { tmpdir } from 'node:os';
import { dirname, join } from 'node:path';
import type { FullConfig } from '@playwright/test';

/**
 * Starts the Go server the browser suite runs against.
 *
 * Everything the run needs is created here, so `npm run test:e2e` is self-contained:
 *
 *   - a free loopback port is reserved by binding port 0 and reading the assigned port;
 *   - a throwaway directory holds the SQLite database and the media root;
 *   - the frontend is built first, because `go run` embeds `frontend/dist` and refuses to compile without it;
 *   - `go run ./cmd/engram serve` is spawned from the repository root and polled through the public `/healthz`;
 *   - the server's process group is stopped and the directory removed when the run ends, including on failure.
 *
 * `BASE_URL` is set explicitly to the test origin. The frame CSP is built from the configured base URL, and the built-in
 * default (`http://localhost:8080`) does not match a server bound to a random `127.0.0.1` port, so leaving it unset would
 * make the frame block its own runtime bundle. `SESSION_SECRET` and `ENCRYPTION_KEY` are required at startup; the latter
 * must be base64 of 32 bytes, so a fresh placeholder key is generated for each run.
 */

const HEALTH_TIMEOUT_MS = 120_000;
const SHUTDOWN_TIMEOUT_MS = 5_000;

function delay(ms: number): Promise<void> {
  return new Promise((done) => setTimeout(done, ms));
}

/** Reserves a free loopback port by letting the operating system choose one. */
function freePort(): Promise<number> {
  return new Promise((done, fail) => {
    const probe = createServer();
    probe.once('error', fail);
    probe.listen(0, '127.0.0.1', () => {
      const address = probe.address();
      if (address === null || typeof address === 'string') {
        probe.close();
        fail(new Error('could not determine the port the operating system assigned'));
        return;
      }
      const { port } = address;
      probe.close(() => done(port));
    });
  });
}

/** Signals the whole process group of the server: `go run` starts the built binary as a child of its own. */
function signalGroup(child: ChildProcess, signal: NodeJS.Signals): void {
  if (child.pid === undefined) return;
  try {
    process.kill(-child.pid, signal);
  } catch {
    // The group is already gone, or the process never became a group leader.
    try {
      child.kill(signal);
    } catch {
      // Already exited.
    }
  }
}

function waitForExit(child: ChildProcess, timeoutMs: number): Promise<boolean> {
  if (child.exitCode !== null) return Promise.resolve(true);
  return new Promise((done) => {
    const timer = setTimeout(() => done(false), timeoutMs);
    child.once('exit', () => {
      clearTimeout(timer);
      done(true);
    });
  });
}

/** Walks up from a directory until the repository root is found; the root is the directory that holds go.mod. */
function findRepoRoot(start: string): string {
  let dir = start;
  for (;;) {
    if (existsSync(join(dir, 'go.mod'))) return dir;
    const parent = dirname(dir);
    if (parent === dir) throw new Error(`could not find go.mod above ${start}`);
    dir = parent;
  }
}

export default async function globalSetup(config: FullConfig): Promise<() => Promise<void>> {
  // `config.rootDir` is the directory of the test files, not of the configuration file, so the repository root is
  // located from the marker file instead of assuming a fixed depth.
  const repoRoot = findRepoRoot(config.rootDir);
  const frontendDir = join(repoRoot, 'frontend');
  const tmpDir = mkdtempSync(join(tmpdir(), 'engram-e2e-'));
  const port = await freePort();
  const baseURL = `http://127.0.0.1:${port}`;

  const build = spawnSync(process.platform === 'win32' ? 'npm.cmd' : 'npm', ['run', 'build'], {
    cwd: frontendDir,
    env: process.env,
    stdio: 'inherit',
  });
  if (build.error) throw build.error;
  if (build.status !== 0) {
    throw new Error(`frontend build failed with status ${String(build.status)}; the Go server cannot embed frontend/dist`);
  }

  const child = spawn('go', ['run', './cmd/engram', 'serve'], {
    cwd: repoRoot,
    env: {
      ...process.env,
      HTTP_ADDR: `127.0.0.1:${port}`,
      BASE_URL: baseURL,
      DB_DRIVER: 'sqlite',
      DB_DSN: join(tmpDir, 'e2e.db'),
      AUTO_MIGRATE: '1',
      MEDIA_DIR: join(tmpDir, 'media'),
      SESSION_SECRET: 'CHANGE_ME-e2e-session-secret',
      ENCRYPTION_KEY: Buffer.alloc(32).toString('base64'),
    },
    // Its own process group lets the teardown stop `go run` and the binary it started with one signal.
    detached: true,
    stdio: ['ignore', 'pipe', 'pipe'],
  });

  const logs: string[] = [];
  const capture = (chunk: Buffer): void => {
    logs.push(chunk.toString());
    if (logs.length > 200) logs.shift();
  };
  child.stdout?.on('data', capture);
  child.stderr?.on('data', capture);

  const stop = async (): Promise<void> => {
    if (child.exitCode === null) {
      signalGroup(child, 'SIGTERM');
      if (!(await waitForExit(child, SHUTDOWN_TIMEOUT_MS))) signalGroup(child, 'SIGKILL');
    }
    rmSync(tmpDir, { recursive: true, force: true });
  };

  try {
    const deadline = Date.now() + HEALTH_TIMEOUT_MS;
    for (;;) {
      if (child.exitCode !== null) {
        throw new Error(`engram server exited with code ${String(child.exitCode)} before /healthz answered:\n${logs.join('')}`);
      }
      try {
        const response = await fetch(`${baseURL}/healthz`);
        if (response.ok) break;
      } catch {
        // The listener is not up yet.
      }
      if (Date.now() > deadline) {
        throw new Error(`engram server did not answer /healthz within ${HEALTH_TIMEOUT_MS}ms:\n${logs.join('')}`);
      }
      await delay(200);
    }
  } catch (error) {
    await stop();
    throw error;
  }

  // Playwright passes environment changes made during global setup on to the worker processes.
  process.env.ENGRAM_E2E_BASE_URL = baseURL;
  console.log(`[e2e] engram server ready at ${baseURL} (sqlite: ${join(tmpDir, 'e2e.db')})`);

  return stop;
}
