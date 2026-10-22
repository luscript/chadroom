# go-chatroom

Chat en tiempo real escrito en Go con WebSockets. Todos los usuarios conectados comparten una
sala: cada mensaje se reenvía a todos, y la sala avisa cuando alguien entra o sale.

## Requisitos

- Go 1.27+

## Uso

```bash
go run ./cmd/chatroom
```

Abrí <http://localhost:8080>, elegí un nickname y empezá a chatear (abrí varias pestañas para
probar). La página está embebida en el binario, así que funciona desde cualquier directorio.

> Usá `localhost`, no `127.0.0.1`: la página se conecta a `ws://localhost:8080` y el chequeo
> de origen rechaza combinaciones distintas (ver `openspec/backlog.md`, punto #13).

### Configuración

| Variable          | Default | Descripción |
|-------------------|---------|-------------|
| `ALLOWED_ORIGINS` | vacío   | Orígenes extra (separados por coma, exactos: `https://chat.example.com`) que pueden abrir el WebSocket además del propio servidor. `null` nunca se acepta. |

El servidor escucha en `:8080` y se apaga ordenadamente con `Ctrl+C` / `SIGTERM`, cerrando
todas las conexiones.

## API

| Ruta | Descripción |
|------|-------------|
| `GET /` | Página del chat (`index.html` embebido). |
| `GET /ws?nickname=<nombre>` | Upgrade a WebSocket. Entrada: cada frame es texto plano. Salida: cada frame es un sobre JSON (ver abajo). |

**Entrada (cliente → servidor):** texto plano UTF-8 de hasta 4096 bytes. Los mensajes en blanco
se ignoran. Los frames binarios cierran la conexión con 1003 y el UTF-8 inválido con 1007.

**Salida (servidor → clientes):** un sobre JSON por frame:

```json
{"type":"chat","from":"bob (2)","text":"hola","ts":1792684800123}
```

| `type`    | Cuándo | `text` |
|-----------|--------|--------|
| `welcome` | Solo al que se conecta, con su nickname final en `from`. | `""` |
| `join`    | Alguien entró a la sala. | `""` |
| `leave`   | Alguien salió de la sala. | `""` |
| `chat`    | Mensaje de `from`, reenviado a todos. | el mensaje |

`ts` es el momento del evento en Unix milisegundos.

**Nicknames:** son únicos sin distinguir mayúsculas; si el nickname ya está en uso se agrega un
sufijo ` (n)` (por ejemplo `bob (2)`). Se quitan los caracteres de control y los de dirección
bidi, se recorta a 32 caracteres y, si queda vacío, se usa `Anonymous`.

## Arquitectura

```
cmd/chatroom/        main: router (gin), http.Server, señales, index.html embebido
internal/chat/
  room.go            Room: hub que registra clientes y difunde mensajes
  client.go          Client: readPump / writePump por conexión
  message.go         Mensaje JSON (type, from, text, ts) y su codificación
  nickname.go        Normalización y unicidad de apodos
```

Se usa el **patrón hub**:

- `Room.Run(ctx)` es la única goroutine que toca el registro de clientes
  (`map[*Client]struct{}`) y la única que cierra el canal `send` de cada cliente. Por eso no
  hay locks, no puede haber doble `close` y el hub nunca se bloquea a sí mismo.
- Cada conexión tiene dos goroutines: `readPump` es la única que lee, aplica el límite de
  tamaño y los deadlines y manda los mensajes al hub. `writePump` es la única que escribe:
  entrega mensajes, envía pings y cierra la conexión.
- Si un cliente lento llena su buffer, el hub lo desconecta en lugar de frenar a los demás.
- Al apagar, el hub cierra todos los clientes y un canal `done` evita que alguna goroutine
  quede bloqueada.

Los detalles y las decisiones de diseño están en
`openspec/changes/archive/2026-10-22-fix-channel-handling/design.md`.

## Tests

```bash
go test -race ./...
```

Incluye tests unitarios del hub (sin red) y tests de integración WebSocket con `httptest`.

## Desarrollo con SDD

El proyecto usa Spec-Driven Development. Todo lo relacionado está en `openspec/`:

- `openspec/specs/`: especificaciones vigentes (`chat`, `server`).
- `openspec/changes/archive/`: historial de cambios (propuesta, diseño, tareas, verificación).
- `openspec/backlog.md`: mejoras pendientes, agrupadas por cambio futuro.
