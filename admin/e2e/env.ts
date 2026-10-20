import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'

// 管理后台地址和老板账号由 scripts/dev.sh up 写入 .dev/e2e.json，这里是唯一的读取处。
const path = fileURLToPath(new URL('../../.dev/e2e.json', import.meta.url))

function load(): { baseURL: string; owner: { account: string; password: string } } {
  try {
    return JSON.parse(readFileSync(path, 'utf8'))
  } catch {
    throw new Error(`${path} not found: run scripts/dev.sh up first`)
  }
}

export const env = load()
