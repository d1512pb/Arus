#!/usr/bin/env bash
# run_tests.sh — Suite definitiva de pruebas + benchmarks del motor Arus.
#
# Uso:
#   cd apps/engine/tests
#   chmod +x run_tests.sh
#   ./run_tests.sh
#
# Pruebas futuras:
#   1. Añade *_test.go en tests/ con package main
#   2. Regístralo en overlay.json
#   3. go test -overlay tests/overlay.json -v -run TestTuNombre .
#
# Benchmarks (latencia matemática aislada de red):
#   go test -overlay tests/overlay.json -bench=BenchmarkNetProfit -benchmem -run=^$ .

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ENGINE_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"
cd "$ENGINE_DIR"

echo "==> [1/3] Unit tests (raíz del motor)"
go test -v -count=1 .

echo ""
echo "==> [2/3] Suite tests/ via overlay (stress + credit + params + adverse)"
go test -overlay tests/overlay.json -v -count=1 \
  -run 'TestStress|TestCredit|TestParams|TestAdverse' .

echo ""
echo "==> [3/3] Benchmarks de rentabilidad neta (ns/op)"
go test -overlay tests/overlay.json -bench=BenchmarkNetProfit -benchmem -benchtime=1s -run=^$ .

echo ""
echo "==> Done. Evidencia: tests/STRESS_TEST_REPORT.md"
