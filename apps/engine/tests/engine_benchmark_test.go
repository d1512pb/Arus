package main

import (
	"math"
	"testing"
)

// escapeSink evita que el compilador elimine el resultado del hot path.
var escapeSink float64

// BenchmarkNetProfitCalculation aísla la aritmética institucional del motor
// (spread bruto − fees taker − slippage estimado) de CUALQUIER latencia de red
// o I/O. Es el hot path que decide ejecutar o descartar en cada tick.
//
// Objetivo de auditoría (hardware local, no colo): < 20 ns/op.
//
// Ejecutar (desde apps/engine):
//
//	go test -overlay tests/overlay.json -bench=BenchmarkNetProfitCalculation -benchmem -run=^$ .
func BenchmarkNetProfitCalculation(b *testing.B) {
	feeBuy := 0.001
	feeSell := 0.0065
	slip := DefaultSlippageRate
	volume := 0.005

	// Libro de precios variado: imp ide que el compilador pliegue el loop a una constante.
	book := [...][2]float64{
		{73_810.50, 73_845.20},
		{74_100.00, 74_155.75},
		{72_990.25, 73_020.10},
		{75_000.00, 75_080.50},
		{71_500.00, 71_612.00},
		{68_888.88, 68_950.00},
		{80_000.00, 80_045.00},
		{65_432.10, 65_500.00},
	}

	b.ReportAllocs()
	b.ResetTimer()

	var sink float64
	for i := 0; i < b.N; i++ {
		pr := book[i&7]
		_, _, _, net := computeNetProfit(pr[0], pr[1], volume, feeBuy, feeSell, slip)
		sink += net
	}
	escapeSink = sink
	if math.IsNaN(escapeSink) {
		b.Fatal("nan")
	}
}

// BenchmarkNetProfitBothDirections mide el costo de evaluar AMBAS direcciones
// del par (Binance→Bitso y Bitso→Binance) y elegir el neto máximo — lo que hace
// executeForSession antes de tocar wallets.
func BenchmarkNetProfitBothDirections(b *testing.B) {
	p := DefaultTradingParameters()
	fb, fs := p.takerFee("Binance"), p.takerFee("Bitso")
	vol := p.MaxOrderSizeBTC
	book := [...][4]float64{
		{60_000, 60_120, 60_110, 60_090},
		{61_000, 61_050, 61_040, 60_980},
		{59_500, 59_700, 59_680, 59_480},
		{70_000, 70_200, 70_180, 69_950},
		{55_000, 55_090, 55_080, 54_970},
		{80_000, 80_150, 80_140, 79_990},
		{62_222, 62_300, 62_290, 62_200},
		{66_666, 66_800, 66_790, 66_600},
	}

	b.ReportAllocs()
	b.ResetTimer()

	var sink float64
	for i := 0; i < b.N; i++ {
		row := book[i&7]
		_, _, _, n1 := computeNetProfit(row[0], row[1], vol, fb, fs, p.SlippageRate)
		_, _, _, n2 := computeNetProfit(row[2], row[3], vol, fs, fb, p.SlippageRate)
		if n1 >= n2 {
			sink += n1
		} else {
			sink += n2
		}
	}
	escapeSink = sink
}
