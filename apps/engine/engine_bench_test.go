package main

import (
	"math"
	"testing"
)

// BenchmarkOpportunityDetection mide la latencia REAL del núcleo de detección de
// oportunidades del motor: dado un tick (precios ask/bid de ambos exchanges),
// cuánto tarda en calcular spreads, actualizar la media móvil del spread (Spike
// Filter), estimar fees + slippage, computar el neto y decidir si la operación es
// viable. Es exactamente la aritmética O(1) que corre el hot loop por cada tick.
//
// Ejecutar:  go test -bench=Detection -benchmem -run=^$
func BenchmarkOpportunityDetection(b *testing.B) {
	tracker := NewSpreadTracker()

	// Precios representativos de BTC/USD con una pequeña divergencia entre exchanges.
	binAsk, binBid := 73_810.50, 73_805.20
	bitAsk, bitBid := 73_790.10, 73_784.80
	const baseVolume = 0.005
	const binanceTakerFee = 0.001
	const bitsoTakerFee = 0.0065

	b.ReportAllocs()
	b.ResetTimer()

	var sink float64
	for i := 0; i < b.N; i++ {
		// 1) Mids y spreads brutos (ambas direcciones).
		binanceMid := (binAsk + binBid) / 2
		bitsoMid := (bitAsk + bitBid) / 2

		grossSpread1 := bitBid - binAsk
		grossSpread2 := binBid - bitAsk
		grossSpread := math.Max(grossSpread1, grossSpread2)
		spread := math.Abs(binanceMid - bitsoMid)

		// 2) Spike Filter: media móvil del spread + factor de anomalía.
		tracker.Add(spread)
		avg := tracker.Average()
		factor := 0.0
		if avg > 0 {
			factor = spread / avg
		}

		// 3) Fees + slippage en la dirección rentable.
		var fees float64
		if grossSpread1 > grossSpread2 {
			fees = (binAsk*binanceTakerFee + bitBid*bitsoTakerFee) * baseVolume
		} else {
			fees = (bitAsk*bitsoTakerFee + binBid*binanceTakerFee) * baseVolume
		}
		slippage := estimateSlippage(binanceMid, bitsoMid, baseVolume)

		// 4) Neto y decisión de viabilidad (incluye corte por spike).
		netOp := grossSpread*baseVolume - fees - slippage
		viable := netOp > 0.10 && factor <= SpikeBlockMultiplier

		if viable {
			sink += netOp
		}

		// Variamos el tick para evitar que el compilador elimine el cálculo.
		binAsk += 0.01
		bitBid += 0.01
	}
	_ = sink
}
