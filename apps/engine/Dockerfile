# --- Etapa de build ---
# El motor usa modernc.org/sqlite (SQLite en Go puro), así que compilamos un
# binario 100% estático con CGO desactivado: sin toolchain de C, sin librerías
# dinámicas que copiar.
FROM golang:1.26 AS build

WORKDIR /src

# Cacheo de dependencias: copiamos primero go.mod/go.sum.
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /bin/arus-engine .

# --- Etapa de ejecución ---
# distroless/static: imagen mínima que YA incluye ca-certificates (necesario para
# las conexiones TLS wss:// a Binance/Bitso) y tzdata. Sin shell ni paquetes extra.
FROM gcr.io/distroless/static-debian12

WORKDIR /app
COPY --from=build /bin/arus-engine /app/arus-engine

# El motor lee PORT (default 8080) y persiste el ledger en ./data (montado como volumen).
EXPOSE 8080

CMD ["/app/arus-engine"]
