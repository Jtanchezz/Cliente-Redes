# Cliente UDP Chat

Cliente UDP con UI web local (React/Vite) y backend en Go. Implementa el protocolo efímero con `JOIN`, `LIST`, `USERS`, `MSG`, `ACK`, `ERR` y límites de 1024 bytes por mensaje.

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
- `text` 1-512 chars
- ACK timeout: 5s
