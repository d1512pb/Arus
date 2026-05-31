// URLs del motor Arus (backend en Go).
//
// En desarrollo local apuntan a localhost:8080. En producción (frontend en Vercel,
// motor en Railway/Fly) se definen vía variables de entorno NEXT_PUBLIC_* al
// desplegar — deben usar esquemas seguros (wss:// y https://) detrás de TLS.
//
// Ver apps/web/.env.example
export const ENGINE_WS_URL =
  process.env.NEXT_PUBLIC_ENGINE_WS_URL ?? "ws://localhost:8080/ws";

export const ENGINE_HTTP_URL =
  process.env.NEXT_PUBLIC_ENGINE_HTTP_URL ?? "http://localhost:8080";
