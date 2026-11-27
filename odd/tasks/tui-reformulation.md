# TUI reformulation

Redesign of the player surface, inspired by the pane-and-focus layout of spotatui, while keeping Pulso's Soloist playback and Spotify Web API use. Playback, saving, removing and pairing behavior must not change.

## Specs

- S1 — “quiero una reformulacion de la ui https://github.com/LargeModGames/spotatui quiero que hagas una tui muestrame la propuesta en formato tui”
- S2 — “hay que quitar los comandos de abajo, tambien hay que quitar el tab para pasar de foco sino que cada parte tendra un comando especifico el reproductor estara abajo donde se mostrara de estaforma sera una caja donde en la parte superior de la caja dentro de la liena de la caja dira si esta en pausa o reprodudiendo si esta en aleatorio si esta en repeticion y el volumen el reproductor tendra la imagen de la cancion a la izquierda al lado el nombre de la cacion abajo el disco/ nombre artistita y abajo el tiempo de reproduccion con el la barra de tiempo de reproduccion”
- S3 — “quiero que generes el plan”
- S4 — “1. es posible agregar la caratula? verifica si es posible 2. genera varias versiones en formato tui 3. arregla los tamaos, 4. la aplicacion se cerrar con q hay que definir bien los controles y evitar duplicaciones 5. en plan debe estar por etapas repara las etapas actualiza el plan”

## Estado actual (verificado en código)

| Hecho | Evidencia |
| --- | --- |
| Existe una línea de pistas al pie que S2 manda quitar | `internal/ui/player.go:1790` (`hintRows`), `:1803` (`hintBudget`), `:1850` (`hintPairs`) |
| El foco se cambia con `Tab` y `Shift+Tab` | `player.go:473`, `476`; dentro de la búsqueda `:608`, `611` |
| El reproductor ya está abajo, con título, artista, álbum y barra de tiempo | `player.go:1716-1717` |
| La cola de Soloist existe y hoy ocupa la tabla | `internal/soloist/transport.go:54` (`State.Queue`), `:462` (`get_queue`), `player.go:2180` |
| No hay carátula: ni URL pedida, ni decodificación, ni dibujo | `internal/spotify/catalog.go:73-80`, `library_source.go:44-49` |

## Carátula: es posible, sin dependencias nuevas

Sí. El trabajo no es de red ni de permisos, es de dibujo:

- **La URL ya viene en las respuestas que hoy se piden.** `/v1/search` (`catalog.go:83-85`) y `/v1/playlists/{id}/items` (`catalog.go:121-126`) devuelven `album.images[].url` en el mismo objeto; solo faltan campos en `spotifyTrack` (`catalog.go:73-80`) y en `Track` (`library_source.go:17-22`). Sin endpoint nuevo y sin permiso extra: los scopes actuales (`oauth.go:28`) ya cubren los objetos de pista.
- **Soloist no transporta carátula.** `transport.go:33-40` y `:219-236` no traen imagen ni id de álbum. La carátula de lo que suena sale de un mapa `URI → URL` armado con las páginas ya cargadas, y cuando la pista no está en ninguna página, de una sola llamada `GET /v1/tracks/{id}?fields=album.images`.
- **No hace falta librería gráfica.** `image/jpeg` e `image/png` son de la biblioteca estándar; el dibujo usa medio bloque `▀` con color de 24 bits, que el tema ya emite (`preferences.go:36-47`, `player.go:1487-1501`).
- **Reglas de costo.** La primera pintura muestra el bloque de iniciales y nunca bloquea la entrada; el resultado se cachea en memoria por URL; una descarga fallida deja las iniciales sin aviso; la carátula solo se dibuja cuando la caja tiene al menos 6 filas interiores.

## Versión adoptada: A — tres cajas

Cada zona es una caja con su título en el borde y el reproductor es una caja aparte, con la línea de estado en su borde superior. No hay dispositivo: Pulso siempre reproduce en la consola, así que la caja no gasta una columna en eso.

