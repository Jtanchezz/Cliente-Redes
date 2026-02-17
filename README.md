# Cliente UDP Chat

Chat **UDP** con interfaz web local y backend en **Go** que actúa como puente **HTTP/SSE ↔ UDP**.  
Los mensajes UDP se envían como **JSON UTF-8** con tamaño máximo de **1024 bytes** por datagrama.  
Incluye un **servidor UDP de prueba (mockserver)**.

## Requisitos
- Go 1.21+
- Node.js 18+

## Componentes (qué hace cada uno)
- **mockserver (Go / UDP)**: servidor UDP central. Mantiene usuarios “online” por ventana de actividad (60s), responde lista de usuarios, reenvía mensajes y envía confirmaciones/errores.
- **backend (Go / HTTP + SSE)**: servidor local que sirve la UI y traduce acciones de la UI a UDP. Recibe UDP del servidor y lo publica a la UI en tiempo real por SSE.
- **frontend (React)**: interfaz del chat en el navegador. No usa UDP directamente; solo habla con el backend local.

## Protocolo UDP (resumen)
Tipos:
- `IDENTIFY` / `IDENTIFY_ACK`
- `LIST_USERS` / `USER_LIST`
- `SEND_MSG` / `SEND_MSG_ACK`
- `ERR`

Campos:
- `type`, `from`, `to`, `content`, `users`, `id`

## Levantar en local (Windows 11)
Desde la raíz del proyecto:

1) Compilar la UI:
- `cd frontend`
- `npm install`
- `npm run build`
- `cd ..`

2) Levantar el servidor UDP:
- `go run ./cmd/mockserver`

3) Levantar el backend (sirve la UI):
- `go run .`

4) Abrir:
- `http://localhost:8080`

En la UI:
- Servidor: `127.0.0.1`
- Puerto: `9999`

## Dos computadoras (misma red LAN)
- Una computadora ejecuta el **servidor UDP** (`go run ./cmd/mockserver`) y debe permitir tráfico **UDP 9999** en el firewall.
- Cada computadora ejecuta su **backend** local (`go run .`) y abre su UI en el navegador.
- En ambas UIs, en “Servidor” se coloca la **IP LAN** de la computadora que está corriendo el servidor UDP (por ejemplo `192.168.1.50`) y puerto `9999`.
