# run_tests.ps1 — Suite definitiva de pruebas + benchmarks del motor Arus (Windows).
#
# Uso:
#   cd apps/engine/tests
#   .\run_tests.ps1
#
# Solo benchmarks:
#   go test -overlay tests/overlay.json -bench=BenchmarkNetProfit -benchmem -run=^$ .

$ErrorActionPreference = "Stop"
$EngineDir = Split-Path -Parent $PSScriptRoot
Set-Location $EngineDir

Write-Host "==> [1/3] Unit tests (raiz del motor)"
go test -v -count=1 .

Write-Host ""
Write-Host "==> [2/3] Suite tests/ via overlay (stress + credit + params + adverse)"
go test -overlay tests/overlay.json -v -count=1 -run "TestStress|TestCredit|TestParams|TestAdverse" .

Write-Host ""
Write-Host "==> [3/3] Benchmarks de rentabilidad neta (ns/op)"
go test -overlay tests/overlay.json -bench=BenchmarkNetProfit -benchmem -benchtime=1s -run "^$" .

Write-Host ""
Write-Host "==> Done. Evidencia: tests/STRESS_TEST_REPORT.md"