Alternativas consideradas y descartadas, conservadas solo como registro: **B — un solo marco** (zonas separadas por reglas simples, más filas para la tabla) y **C — foco expandido** (la tabla usa todo el ancho y lleva álbum, la cola se abre solo con `c`, reproductor más alto).

```
┌──────────────────────────────────────────────────────────────────────────────────────────────────────────────────────┐
│  Pulso                                                                                              @diego · ? ayuda │
├──────────────────────────────────────────────────────────────────────────────────────────────────────────────────────┤
│┌─ BIBLIOTECA ──────────┐ ┌─ Rock Chileno & Clásicos · 42 canciones ──────────────────┐ ┌─ A CONTINUACIÓN ───────────┐│
││   Playlists           │ │   #  TÍTULO                         ARTISTA           DUR │ │ 1 Weón                     ││
││ ▸ Rock Chileno        │ │ ───────────────────────────────────────────────────────── │ │   Chico Trujillo           ││
││   Clásicos 90         │ │   1 Tren al Sur                    Los Prisioneros   5:36 │ │ 2 Huelga de Dolores        ││
││   Favoritas           │ │ ▸ 2 Estrechez de Corazón           Los Prisioneros   6:21 │ │   Los Miserables           ││
││   Para correr         │ │   3 Llueve sobre la Ciudad         Los Bunkers       3:56 │ │ 3 El Baile de los Que Sobr ││
││                       │ │   4 Weón                           Chico Trujillo    3:02 │ │   Los Prisioneros          ││
││                       │ │   5 Huelga de Dolores              Los Miserables    4:18 │ │ 4 La Espada & la Pared     ││
││                       │ │   6 El Baile de los Que Sobran     Los Prisioneros   5:42 │ │   Los Tres                 ││
││                       │ │   7 La Espada & la Pared           Los Tres          4:02 │ │                            ││
││                       │ │                                                           │ │                            ││
││                       │ │                                                           │ │                            ││
││                       │ │                                                           │ │                            ││
││                       │ │                                                           │ │                            ││
││                       │ │                                                           │ │ CONTEXTO                   ││
││                       │ │                                                           │ │ Rock Chileno               ││
││                       │ │                                                           │ │ ⇄ sí · ↻ lista             ││
││                       │ │                                                           │ │                            ││
│└───────────────────────┘ └───────────────────────────────────────────────────────────┘ └────────────────────────────┘│
│                                                                                                                      │
├─  ▶ Reproduciendo  ▸  ⇄ Sí  ▸  ↻ lista  ▸  vol 85% ───────────────────────────────────────────────────────────────── ┤
│                                                                                                                      │
│  ▄▄▄▄▄▄▄▄▄▄  Estrechez de Corazón                                                                                    │
│  █░░░██░░██  Los Prisioneros                                                                                         │
│  █░██░░██░█  La Cultura de la Basura · 1986                                                                          │
│  █░░░░██░░█  2:14  ━━━━━━━━━━━━━╸──────────────────────────  6:21                                                    │
│  ██████████                                                                                                          │
│              pista 2 de 42 · Rock Chileno                                                                            │
│                                                                                                                      │
│                                                                                                                      │
└──────────────────────────────────────────────────────────────────────────────────────────────────────────────────────┘
```

### Banda media — 100×30

Sin columna de cola: la tabla se queda el ancho y la cola se abre con `c`.

