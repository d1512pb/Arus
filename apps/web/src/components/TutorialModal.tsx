"use client";

import { useEffect, useState } from "react";
import { X, ArrowRight, ArrowLeft } from "lucide-react";

interface Step {
  emoji: string;
  text: string;
  where: string;
}

const STEPS: Step[] = [
  {
    emoji: "👋",
    text: "Arus es un bot que gana con la diferencia de precio del bitcoin entre dos casas de cambio.",
    where: "Bienvenida",
  },
  {
    emoji: "📊",
    text: "Tu Dinero Total, Ganancia y nº de Operaciones.",
    where: "Arriba · tarjetas grandes",
  },
  {
    emoji: "🏦",
    text: "Tus 2 casas de cambio (Binance y Bitso) con sus saldos.",
    where: "Columna izquierda",
  },
  {
    emoji: "✏️",
    text: "¿Agregar o quitar dinero? Toca «Editar fondos».",
    where: "En cada casa de cambio",
  },
  {
    emoji: "▶️",
    text: "¿No ves movimiento? Pulsa el botón rojo «Probar el bot».",
    where: "Arriba a la derecha",
  },
  {
    emoji: "📈",
    text: "Operaciones en vivo. El «?» explica la estrategia.",
    where: "Columna derecha",
  },
  {
    emoji: "📜",
    text: "«Historial / Auditoría»: todo queda guardado, aunque reinicies.",
    where: "Final de la página",
  },
  {
    emoji: "🚀",
    text: "¡Listo! Explora. ¿Empezar de cero? Usa «RESET» arriba.",
    where: "A jugar",
  },
];

export function TutorialModal({ open, onClose }: { open: boolean; onClose: () => void }) {
  const [step, setStep] = useState(0);

  useEffect(() => {
    if (open) setStep(0);
  }, [open]);

  if (!open) return null;

  const isLast = step === STEPS.length - 1;
  const current = STEPS[step];

  return (
    <div className="fixed inset-0 z-[130] flex items-center justify-center bg-gray-900/70 backdrop-blur-sm p-4">
      <div className="bg-white dark:bg-gray-900 border border-gray-200 dark:border-gray-800 rounded-2xl max-w-md w-full p-6 sm:p-8 shadow-2xl relative animate-modal-scale text-center">
        <button
          onClick={onClose}
          className="absolute top-4 right-4 text-gray-400 hover:text-gray-600 dark:hover:text-gray-200 transition-colors"
          aria-label="Cerrar tutorial"
        >
          <X className="w-5 h-5" />
        </button>

        <div className="text-[10px] font-bold uppercase tracking-widest text-gray-400 mb-4">
          Tutorial · {step + 1} / {STEPS.length}
        </div>

        <div className="text-5xl mb-4">{current.emoji}</div>

        <div className="inline-block text-[10px] font-bold uppercase tracking-widest text-blue-600 dark:text-blue-400 bg-blue-50 dark:bg-blue-500/10 border border-blue-200 dark:border-blue-500/30 rounded-full px-3 py-1 mb-4">
          📍 {current.where}
        </div>

        <p className="text-base sm:text-lg font-bold text-gray-900 dark:text-gray-100 leading-snug min-h-[3.5rem] flex items-center justify-center">
          {current.text}
        </p>

        {/* Progreso */}
        <div className="flex items-center justify-center gap-1.5 my-6">
          {STEPS.map((_, i) => (
            <span
              key={i}
              className={`h-1.5 rounded-full transition-all duration-300 ${i === step ? "w-6 bg-blue-500" : "w-1.5 bg-gray-300 dark:bg-gray-700"}`}
            />
          ))}
        </div>

        <div className="flex items-center justify-between gap-3">
          <button
            onClick={onClose}
            className="text-xs font-bold uppercase tracking-widest text-gray-400 hover:text-gray-600 dark:hover:text-gray-300 transition-colors"
          >
            Saltar
          </button>

          <div className="flex items-center gap-2">
            {step > 0 && (
              <button
                onClick={() => setStep((s) => s - 1)}
                className="flex items-center gap-1 px-4 py-2.5 rounded-lg text-xs font-bold uppercase tracking-widest bg-gray-100 dark:bg-gray-800 text-gray-600 dark:text-gray-300 hover:bg-gray-200 dark:hover:bg-gray-700 transition-all active:scale-95"
              >
                <ArrowLeft className="w-4 h-4" /> Atrás
              </button>
            )}
            <button
              onClick={() => (isLast ? onClose() : setStep((s) => s + 1))}
              className="flex items-center gap-1 px-5 py-2.5 rounded-lg text-xs font-bold uppercase tracking-widest bg-blue-600 hover:bg-blue-500 text-white transition-all active:scale-95 shadow-sm"
            >
              {isLast ? "¡Entendido!" : <>Siguiente <ArrowRight className="w-4 h-4" /></>}
            </button>
          </div>
        </div>
      </div>
    </div>
  );
}
