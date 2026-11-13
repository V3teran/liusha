# liusha.exe 在 Windows 上的实机冒烟（CI 用）。各阶段独立判定，最后汇总；任一阶段失败整体失败。
#   shell    沙盒自动找到 Git for Windows 的 sh（PATH 中去掉 Git 后）
#   jobs     命令执行、超时只结束这条命令、任务释放后无残留进程
#   browser  浏览器能力打开本地页面（失败时采集诊断：浏览器服务日志、作业外对照组、Chrome 原始启动）
#   service  作为 Windows 服务启停，停止时优雅收尾
#   serve    setup + serve 全部组件（PostgreSQL / Redis 为 Windows 原生版），经 MITM 代理的流量落库
# 用法：pwsh scripts/ci/windows-smoke.ps1 -Exe <liusha.exe> -Python <装了 browser-use 的 python>
#        [-PgBin <PostgreSQL bin 目录>] [-RedisDir <Redis 目录>]
param(
  [Parameter(Mandatory)] [string] $Exe,
  [Parameter(Mandatory)] [string] $Python,
  [string] $PgBin = '',
  [string] $RedisDir = ''
)
$ErrorActionPreference = 'Stop'
$work = Join-Path $env:RUNNER_TEMP 'liusha-smoke'
New-Item -ItemType Directory -Force -Path $work | Out-Null
$token = [guid]::NewGuid().ToString('N')
$headers = @{ Authorization = "Bearer $token" }
$base = 'http://127.0.0.1:18591'
$results = [ordered]@{}

function Stage($name, [scriptblock] $body) {
  Write-Host "::group::$name"
  try { & $body; $results[$name] = 'pass' }
  catch { $results[$name] = "FAIL: $($_.Exception.Message)"; Write-Host "::error::[$name] $($_.Exception.Message)" }
  Write-Host '::endgroup::'
}
function Exec-Sandbox($command, $timeout = 30, $task = 'smoke-task') {
  $body = @{ assignment_id = 'smoke'; task_id = $task; agent_run_id = 'run-1'; command = $command; timeout_seconds = $timeout } | ConvertTo-Json
  Invoke-RestMethod -Method Post -Uri "$base/exec" -Headers $headers -ContentType 'application/json' -Body $body -TimeoutSec ($timeout + 90)
}
function Wait-Http($url, $seconds = 60) {
  for ($i = 0; $i -lt $seconds * 2; $i++) {
    try { Invoke-WebRequest $url -TimeoutSec 2 | Out-Null; return } catch { Start-Sleep -Milliseconds 500 }
  }
  throw "$url 未就绪"
}
# 运行一个命令，超时即结束整棵进程树（诊断步骤不得挂住整个作业）。返回是否在时限内成功结束。
function Run-Bounded($file, [string[]] $arguments, $seconds, $tail = 40) {
  $out = Join-Path $work ("run-" + [guid]::NewGuid().ToString('N') + '.log')
  $sw = [Diagnostics.Stopwatch]::StartNew()
  $p = Start-Process -FilePath $file -ArgumentList $arguments -PassThru -NoNewWindow -RedirectStandardOutput $out -RedirectStandardError "$out.err"
  $ok = $p.WaitForExit($seconds * 1000)
  if (-not $ok) {
    & taskkill /T /F /PID $p.Id | Out-Null
    Write-Host "（$seconds 秒内未结束，已结束进程树）"
  }
  Write-Host ("[{0:N1}s] {1} {2}" -f $sw.Elapsed.TotalSeconds, (Split-Path $file -Leaf), ($arguments -join ' '))
  Get-Content $out, "$out.err" -ErrorAction SilentlyContinue | Select-Object -Last $tail | Out-Host
  return ($ok -and $p.ExitCode -eq 0)
}
# 有时限的必需步骤：未在时限内成功即失败。
function Step($label, $file, [string[]] $arguments, $seconds) {
  Write-Host "--- $label"
  if (-not (Run-Bounded $file $arguments $seconds 20)) { throw "$label 失败" }
}