```
┌──────────────────────────────────────────────────────────────────────────────────────────────────┐
│  Pulso                                                                          @diego · ? ayuda │
├──────────────────────────────────────────────────────────────────────────────────────────────────┤
│┌─ BIBLIOTECA ───────┐ ┌─ Rock Chileno & Clásicos · 42   c cola ─────────────────────────────────┐│
││   Playlists        │ │   #  TÍTULO                                       ARTISTA           DUR ││
││ ▸ Rock Chileno     │ │ ─────────────────────────────────────────────────────────────────────── ││
││   Clásicos 90      │ │   1 Tren al Sur                                  Los Prisioneros   5:36 ││
││   Favoritas        │ │ ▸ 2 Estrechez de Corazón                         Los Prisioneros   6:21 ││
││   Para correr      │ │   3 Llueve sobre la Ciudad                       Los Bunkers       3:56 ││
││                    │ │   4 Weón                                         Chico Trujillo    3:02 ││
││                    │ │   5 Huelga de Dolores                            Los Miserables    4:18 ││
││                    │ │   6 El Baile de los Que Sobran                   Los Prisioneros   5:42 ││
││                    │ │   7 La Espada & la Pared                         Los Tres          4:02 ││
││                    │ │                                                                         ││
││                    │ │                                                                         ││
││                    │ │                                                                         ││
││                    │ │                                                                         ││
│└────────────────────┘ └─────────────────────────────────────────────────────────────────────────┘│
│                                                                                                  │
├─  ▶ Reproduciendo  ▸  ⇄ Sí  ▸  ↻ lista  ▸  vol 85% ───────────────────────────────────────────── ┤
│                                                                                                  │
│  ▄▄▄▄▄▄▄▄▄▄  Estrechez de Corazón                                                                │
│  █░░░██░░██  Los Prisioneros                                                                     │
│  █░██░░██░█  La Cultura de la Basura · 1986                                                      │
│  █░░░░██░░█  2:14  ━━━━━━━━━━━━━╸──────────────────────────  6:21                                │
│  ██████████                                                                                      │
│              pista 2 de 42 · Rock Chileno                                                        │
│                                                                                                  │
│                                                                                                  │
└──────────────────────────────────────────────────────────────────────────────────────────────────┘
```

### Banda compacta — 80×24

Sin barra lateral: un selector de biblioteca ocupa una fila y la carátula cae a su bloque de iniciales.

```
┌──────────────────────────────────────────────────────────────────────────────┐
│  Pulso                                                      @diego · ? ayuda │
├──────────────────────────────────────────────────────────────────────────────┤
│ BIBLIOTECA ‹ Rock Chileno › · 42 canciones                        l cambiar  │
│──────────────────────────────────────────────────────────────────────────────│
│   #  TÍTULO                                            ARTISTA           DUR │
│ ──────────────────────────────────────────────────────────────────────────── │
│   1 Tren al Sur                                       Los Prisioneros   5:36 │
│ ▸ 2 Estrechez de Corazón                              Los Prisioneros   6:21 │
│   3 Llueve sobre la Ciudad                            Los Bunkers       3:56 │
│   4 Weón                                              Chico Trujillo    3:02 │
│   5 Huelga de Dolores                                 Los Miserables    4:18 │
│   6 El Baile de los Que Sobran                        Los Prisioneros   5:42 │
│   7 La Espada & la Pared                              Los Tres          4:02 │
│                                                                              │
├─  ▶ Reproduciendo  ▸  ⇄ Sí  ▸  ↻ lista  ▸  vol 85% ───────────────────────── ┤
│                                                                              │
│  ┌────────┐  Estrechez de Corazón                                            │
│  │        │  Los Prisioneros                                                 │
│  │   EC   │  La Cultura de la Basura · 1986                                  │
│  │        │  2:14  ━━━━━━━━━━━━━╸──────────────────────────  6:21            │
│  └────────┘                                                                  │
│                                                                              │
└──────────────────────────────────────────────────────────────────────────────┘
```

### Banda mínima — 40×12

Solo contexto y reproductor: la línea de estado se acorta sin perder volumen.

```
┌──────────────────────────────────────┐
│  Pulso              @diego · ? ayuda │
├──────────────────────────────────────┤
│ Rock Chileno · 42 canciones · l camb │
│                                      │
├─  ▶ ▸ ⇄ Sí ▸ ↻lista ▸ 85% ────────── ┤
│                                      │
│  Estrechez de Corazón                │
│  Los Prisioneros                     │
│  2:14  ━━━━━━╸────────────  6:21     │
│                                      │
└──────────────────────────────────────┘
```

