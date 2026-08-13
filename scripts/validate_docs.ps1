param(
    [string]$Root = (Split-Path -Parent $PSScriptRoot)
)

$ErrorActionPreference = 'Stop'
$markdownFiles = Get-ChildItem -LiteralPath $Root -Recurse -File -Filter '*.md' -Force |
    Where-Object { $_.FullName -notmatch '[\\/](node_modules|\.git)[\\/]' }
$missing = [System.Collections.Generic.List[string]]::new()
$legacy = [System.Collections.Generic.List[string]]::new()

foreach ($file in $markdownFiles) {
    $content = Get-Content -LiteralPath $file.FullName -Raw
    foreach ($match in [regex]::Matches($content, '!?(?:\[[^\]]*\])\(([^)]+)\)')) {
        $target = $match.Groups[1].Value.Trim().Trim('<', '>')
        if ($target -match '^(https?:|mailto:|#)') { continue }
        $pathOnly = ($target -split '#', 2)[0]
        if ([string]::IsNullOrWhiteSpace($pathOnly)) { continue }
        $decoded = [Uri]::UnescapeDataString($pathOnly)
        $resolved = Join-Path -Path $file.DirectoryName -ChildPath $decoded
        if (-not (Test-Path -LiteralPath $resolved)) {
            $relative = [IO.Path]::GetRelativePath($Root, $file.FullName)
            $missing.Add("${relative}: $target")
        }
    }
    $legacyCheckContent = $content
    if ($file.Name -in @('README.md', 'README.zh-CN.md')) {
        $legacyCheckContent = $legacyCheckContent -replace '(?ms)^## (?:Acknowledgements|致谢)\s*$.*\z', ''
    }
    if ($legacyCheckContent -match 'images/ccload|ccload-dashboard|ccload-logs|github\.com/caidaoli/ccLoad') {
        $legacy.Add([IO.Path]::GetRelativePath($Root, $file.FullName))
    }
}

if ($missing.Count -gt 0) {
    Write-Error ("Missing local documentation targets:`n" + ($missing -join "`n"))
}
if ($legacy.Count -gt 0) {
    Write-Error ("Legacy product documentation references remain:`n" + ($legacy -join "`n"))
}

Write-Output "Validated $($markdownFiles.Count) Markdown files: local links and images resolve."
