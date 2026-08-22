# Сборка релизного .exe одной командой:
#
#   .\build.ps1
#
# Кладёт готовый файл в dist\songrequest.exe и собирает архив с инструкцией,
# который можно сразу отправить стримеру.
#
# Параметры:
#   -Version 0.2.0   вписать конкретную версию вместо автоматической
#   -NoZip           не собирать архив, нужен только .exe
#   -SkipTests       пропустить тесты (по умолчанию они обязательны)

param(
    [string]$Version,
    [switch]$NoZip,
    [switch]$SkipTests
)

$ErrorActionPreference = "Stop"
Set-Location $PSScriptRoot

# Go мог быть установлен уже после открытия этого окна — подхватываем PATH.
if (-not (Get-Command go -ErrorAction SilentlyContinue)) {
    $env:Path = [Environment]::GetEnvironmentVariable("Path", "Machine") + ";" +
                [Environment]::GetEnvironmentVariable("Path", "User")
}
if (-not (Get-Command go -ErrorAction SilentlyContinue)) {
    throw "Go не найден. Установи его: winget install GoLang.Go"
}

# Версия: короткий хеш коммита, чтобы по логу от стримера было понятно, какая
# именно сборка у него стоит. Без git — просто дата.
if (-not $Version) {
    $commit = ""
    try { $commit = (git rev-parse --short HEAD 2>$null) } catch {}
    if ($commit) {
        $dirty = ""
        if ((git status --porcelain 2>$null)) { $dirty = "-грязная" }
        $Version = "$(Get-Date -Format 'yyyy.MM.dd')-$commit$dirty"
    } else {
        $Version = Get-Date -Format 'yyyy.MM.dd-HHmm'
    }
}

if (-not $SkipTests) {
    Write-Host "Тесты…" -ForegroundColor Cyan
    go test ./...
    if ($LASTEXITCODE -ne 0) { throw "Тесты не прошли — сборка остановлена." }
}

Write-Host "Сборка $Version…" -ForegroundColor Cyan
New-Item -ItemType Directory -Force -Path dist | Out-Null

$exe = "dist\songrequest.exe"
# -s -w выкидывают отладочные таблицы: файл меньше почти на треть.
$env:CGO_ENABLED = "0"
go build -trimpath -ldflags "-s -w -X main.version=$Version" -o $exe .
if ($LASTEXITCODE -ne 0) { throw "Сборка не удалась." }

$size = [math]::Round((Get-Item $exe).Length / 1MB, 1)
Write-Host "Готово: $exe ($size МБ, версия $Version)" -ForegroundColor Green

if (-not $NoZip) {
    $zip = "dist\songrequest-$Version.zip"
    $staging = "dist\_pack"
    Remove-Item -Recurse -Force $staging -ErrorAction SilentlyContinue
    New-Item -ItemType Directory -Force -Path $staging | Out-Null

    Copy-Item $exe $staging
    foreach ($doc in @("docs\Инструкция-Spotify.md", "README.md")) {
        if (Test-Path $doc) { Copy-Item $doc $staging }
    }

    Remove-Item $zip -ErrorAction SilentlyContinue
    Compress-Archive -Path "$staging\*" -DestinationPath $zip
    Remove-Item -Recurse -Force $staging

    Write-Host "Архив для отправки: $zip" -ForegroundColor Green
}