# 沙盒以子进程运行（shell / jobs / browser 三个阶段共用）。
$env:Path = ($env:Path -split ';' | Where-Object { $_ -notmatch '\\Git\\' }) -join ';'
$env:LIUSHA_SANDBOX_TOKEN = $token
$env:LIUSHA_SANDBOX_LISTEN_ADDR = '127.0.0.1:18591'
$env:LIUSHA_SANDBOX_WORKSPACE_DIR = Join-Path $work 'ws'
$env:LIUSHA_SANDBOX_BROWSER_PYTHON = $Python
$env:LIUSHA_SANDBOX_INGEST_URL = ''
$env:LIUSHA_LOG_DIR = Join-Path $work 'logs'
$sandbox = Start-Process -FilePath $Exe -ArgumentList 'sandbox' -PassThru -NoNewWindow `
  -RedirectStandardOutput (Join-Path $work 'sandbox.out') -RedirectStandardError (Join-Path $work 'sandbox.err')

Stage 'shell' {
  if (Get-Command sh -ErrorAction SilentlyContinue) { throw '前提：PATH 中不应再有 sh' }
  Wait-Http "$base/healthz"
  $line = Select-String -Path (Join-Path $work 'logs\sandbox.log') -Pattern '命令 shell 已就绪' | Select-Object -First 1
  if (-not $line -or $line.Line -notmatch 'Git') { throw "未自动找到 Git Bash: $line" }
  Write-Host $line.Line
}

Stage 'jobs' {
  $r = Exec-Sandbox 'printf "%s|" "a\\b" "$((1+2))"'
  if ($r.exit_code -ne 0 -or $r.stdout -ne 'a\b|3|') { throw "命令执行异常: $($r | ConvertTo-Json -Compress)" }
  $sw = [Diagnostics.Stopwatch]::StartNew()
  $r = Exec-Sandbox 'sleep 60 | tail' 2
  if (-not $r.timed_out -or $sw.Elapsed.TotalSeconds -gt 8) { throw "超时未结束整条命令（耗时 $($sw.Elapsed.TotalSeconds)s）" }
  $r = Exec-Sandbox 'sleep 300 >/dev/null 2>&1 & p=$!; sleep 0.5; cat /proc/$p/winpid'
  $left = [int]$r.stdout.Trim()
  if (-not (Get-Process -Id $left -ErrorAction SilentlyContinue)) { throw '后台进程应保留到任务释放' }
  $rel = Invoke-RestMethod -Method Post -Uri "$base/tasks/smoke-task/release" -Headers $headers
  Start-Sleep -Seconds 1
  if (Get-Process -Id $left -ErrorAction SilentlyContinue) { throw "任务释放后仍有残留进程（$($rel | ConvertTo-Json -Compress)）" }
}

Stage 'browser' {
  $inv = Invoke-RestMethod "$base/tools" -Headers $headers
  if (-not $inv.browser.installed) { throw "浏览器能力不可用: $($inv.browser.detail)" }
  $page = Join-Path $work 'page'
  New-Item -ItemType Directory -Force -Path $page | Out-Null
  Set-Content -Path (Join-Path $page 'index.html') -Value '<html><head><title>smoke</title></head><body><h1>liusha smoke</h1></body></html>'
  $web = Start-Process -FilePath $Python -ArgumentList '-m', 'http.server', '18080', '--bind', '127.0.0.1', '--directory', $page -PassThru -NoNewWindow
  try {
    Wait-Http 'http://127.0.0.1:18080/'
    $r = Exec-Sandbox 'AGENT_ID=a browser-use open http://127.0.0.1:18080/ && AGENT_ID=a browser-use state' 180 'smoke-task'
    $logs = Get-ChildItem -Recurse -Filter 'browser-*.log' (Join-Path $work 'ws') | ForEach-Object { Get-Content $_.FullName -Tail 40 }
    if ($r.exit_code -ne 0 -or $r.stdout -notmatch 'liusha smoke') {
      Write-Host '--- 浏览器服务日志'
      $logs | Out-Host
      Write-Host '--- 本机浏览器'
      Get-ChildItem 'C:\Program Files\Google\Chrome\Application\chrome.exe', 'C:\Program Files (x86)\Microsoft\Edge\Application\msedge.exe' -ErrorAction SilentlyContinue |
        ForEach-Object { "$($_.FullName) $($_.VersionInfo.ProductVersion)" } | Out-Host
      Invoke-RestMethod -Method Post -Uri "$base/tasks/smoke-task/release" -Headers $headers | Out-Null
      throw "浏览器未能打开本地页面: $($r | ConvertTo-Json -Compress)"
    }
    # 浏览器不加载任何扩展：扩展会改写目标的请求与状态。
    if ($logs -match 'extension') { throw "浏览器服务加载了扩展: $(($logs -match 'extension') -join ' | ')" }
    $rel = Invoke-RestMethod -Method Post -Uri "$base/tasks/smoke-task/release" -Headers $headers
    if ($rel.browsers -lt 1) { throw "释放应关闭浏览器会话: $($rel | ConvertTo-Json -Compress)" }
  } finally {
    Stop-Process -Id $web.Id -Force -ErrorAction SilentlyContinue
  }
}

Stop-Process -Id $sandbox.Id -Force -ErrorAction SilentlyContinue
Start-Sleep -Seconds 1

Stage 'service' {
  $envFile = Join-Path $work 'svc.env'
  Set-Content -Path $envFile -Encoding utf8 -Value @(
    "LIUSHA_SANDBOX_TOKEN=`"$token`"",
    "LIUSHA_SANDBOX_LISTEN_ADDR=`"127.0.0.1:18591`"",
    "LIUSHA_SANDBOX_WORKSPACE_DIR=`"$((Join-Path $work 'svc-ws') -replace '\\','/')`"",
    "LIUSHA_LOG_DIR=`"$((Join-Path $work 'svc-logs') -replace '\\','/')`""
  )
  New-Service -Name liusha-smoke -StartupType Manual -BinaryPathName "`"$Exe`" sandbox -env-file `"$envFile`"" | Out-Null
  try {
    Start-Service liusha-smoke
    Wait-Http "$base/healthz"
    $sw = [Diagnostics.Stopwatch]::StartNew()
    Stop-Service liusha-smoke
    (Get-Service liusha-smoke).WaitForStatus('Stopped', [TimeSpan]::FromSeconds(60))
    Write-Host "服务停止耗时 $([int]$sw.Elapsed.TotalSeconds)s"
    if (-not (Select-String -Path (Join-Path $work 'svc-logs\sandbox.log') -Pattern 'sandbox stopped' -Quiet)) {
      throw '服务停止时未优雅收尾（日志缺 sandbox stopped）'
    }
  } finally {
    & sc.exe delete liusha-smoke | Out-Null
  }
}

