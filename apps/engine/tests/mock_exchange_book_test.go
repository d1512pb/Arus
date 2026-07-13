package main

import "sync"

// mockExchangeBook simula libros mutables de Binance/Bitso (como los WebSockets reales)
// para inyectar escenarios de estrés sin tocar la lógica de negocio del motor.
type mockExchangeBook struct {
	mu                   sync.Mutex
	binAsk, binBid       float64
	bitAsk, bitBid       float64
	binAskQty, binBidQty float64
	bitAskQty, bitBidQty float64
}

func newMockExchangeBook() *mockExchangeBook {
	return &mockExchangeBook{
		binAsk: 60_000, binBid: 59_990,
		bitAsk: 60_010, bitBid: 60_000,
		binAskQty: 2, binBidQty: 2, bitAskQty: 2, bitBidQty: 2,
	}
}

func (m *mockExchangeBook) snapshot() pairView {
	m.mu.Lock()
	defer m.mu.Unlock()
	return pairView{
		BinAsk: m.binAsk, BinBid: m.binBid,
		BitAsk: m.bitAsk, BitBid: m.bitBid,
		BinAskQty: m.binAskQty, BinBidQty: m.binBidQty,
		BitAskQty: m.bitAskQty, BitBidQty: m.bitBidQty,
	}
}

func (m *mockExchangeBook) setBinance(ask, bid float64) {
	m.mu.Lock()
	m.binAsk, m.binBid = ask, bid
	m.mu.Unlock()
}

func (m *mockExchangeBook) setBitso(ask, bid float64) {
	m.mu.Lock()
	m.bitAsk, m.bitBid = ask, bid
	m.mu.Unlock()
}

func (m *mockExchangeBook) publishBoth(priceChan chan<- PriceTick) {
	m.mu.Lock()
	ba, bb := m.binAsk, m.binBid
	ta, tb := m.bitAsk, m.bitBid
	baq, bbq := m.binAskQty, m.binBidQty
	taq, tbq := m.bitAskQty, m.bitBidQty
	m.mu.Unlock()

	binInstr, _ := primaryInstrument("Binance")
	bitInstr, _ := primaryInstrument("Bitso")
	publishTick(priceChan, binInstr, ba, bb, baq, bbq)
	publishTick(priceChan, bitInstr, ta, tb, taq, tbq)
}
