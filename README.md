# Cliente UDP Chat

Cliente de chat **UDP** con interfaz web local (**React + Vite**) y backend en **Go**. El backend sirve la UI (SPA) desde `PUBLIC_DIR` y expone una API HTTP interna que traduce acciones de la UI a mensajes UDP usando un protocolo JSON **efímero** con límite estricto de **1024 bytes por datagrama**.

Incluye un **mock server UDP** para pruebas locales.

---

## Requisitos

- **Go 1.21+**
- **Node.js 18+** (solo para compilar la UI)

---

## Arquitectura (alto nivel)

- **Frontend (React/Vite)**:
  - En producción se compila a `frontend/dist` y lo sirve el backend Go como SPA.
  - En desarrollo se usa Vite (`npm run dev`) con proxy a `/api`.

- **Backend (Go)**:
  - API HTTP interna para configurar el cliente, enviar mensajes, listar usuarios y consumir eventos por SSE.
  - Socket UDP local (puerto efímero) para hablar con el servidor UDP configurado.

- **Servidor UDP (mock)**:
  - Implementa `LIST_USERS` y `SEND_MSG`.
  - Mantiene un registro de usuarios “online” por ventana de actividad (`onlineWindow = 60s`).

---

## Ejecución (UI compilada)

1. Compila la UI:
   - `cd frontend`
   - `npm install`
   - `npm run build`

2. Ejecuta el backend:
   - `cd ..`
   - `go run .`

3. Abre:
   - `http://localhost:8080`

> Si `PUBLIC_DIR` no existe o no contiene la UI compilada, el backend muestra una página simple indicando cómo construirla.

---

## Desarrollo (Vite + Go)

- Terminal 1 (backend):
  - `go run .`

- Terminal 2 (frontend):
  - `cd frontend`
  - `npm install`
  - `npm run dev`

Abre el host que indique Vite. El proxy de desarrollo reenvía `/api` al backend.

---

## Mock server UDP (para pruebas)

Levanta el servidor UDP de prueba:

- `go run ./cmd/mockserver`

Parámetros:
- `-port` (default: `9999`)
- `-tag`  (default: `mock-server`) → valor usado como `from` en respuestas del servidor

Configuración recomendada del cliente:
- `serverHost = 127.0.0.1`
- `serverPort = 9999`

---

## Variables de entorno (backend HTTP)

- `HTTP_ADDR`: dirección del servidor HTTP  
  **Default:** `:8080`

- `PUBLIC_DIR`: ruta de la UI compilada (SPA)  
  **Default:** `frontend/dist`

---

## API interna (HTTP)

Base: `http://<HTTP_ADDR>`

### `POST /api/config`

Configura el usuario local y el servidor UDP destino. Además:
- abre el socket UDP local (si no existe),
- inicia los loops de lectura UDP y expiración de ACKs (solo una vez),
- dispara un `LIST_USERS` inicial,
- emite evento SSE `status`.

**Request**
```json
{ "user": "alice", "serverHost": "127.0.0.1", "serverPort": 9999 }