if ($PgBin -and $RedisDir) {
  Stage 'serve' {
    # PostgreSQL（含 pgvector）与 Redis 在本机运行，与二进制部署相同。
    $pgdata = Join-Path $work 'pgdata'
    Step 'initdb' "$PgBin\initdb.exe" @('-D', $pgdata, '-U', 'liusha', '-A', 'trust', '-E', 'UTF8') 120
    Step '启动 PostgreSQL' "$PgBin\pg_ctl.exe" @('-D', $pgdata, '-o', '"-p 55432"', '-l', (Join-Path $work 'pg.log'), '-w', '-t', '60', 'start') 90
    Step 'createdb' "$PgBin\createdb.exe" @('-h', '127.0.0.1', '-p', '55432', '-U', 'liusha', 'liusha') 30
    Write-Host '--- 启动 Redis'
    $redis = Start-Process -FilePath (Join-Path $RedisDir 'redis-server.exe') -ArgumentList '--port', '56379' -PassThru -NoNewWindow
    Start-Sleep -Seconds 2
    if ($redis.HasExited) { throw 'Redis 启动即退出' }
    try {
      $envFile = Join-Path $work 'liusha.env'
      Remove-Item Env:LIUSHA_* -ErrorAction SilentlyContinue
      Step 'liusha setup' $Exe @('setup', '-env-file', $envFile, '-postgres-dsn', 'postgres://liusha@127.0.0.1:55432/liusha?sslmode=disable', '-redis-addr', '127.0.0.1:56379', '-workspace-dir', (Join-Path $work 'serve-ws')) 180
      $password = ((Get-Content $envFile | Select-String '^LIUSHA_ADMIN_INITIAL_PASSWORD=').Line -split '=', 2)[1].Trim('"')
      $env:LIUSHA_LOG_DIR = Join-Path $work 'serve-logs'
      $serve = Start-Process -FilePath $Exe -ArgumentList 'serve', '-env-file', $envFile -PassThru -NoNewWindow `
        -RedirectStandardOutput (Join-Path $work 'serve.out') -RedirectStandardError (Join-Path $work 'serve.err')
      try {
        Write-Host '--- 等待控制台就绪'
        Wait-Http 'http://127.0.0.1:8580/' 90
        $s = New-Object Microsoft.PowerShell.Commands.WebRequestSession
        Invoke-RestMethod -Method Post -Uri 'http://127.0.0.1:8580/api/auth/login' -WebSession $s -ContentType 'application/json' `
          -Body (@{ username = 'admin'; password = $password } | ConvertTo-Json) | Out-Null
        $tools = (Invoke-RestMethod 'http://127.0.0.1:8580/api/tools' -WebSession $s).tools
        $known = @($tools | Where-Object { $null -ne $_.installed }).Count
        if ($known -eq 0) { throw 'api 未从沙盒取到工具安装状态' }
        Write-Host "工具安装状态：$known 项已探测"
        # 经 MITM 代理访问本机页面，流量落库（代理 → Redis Stream → runner 摄入 → PostgreSQL）。
        $page = Join-Path $work 'page2'
        New-Item -ItemType Directory -Force -Path $page | Out-Null
        Set-Content -Path (Join-Path $page 'login.html') -Value '<html><body>login</body></html>'
        $web = Start-Process -FilePath $Python -ArgumentList '-m', 'http.server', '18081', '--bind', '127.0.0.1', '--directory', $page -PassThru -NoNewWindow
        try {
          Wait-Http 'http://127.0.0.1:18081/login.html'
          # 用 curl 显式走代理：.NET 的代理设置对回环地址一律直连，请求进不了代理。
          & curl.exe -sS -o NUL -x http://127.0.0.1:8588 --max-time 15 http://127.0.0.1:18081/login.html
          if ($LASTEXITCODE -ne 0) { throw "经代理访问失败（curl 退出码 $LASTEXITCODE）" }
          $seen = $false
          for ($i = 0; $i -lt 30 -and -not $seen; $i++) {
            $tr = Invoke-RestMethod 'http://127.0.0.1:8580/api/traffic' -WebSession $s
            $seen = ($tr | ConvertTo-Json -Depth 6) -match 'login\.html'
            if (-not $seen) { Start-Sleep -Seconds 1 }
          }
          if (-not $seen) { throw '经代理的流量未落库' }
        } finally { Stop-Process -Id $web.Id -Force -ErrorAction SilentlyContinue }
      } finally {
        Stop-Process -Id $serve.Id -Force -ErrorAction SilentlyContinue
        Get-Content (Join-Path $work 'serve.err') -Tail 20 -ErrorAction SilentlyContinue | Out-Host
      }
    } finally {
      Stop-Process -Id $redis.Id -Force -ErrorAction SilentlyContinue
      Run-Bounded "$PgBin\pg_ctl.exe" @('-D', $pgdata, '-m', 'fast', 'stop') 60 5 | Out-Null
    }
  }
}

Write-Host '==== 冒烟结果'
$results.GetEnumerator() | ForEach-Object { Write-Host ("{0,-8} {1}" -f $_.Key, $_.Value) }
if ($results.Values | Where-Object { $_ -ne 'pass' }) { exit 1 }
Write-Host 'Windows 冒烟通过'
