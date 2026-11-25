// 档位守门（DESIGN.md「档位」）：扫 app/ 下的 .vue、.ts、.css，档外的字号、圆角、阴影、间距直接报错。
// 档位：字号 xs 12 · meta 13 · sm 14 · base 16 · figure 20 · title 32；圆角 md 6 · card 12 · full · none；
// 阴影 line · paper · lift · none，或只画一道描边的 shadow-[inset_…]（不带逗号，不叠投影）；字重 normal、medium、semibold；间距用 Tailwind 的整数档（4 的倍数）和 0.5（2px 细缝）。
import { readdirSync, readFileSync, statSync } from 'node:fs'
import { join, relative } from 'node:path'

const root = new URL('../app/', import.meta.url).pathname
const B = String.raw`(?<=^|[\s"'\`:!(])`
const E = String.raw`(?=$|[\s"'\`/)])`
const rules = [
  [new RegExp(B + String.raw`text-(2xs|lg|xl|[2-9]xl|lead|greet|hero|\[\d[^\]]*\])` + E, 'g'), '字号不在 6 档里（xs meta sm base figure title）'],
  [new RegExp(B + String.raw`rounded(-[tblrse]{1,2})?(-(xs|sm|lg|xl|[2-9]xl|tag|\[[^\]]+\]))?` + E, 'g'), '圆角只有 rounded-md、rounded-card、rounded-full'],
  [new RegExp(B + String.raw`shadow-(2xs|xs|sm|md|lg|xl|2xl|inner|card|float|raised)` + E, 'g'), '阴影只有 shadow-line、shadow-paper、shadow-lift'],
  [new RegExp(B + String.raw`shadow-\[([^\]]+)\]` + E, 'g'), '任意阴影只能是一道 inset 描边',
    v => v.startsWith('inset_') && !v.replace(/\([^)]*\)/g, '').includes(',') && !/_\d+px_\d+px_[1-9]/.test(v.replace(/\([^)]*\)/g, ''))],
  [new RegExp(B + String.raw`-?(p|px|py|pt|pb|ps|pe|pl|pr|m|mx|my|mt|mb|ms|me|ml|mr|gap|gap-x|gap-y|space-x|space-y)-(1\.5|2\.5|3\.5)` + E, 'g'), '间距要是 4 的倍数（x.5 档是 6、10、14px）'],
  [new RegExp(B + String.raw`font-(thin|extralight|light|bold|extrabold|black)` + E, 'g'), '字重只用 400、500、600'],
  [/font-size:\s*([^;}'"]+)/g, '字号不在 6 档里', v => /^(12|13|14|16|20|32)px$|^var\(--text-(xs|meta|sm|base|figure|title)\)$/.test(v)],
  [/border-radius:\s*([^;}'"]+)/g, '圆角不在档位里', v => /^(0|6px|12px|999px|50%|var\(--(ui-radius|radius-card)\))$/.test(v)]
]

function* files(dir) {
  for (const name of readdirSync(dir)) {
    const path = join(dir, name)
    if (statSync(path).isDirectory()) yield* files(path)
    else if (/\.(vue|ts|css)$/.test(name) && !name.endsWith('.d.ts') && name !== 'fonts.css') yield path
  }
}

let bad = 0
for (const path of files(root)) {
  readFileSync(path, 'utf8').split('\n').forEach((line, i) => {
    if (/^\s*(\/\/|\/\*|\*)/.test(line)) return
    for (const [re, why, ok] of rules) {
      for (const m of line.matchAll(re)) {
        if (ok && ok(m[1].trim())) continue
        // 对象键（例如 twMergeConfig 里的 'rounded':）不是类名。
        if (/^['"]?:/.test(line.slice(m.index + m[0].length))) continue
        bad++
        console.log(`${relative(root, path)}:${i + 1}  ${m[0]}  ${why}`)
      }
    }
  })
}
if (bad) {
  console.log(`\n${bad} 处档外的值。档位见 DESIGN.md「档位」。`)
  process.exit(1)
}
console.log('档位检查通过')
