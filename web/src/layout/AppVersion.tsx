/** 展示用的版本号：以数字开头的加 v（与发布标签同形，v0.15.0）；dev 之类的原样显示。 */
export function formatVersion(version: string): string {
  return /^\d/.test(version) ? `v${version}` : version
}

/**
 * 侧栏品牌行里的版本号。取自 GET /version——与 liusha version 打印的是同一个值；前端不自带
 * 版本号，问运行中的程序最准：升级后浏览器若还留着旧的前端，也不会把旧版本号当成现在跑的版本。
 */
export function AppVersion({ version }: { version: string }) {
  return (
    <span className="truncate font-mono text-xs text-text-tertiary" title={`liusha ${version}`}>
      <span className="sr-only">版本 </span>
      {formatVersion(version)}
    </span>
  )
}