### Banda insuficiente — 39×11

```
┌─────────────────────────────────────┐
│                                     │
│                                     │
│                                     │
│                                     │
│     Mínimo 40×12 · ahora 39×11      │
│                                     │
│                                     │
│                                     │
│                                     │
└─────────────────────────────────────┘
```

## Tamaños (matriz única)

Una sola fuente de verdad; la etapa E5 la copia a `docs/UI-PLAN.md`.

| Banda | Condición | Zonas | Reproductor | Carátula | Prueba |
| --- | --- | --- | --- | --- | --- |
| Amplio | ancho ≥ 120 y alto ≥ 30 | biblioteca + tabla + cola/contexto | 10 filas interiores | sí | 140×40, 120×34 |
| Medio | 100 ≤ ancho < 120 y alto ≥ 26 | biblioteca + tabla; cola con `c` | 10 filas | sí | 100×30 |
| Compacto | 80 ≤ ancho < 100 y alto ≥ 20 | selector + tabla | 8 filas | no | 80×24 |
| Mínimo | 40 ≤ ancho < 80 y alto ≥ 12 | solo contexto y reproductor | 6 filas | no | 40×12, 79×23 |
| Insuficiente | ancho < 40 o alto < 12 | mensaje de tamaño, sin marco | — | — | 39×11 |

Las maquetas de arriba salen del prototipo `python3 /tmp/pulso-preview.py` (y `--live` para verlo con colores y teclas), que dibuja cada banda con el ancho exacto. Reglas que no cambian con la banda: la línea de estado vive siempre en el borde superior de la caja del reproductor y se recorta con `…`; la caja nunca crece más allá de su altura de banda y la tabla es la que cede filas; ninguna fila desborda el ancho.

## Controles (una tecla, una acción)

`q` es la salida de la aplicación: solo cierra cuando el foco no está en un campo de texto; `Ctrl+C` sale siempre. `Esc` nunca sale: vuelve a la tabla o cancela lo enfocado.

| Ámbito | Tecla | Acción |
| --- | --- | --- |
| Global | `q` / `Ctrl+C` | Salir |
| Global | `Space` | Reproducir / pausar |
| Global | `n` / `b` | Siguiente / anterior |
| Global | `+` / `-` | Volumen |
| Global | `r` | Repetir: apagado → lista → pista |
| Global | `F2` / `F3` | Idioma / tema |
| Global | `?` | Ayuda: la única referencia de teclas |
| Global | `Esc` | Volver a la tabla o cancelar lo enfocado |
| Zona | `l` | Biblioteca |
| Zona | `/` | Búsqueda (el texto se traga `q`, `s`, `r`, `+`, `-`) |
| Zona | `c` | Cola |
| Biblioteca | `j` / `k`, `Enter` | Mover cursor, abrir la lista |
| Biblioteca | `s` | Reproducir la lista seleccionada en aleatorio |
| Tabla | `j` / `k`, `Enter` | Mover cursor, reproducir la fila |
| Tabla | `a` / `x` | Guardar la pista sonando / quitar la fila resaltada |

Duplicaciones que desaparecen, con su reemplazo:

| Hoy | Problema | Plan |
| --- | --- | --- |
| `Tab` / `Shift+Tab` | Ciclo de foco que S2 prohíbe | Se eliminan; cada zona tiene su letra y `Esc` vuelve |
| `p` (`player.go:519`, `707`, `712`) | Es "anterior" o "reproducir lista restringida" según el foco | `b` es siempre anterior; la lista restringida se reproduce con `Enter` en la biblioteca |
| `s` / `S` (`player.go:551`, `676`) | Misma letra en dos casos | `s` es aleatorio, acotado por zona: pista en reproducción, lista en la biblioteca |
| `?` y `F1` (`player.go:449`, `431`) | Dos teclas para la misma ayuda | Solo `?` abre la ayuda |
| `q` y `Esc` (`player.go:458-464`) | Ambos salían de la aplicación | `q` sale; `Esc` vuelve a la tabla |
| Línea de pistas del pie (`player.go:1790`) | Repite lo que la ayuda ya dice | Se elimina; `?` es la única referencia |

