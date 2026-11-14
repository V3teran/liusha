// 生成前端生产依赖（编入控制台的 npm 包）的第三方声明，输出到 stdout。
//
//   node scripts/release/web-notices.mjs <web 目录>
//
// 依赖清单取自 pnpm licenses list --prod，许可证正文取自各包目录中的许可证文件。
import { execFileSync } from 'node:child_process'
import { readdirSync, readFileSync } from 'node:fs'
import { join } from 'node:path'

const webDir = process.argv[2] ?? 'web'
const byLicense = JSON.parse(
  execFileSync('pnpm', ['licenses', 'list', '--prod', '--json'], { cwd: webDir, encoding: 'utf8' }),
)
const pkgs = Object.values(byLicense)
  .flat()
  .flatMap((p) => p.versions.map((v, i) => ({ name: p.name, version: v, path: p.paths[i], license: p.license })))
  .sort((a, b) => a.name.localeCompare(b.name) || a.version.localeCompare(b.version))

const isLicenseFile = (n) => /^(licen[cs]e|copying|unlicense|notice)/i.test(n)
const out = [`前端依赖（${pkgs.length} 个）`]
for (const p of pkgs) {
  const files = readdirSync(p.path).filter(isLicenseFile).sort()
  const text = files.length
    ? files.map((f) => readFileSync(join(p.path, f), 'utf8').trim()).join('\n\n')
    : `（该包未附许可证文件，声明的许可证：${p.license}）`
  out.push('', '='.repeat(72), `${p.name} ${p.version}`, '-'.repeat(72), text)
}
process.stdout.write(out.join('\n') + '\n')
