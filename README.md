# go-chatroom

Chat en tiempo real escrito en Go con WebSockets. Hay varias salas fijas (`general`, `juegos` y
`musica`): cada mensaje se reenvía a todos los de la misma sala, y la sala avisa cuando alguien
entra o sale.

## Requisitos

- Go 1.27+

## Uso

```bash
go run ./cmd/chatroom
```

Abrí <http://localhost:8080> (o `127.0.0.1`, o la IP de tu máquina en la red local), elegí un
nickname y empezá a chatear (abrí varias pestañas para probar). La página se conecta al
WebSocket del mismo host desde el que la abriste, y está embebida en el binario, así que
funciona desde cualquier directorio.

Si se corta la conexión (reinicio del servidor, Wi-Fi, notebook suspendida), la página se
reconecta sola: reintenta con esperas crecientes de hasta 30 s y vuelve a pedir el mismo
nickname. Los mensajes enviados por otros durante el corte no se recuperan.

### Configuración

| Variable          | Default | Descripción |
|-------------------|---------|-------------|
| `ALLOWED_ORIGINS` | vacío   | Orígenes extra (separados por coma, exactos: `https://chat.example.com`) que pueden abrir el WebSocket además del propio servidor. `null` nunca se acepta. |

El servidor escucha en `:8080` y se apaga ordenadamente con `Ctrl+C` / `SIGTERM`, cerrando
todas las conexiones.

## API

| Ruta | Descripción |
|------|-------------|
| `GET /` | Página del chat (`index.html` embebido), con `Content-Security-Policy`. |
| `GET /app.js`, `GET /app.css` | Script y estilos de la página (embebidos). |
| `GET /rooms` | Lista de salas: `{"rooms":["general","juegos","musica"]}` (`application/json`, `nosniff`, `Cache-Control: no-cache`). |
| `GET /ws?nickname=<nombre>&room=<sala>` | Upgrade a WebSocket a una sala. Entrada: cada frame es texto plano. Salida: cada frame es un sobre JSON (ver abajo). |

**Salas:** `room` debe coincidir exactamente (distingue mayúsculas) con una de las de `/rooms`. Si
falta o está vacío se usa la sala por defecto (`general`, la primera); si no existe, el servidor
responde `404` antes del upgrade. Cada sala es independiente: tiene sus propios usuarios, su
historial de 50 mensajes y su propio espacio de nicknames (`bob` puede estar en `general` y en
`juegos` a la vez). Cada conexión pertenece a una sola sala; para estar en varias hay que abrir
un WebSocket por sala. Los frames no llevan el nombre de la sala. La política de orígenes
(`ALLOWED_ORIGINS`) es la misma para todas.

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

Además hay dos frames privados que solo recibe quien se conecta y que no llevan `from` ni `text`:

```json
{"type":"users","users":["Alice","bob"],"ts":1792684800123}
{"type":"history","messages":[{"type":"chat","from":"bob","text":"hola","ts":1792684790000}],"ts":1792684800123}
```

- `users`: los nicknames conectados (incluido el propio), ordenados sin distinguir mayúsculas.
- `history`: los últimos 50 mensajes `chat`, del más viejo al más nuevo (`[]` si no hay). Vive solo
  en memoria: se pierde al reiniciar el servidor.

**Secuencia al conectarse:** `welcome` → `users` → `history` → `join` (este último lo reciben todos,
incluido el que entra). Si falla la entrega de alguno de los tres privados, el cliente se descarta
sin anunciar `join` ni `leave`.

**Clientes lentos:** si la sala descarta a un cliente por no leer a tiempo, ahora los demás reciben
su `leave`. Al apagar el servidor no se envía ningún `leave`.

**Nicknames:** son únicos sin distinguir mayúsculas; si el nickname ya está en uso se agrega un
sufijo ` (n)` (por ejemplo `bob (2)`). Se quitan los caracteres de control y los de dirección
bidi, se recorta a 32 caracteres y, si queda vacío, se usa `Anonymous`.

**Imágenes y GIFs:** un mensaje que es solo una URL `https` de imagen (`.gif`, `.webp`, `.png`,
`.jpg`) se muestra inline únicamente si el host está en la lista permitida (Giphy, Tenor e
Imgur); cualquier otra URL se muestra como texto. Así nadie puede hacer que los navegadores de
los demás pidan una imagen a un servidor propio y vean sus IPs. Para sumar un host, agregalo en
`imageHosts` (`cmd/chatroom/main.go`, que arma el `img-src` de la CSP) y en `IMAGE_HOSTS`
(`app.js`); un test falla si las dos listas no coinciden.

**CSP:** la página no tiene código inline; la política solo permite scripts y estilos del propio
origen, Google Fonts y las imágenes de la lista permitida.

## Arquitectura

```
cmd/chatroom/        main: router (gin), http.Server, señales, CSP y lista de hosts de imágenes
  index.html         página (embebida), con app.js y app.css
internal/chat/
  server.go          Server: elige la sala (`room`), hace el upgrade y aplica la política de orígenes
  room.go            Room: hub que registra clientes y difunde mensajes (uno por sala)
  client.go          Client: readPump / writePump por conexión
  message.go         Mensaje JSON (type, from, text, ts) y su codificación
  nickname.go        Normalización y unicidad de apodos
```

Se usa el **patrón hub**:

- `Room.Run(ctx)` (una goroutine por sala, lanzadas por `Server.Run`) es la única goroutine que toca el registro de clientes
  (`map[*Client]struct{}`) y la única que cierra el canal `send` de cada cliente. Por eso no
  hay locks, no puede haber doble `close` y el hub nunca se bloquea a sí mismo.
- Cada conexión tiene dos goroutines: `readPump` es la única que lee, aplica el límite de
  tamaño y los deadlines y manda los mensajes al hub. `writePump` es la única que escribe:
  entrega mensajes, envía pings y cierra la conexión.
- Si un cliente lento llena su buffer, el hub lo desconecta en lugar de frenar a los demás.
- Al apagar, cada hub cierra todos sus clientes y un canal `done` evita que alguna goroutine
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