## Tasks (etapas)

Cada etapa es un commit revisable propio; el orden importa: E1 antes de E2, E3 y E4; E3 antes de E5; E6 al final. La versión A fija el cromo de E2; E1, E3, E4, E5 y E6 no dependen de esa elección.

- E1 [S2] — Reproductor en caja: línea de estado en el borde superior (estado, aleatorio, repetición, volumen) y, dentro, carátula o iniciales a la izquierda, nombre, álbum y artista, tiempo con barra; altura por banda y recorte con `…`. Aceptación: dibuja sin desbordar en 120×34, 100×30, 80×24 y 40×12; las teclas no cambian; `player_test.go` sigue verde. Hecho: la caja sustituye a la barra plana, el pie cede sus filas al contenido —a 40×12 desaparece— y el dispositivo solo aparece en la línea de contexto cuando la reproducción corre en otro equipo. Las pruebas que fijaban la barra antigua se movieron al tamaño donde su regla sigue siendo comprobable. Ruta: parent. Commit: —.
- E2 [S2, S4] — Zonas: biblioteca, tabla y cola/contexto con el cromo de tres cajas (versión A); en medio la cola se abre con `c` y no ocupa columna; en compacto la barra lateral se reduce a una fila; en mínimo no hay tabla, solo contexto y reproductor. Aceptación: las cuatro bandas de la matriz dibujan completo; la tabla es la única que cede filas; las pruebas de ancho pasan en 140×40, 120×34, 100×30, 80×24, 79×23, 40×12 y 39×11. Ruta: parent. Commit: —.
- E3 [S2, S4] — Controles: quitar la línea de pistas y `Tab`/`Shift+Tab`; `c`, `b`, `Esc` que vuelve y `q` que sale; `s` acotado por zona; retirar `p`, `S` y `F1` duplicados; ayuda como única referencia; el campo de texto se traga `q`, `s`, `r`, `+`, `-`. Aceptación: existe una prueba por cada fila de la tabla de controles; ninguna tecla hace dos cosas; `q` no sale con la búsqueda enfocada y sí sale desde la tabla. Ruta: parent. Commit: —.
- E4 [S2, S4] — Carátula: campos `album.images` en las respuestas ya pedidas, mapa `URI → URL`, una llamada `GET /v1/tracks/{id}?fields=album.images` cuando la pista no está cargada, descarga y decodificación con `image/jpeg` y `image/png` de la biblioteca estándar, dibujo con `▀` y color de 24 bits, caché en memoria y bloque de iniciales como respaldo. Aceptación: sin dependencia nueva en `go.mod`; sin scope nuevo; una descarga fallida deja iniciales y no emite aviso; la primera pintura no espera la imagen; se dibuja solo con 6 o más filas interiores. Ruta: parent. Commit: —.
- E5 [S1, S4] — Documentación y regresión: `INSTRUCTIONS.md`, `INSTRUCTIONS.en.md`, `docs/UI-PLAN.md` con la matriz y los controles nuevos, ayuda actualizada y las pruebas que fijan la matriz de bandas, las teclas retiradas y la eliminación de la línea de pistas. Aceptación: `go test ./...` verde en Linux; ninguna prueba menciona `Tab`, `F1` ni la línea de pistas; `docs/UI-PLAN.md` y este documento declaran la misma matriz. Ruta: parent. Commit: —.
- E6 [S1, S4] — Verificación visual: captura de terminal en 120×34 y 80×24, antes y después, con carátula disponible y sin ella, y `go test ./...`; las fallas preexistentes de Windows quedan registradas en el `## Log` de este documento. Aceptación: las cuatro capturas están en el registro y cada una coincide con la banda que declara. Ruta: parent. Commit: —.

