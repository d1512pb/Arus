import React, { useState } from 'react';
import { ShieldAlert, ArrowRight } from 'lucide-react';

interface OnboardingModalProps {
  onInit: (usd: number, btc: number) => void;
}

export const OnboardingModal: React.FC<OnboardingModalProps> = ({ onInit }) => {
  const [usd, setUsd] = useState<number | ''>('');
  const [btc, setBtc] = useState<number | ''>('');

  const handleSubmit = (e: React.FormEvent) => {
    e.preventDefault();
    const finalUsd = Number(usd) || 0;
    const finalBtc = Number(btc) || 0;
    
    if (finalUsd < 1000 || finalBtc < 0.1) {
      alert("Por favor, ingresa al menos $1000 USD y 0.1 BTC para comenzar.");
      return;
    }
    
    onInit(finalUsd, finalBtc);
  };

  return (
    <div className="fixed inset-0 z-[200] overflow-y-auto bg-gray-900/80 backdrop-blur-md flex items-center justify-center p-4 font-mono">
      <div className="bg-white dark:bg-gray-900 border border-gray-200 dark:border-gray-800 rounded-xl max-w-lg w-full p-6 sm:p-8 shadow-2xl transform transition-all relative">
        
        <div className="flex items-center gap-4 mb-6 border-b border-gray-100 dark:border-gray-800 pb-6">
          <div className="w-12 h-12 rounded-lg flex items-center justify-center shadow-lg overflow-hidden bg-white">
            <img src="/Logo-Arus.jpeg" alt="Logo Arus" className="w-full h-full object-cover" />
          </div>
          <div>
            <h2 className="text-xl font-black text-gray-900 dark:text-gray-100 tracking-widest uppercase">ARUS</h2>
            <p className="text-gray-500 dark:text-gray-400 text-xs font-bold tracking-widest mt-1">CONFIGURACIÓN INICIAL</p>
          </div>
        </div>

        <p className="text-gray-600 dark:text-gray-400 text-sm mb-6 leading-relaxed">
          Elige con cuánto dinero y bitcoin quieres empezar. El bot repartirá todo por igual (50/50) entre las dos casas de cambio, Binance y Bitso, para buscar oportunidades de ganancia entre ellas.
        </p>

        <form onSubmit={handleSubmit} className="flex flex-col gap-5">
          <div>
            <label className="text-[10px] font-bold tracking-widest text-gray-500 dark:text-gray-400 uppercase flex justify-between">
              <span>Dinero inicial (USD)</span>
              <span className="text-blue-500">(Mínimo: $1000)</span>
            </label>
            <div className="relative mt-2">
              <span className="absolute left-4 top-1/2 -translate-y-1/2 text-gray-400 font-bold">$</span>
              <input 
                type="number" 
                value={usd}
                onChange={e => setUsd(e.target.value === '' ? '' : parseFloat(e.target.value) || 0)}
                className="w-full bg-gray-50 dark:bg-gray-950 border border-gray-300 dark:border-gray-700 text-gray-900 dark:text-gray-100 p-4 pl-8 rounded-lg outline-none focus:border-blue-500 focus:ring-1 focus:ring-blue-500 transition-colors font-mono font-bold shadow-sm"
                placeholder="Ej. 60000"
                min="1000"
                step="1000"
              />
            </div>
            <p className="text-[10px] text-gray-400 mt-2">Se repartirán ${(Number(usd) / 2).toFixed(2)} en Binance y ${(Number(usd) / 2).toFixed(2)} en Bitso.</p>
          </div>

          <div>
            <label className="text-[10px] font-bold tracking-widest text-gray-500 dark:text-gray-400 uppercase flex justify-between">
              <span>Bitcoin inicial (BTC)</span>
              <span className="text-blue-500">(Mínimo: 0.1 BTC)</span>
            </label>
            <div className="relative mt-2">
              <span className="absolute left-4 top-1/2 -translate-y-1/2 text-gray-400 font-bold">₿</span>
              <input 
                type="number" 
                value={btc}
                onChange={e => setBtc(e.target.value === '' ? '' : parseFloat(e.target.value) || 0)}
                className="w-full bg-gray-50 dark:bg-gray-950 border border-gray-300 dark:border-gray-700 text-gray-900 dark:text-gray-100 p-4 pl-8 rounded-lg outline-none focus:border-blue-500 focus:ring-1 focus:ring-blue-500 transition-colors font-mono font-bold shadow-sm"
                placeholder="Ej. 1.0"
                min="0.1"
                step="0.1"
              />
            </div>
            <p className="text-[10px] text-gray-400 mt-2">Se repartirán {(Number(btc) / 2).toFixed(4)} BTC en Binance y {(Number(btc) / 2).toFixed(4)} BTC en Bitso.</p>
          </div>

          <button 
            type="submit"
            disabled={Number(usd) < 1000 || Number(btc) < 0.1}
            className={`mt-4 w-full p-4 rounded-lg font-black text-sm uppercase tracking-widest flex justify-between items-center group shadow-md transition-all ${Number(usd) >= 1000 && Number(btc) >= 0.1 ? 'bg-blue-600 hover:bg-blue-700 text-white' : 'bg-gray-300 dark:bg-gray-800 text-gray-500 cursor-not-allowed'}`}
          >
            <span>Empezar</span>
            <ArrowRight className="w-5 h-5 group-hover:translate-x-1 transition-transform" />
          </button>
        </form>
      </div>
    </div>
  );
};
