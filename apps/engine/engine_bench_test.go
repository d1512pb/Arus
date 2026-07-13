package main

import (
	"math"
	"testing"
)

// BenchmarkOpportunityDetection mide la latencia REAL del núcleo de detección de
// oportunidades del motor: dado un tick (precios ask/bid de ambos exchanges),
// cuánto tarda en calcular spreads, actualizar la media móvil del spread (Spike
// Filter), estimar fees + slippage vía computeNetProfit (la fórmula única del
// motor) y decidir si la operación es viable. Es exactamente la aritmética O(1)
// que corre el hot loop por cada tick.
//
// Ejecutar:  go test -bench=Detection -benchmem -run=^$
func BenchmarkOpportunityDetection(b *testing.B) {
	tracker := NewSpreadTracker()

	// Precios representativos de BTC/USD con una pequeña divergencia entre exchanges.
	binAsk, binBid := 73_810.50, 73_805.20
	bitAsk, bitBid := 73_790.10, 73_784.80
	// Mismos parámetros por defecto que usa el motor (single source of truth): el
	// bench mide el hot path real, no una copia con números a mano.
	params := DefaultTradingParameters()
	baseVolume := params.MaxOrderSizeBTC
	binanceTakerFee := params.takerFee("Binance")
	bitsoTakerFee := params.takerFee("Bitso")

	b.ReportAllocs()
	b.ResetTimer()

	var sink float64
	for i := 0; i < b.N; i++ {
		// 1) Mids y spreads brutos (ambas direcciones).
		binanceMid := (binAsk + binBid) / 2
		bitsoMid := (bitAsk + bitBid) / 2

		grossSpread1 := bitBid - binAsk
		grossSpread2 := binBid - bitAsk
		spread := math.Abs(binanceMid - bitsoMid)

		// 2) Spike Filter: media móvil del spread + factor de anomalía.
		tracker.Add(spread)
		avg := tracker.Average()
		factor := 0.0
		if avg > 0 {
			factor = spread / avg
		}

		// 3) Fórmula única del motor (fees + slippage + neto) en la dirección rentable.
		var netOp float64
		if grossSpread1 > grossSpread2 {
			_, _, _, netOp = computeNetProfit(binAsk, bitBid, baseVolume, binanceTakerFee, bitsoTakerFee, params.SlippageRate)
		} else {
			_, _, _, netOp = computeNetProfit(bitAsk, binBid, baseVolume, bitsoTakerFee, binanceTakerFee, params.SlippageRate)
		}

		// 4) Decisión de viabilidad (incluye corte por spike).
		viable := netOp > params.MinNetProfitUSD && factor <= SpikeBlockMultiplier

		if viable {
			sink += netOp
		}

		// Variamos el tick para evitar que el compilador elimine el cálculo.
		binAsk += 0.01
		bitBid += 0.01
	}
	_ = sink
}
