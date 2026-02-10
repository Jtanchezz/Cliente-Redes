# Cliente UDP Chat

Cliente UDP con UI web local (React/Vite) y backend en Go. Implementa el protocolo efímero con `LIST_USERS`, `USER_LIST`, `SEND_MSG`, `SEND_MSG_ACK`, `ERROR` y límites de 1024 bytes por mensaje.

## Requisitos
- Go 1.21+
- Node 18+ (para build de UI)

## Ejecución (UI compilada)
1. `cd frontend`
2. `npm install`
3. `npm run build`
4. `cd ..`
5. `go run .`
6. Abre `http://localhost:8080`

## Mock server local (UDP)
- `go run ./cmd/mockserver`
- Configura el cliente con `serverHost=127.0.0.1` y `serverPort=9999`

## Desarrollo (Vite + Go)
- Terminal 1: `go run .`
- Terminal 2: `cd frontend && npm install && npm run dev`
- Abre el host que indique Vite. El proxy reenvía `/api` al backend.

## Variables
- `HTTP_ADDR`: dirección del backend HTTP. Default `:8080`.
- `PUBLIC_DIR`: ruta a la UI compilada. Default `frontend/dist`.

## API interna
- `POST /api/config` -> `{ user, serverHost, serverPort }`
- `POST /api/send` -> `{ to, text }`
- `POST /api/list`
- `GET /api/events` -> SSE (`message`, `ack`, `users`, `error`, `status`)

## Notas de protocolo
- Tamaño de JSON <= 1024 bytes
- `from` y `to` solo `[A-Za-z0-9_-]`, case-insensitive, 1-20 chars
- `content` 1-512 chars
- Campos JSON: `type`, `from`, `to`, `content`, `users`, `id`
- `id` es UUID y se usa para mapear `SEND_MSG_ACK` al mensaje original
- ACK timeout: 5s