## Log

- L1 — User: “quiero una reformulacion de la ui https://github.com/LargeModGames/spotatui quiero que hagas una tui muestrame la propuesta en formato tui”
- L2 — Proposal shown as terminal mockups: three zones with focus, upper status line and a transport footer; taken from spotatui the pane focus, density and control bar, not its sources, visualizer or plugins.
- L3 — User: “hay que quitar los comandos de abajo, tambien hay que quitar el tab para pasar de foco sino que cada parte tendra un comando especifico el reproductor estara abajo …”. Revised proposal: no hint bar, no `Tab`, per-zone keys, and the player as a bottom box whose top border carries play state, shuffle, repeat and volume, with cover, title, album/artist and the time bar inside.
- L4 — User: “quiero que generes el plan”. Delivery is split into player, layout, command model, docs/tests and visual verification. No implementation started and no branch created yet.
- L5 — User: “1. es posible agregar la caratula? verifica si es posible 2. genera varias versiones en formato tui 3. arregla los tamaos, 4. la aplicacion se cerrar con q hay que definir bien los controles y evitar duplicaciones 5. en plan debe estar por etapas repara las etapas actualiza el plan”
- L6 — Carátula verificada como posible sin dependencias nuevas: la URL viaja en las respuestas ya pedidas (`catalog.go:73-80`, `library_source.go:44-49`) y el dibujo usa `▀` con color de 24 bits de la biblioteca estándar; Soloist no transporta imagen (`transport.go:33-40`, `219-236`), así que el mapa `URI → URL` y una llamada de respaldo resuelven la pista sonando.
- L7 — Tres versiones dibujadas con ancho exacto comprobado fila por fila y la banda estrecha 80×24 común; la matriz de tamaños, la tabla de controles y las seis etapas reemplazan las cinco tareas anteriores. Se descartan B (un solo marco) y C (foco expandido).
- L8 — Riesgo vigente: E3 cambia contratos de teclado que las pruebas actuales fijan (`player_test.go`, `help_test.go`, `modes_regression_test.go`, `u3_regression_test.go`), así que E5 debe actualizarlas en el mismo commit que documenta el cambio; E1 y E2 reescriben la misma `View()` de 2318 líneas y conviene vigilar el tamaño de cada commit.
- L9 — Decisión del usuario: versión **A — tres cajas** como cromo de E2. B y C quedan descartadas y solo se registran como alternativas; E1, E3, E4, E5 y E6 no cambian.
- L10 — Las cinco bandas quedan dibujadas por un prototipo ejecutable (`/tmp/pulso-preview.py`, modo `--live` para verlo en la terminal con colores y teclas) y sus capturas, comprobadas fila por fila, reemplazan las maquetas manuales del documento. La banda mínima no lleva tabla: a 40×12 el marco, el encabezado y el reproductor dejan una sola fila de contenido.
- L11 — User: “lo de desk o spotify connect deks no tiene sentido porque siempre se ocupara en la consola”. El diseño pierde el dispositivo: la línea de estado queda con reproducción, aleatorio, repetición y volumen, y las maquetas se regeneran sin esa columna.
- L12 — E1 implementada en `internal/ui/player.go`: la caja dibuja su línea de estado en el borde superior con degradación por ancho (palabra de estado → etiqueta de volumen → porcentaje → puntos suspensivos), bloque de iniciales de 10×5 desde 80 columnas, título, artista, álbum, barra de tiempo y línea de contexto. El pie de pistas pasa a ceder filas y la reproducción remota deja de ocupar una columna fija. `go test ./...` verde en Linux; las pruebas que fijaban la barra plana se adaptaron al tamaño donde su regla sigue siendo observable.
