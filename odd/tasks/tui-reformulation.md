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
| Medio | ancho ≥ 100 y alto ≥ 26, también con más de 120 columnas si el alto no llega a la banda amplia | biblioteca + tabla; cola con `c` | 10 filas | sí | 100×30 |
| Compacto | ancho ≥ 80 y alto ≥ 20 | selector + tabla | 8 filas | sí | 80×24 |
| Mínimo | ancho ≥ 40 y alto ≥ 12 | contexto y reproductor, y la tabla cuando el alto deja filas para su cabecera | 6 filas | no | 40×12, 79×23 |
| Insuficiente | ancho < 40 o alto < 12 | mensaje de tamaño, sin marco | — | — | 39×11 |

Las maquetas de arriba salen del prototipo `python3 /tmp/pulso-preview.py` (y `--live` para verlo con colores y teclas), que dibuja cada banda con el ancho exacto. Reglas que no cambian con la banda: la línea de estado vive siempre en el borde superior de la caja del reproductor; la caja nunca crece más allá de su altura de banda y, cuando la ventana es baja, se recorta por debajo de ella —a 40×12 dibuja tres filas interiores— para que el borde inferior no se pierda; la tabla es la que cede filas; ninguna fila desborda el ancho. Las bandas se resuelven en escalera: las condiciones de ancho son pisos y el alto decide, así que un alto que no alcanza el umbral de una banda baja a la siguiente forma más rica que sí cabe, nunca a la más pobre. Por eso 120×29 usa la banda media y 100×25 la compacta.

## Controles (una tecla, una acción)

`q` es la salida de la aplicación: solo cierra cuando el foco no está en un campo de texto; `Ctrl+C` sale siempre. `Esc` nunca sale: vuelve a la tabla o cancela lo enfocado.

| Ámbito | Tecla | Acción |
| --- | --- | --- |
| Global | `q` / `Ctrl+C` | Salir |
| Global | `Space` | Reproducir / pausar |
| Global | `n` / `b` | Siguiente / anterior |
| Global | `+` / `-` | Volumen |
| Global | `←` / `→` | Retroceder / avanzar 10 s |
| Global | `r` | Repetir: apagado → lista → pista |
| Global | `F2` / `F3` | Idioma / tema |
| Global | `?` | Ayuda: la única referencia de teclas |
| Global | `Esc` | Volver a la tabla o cancelar lo enfocado |
| Zona | `l` | Biblioteca |
| Zona | `/` | Búsqueda (el texto se traga `q`, `s`, `r`, `+`, `-`) |
| Zona | `c` | La tabla muestra la cola y toma el foco (la columna de la banda amplia es solo informativa) |
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
| `Tab: library` en el selector compacto | Nombra una tecla que ya no existe | Pasa a `l` |

## Tasks (etapas)

Cada etapa es un commit revisable propio; el orden importa: E1 antes de E2, E3 y E4; E3 antes de E5; E6 al final. La versión A fija el cromo de E2; E1, E3, E4, E5 y E6 no dependen de esa elección.

- E1 [S2] — Reproductor en caja: línea de estado en el borde superior (estado, aleatorio, repetición, volumen) y, dentro, carátula o iniciales a la izquierda, nombre, álbum y artista, tiempo con barra; altura por banda y recorte con `…`. Aceptación: dibuja sin desbordar en 120×34, 100×30, 80×24 y 40×12; las teclas no cambian; `player_test.go` sigue verde. Hecho: la caja sustituye a la barra plana, el pie cede sus filas al contenido —a 40×12 desaparece— y el dispositivo solo aparece en la línea de contexto cuando la reproducción corre en otro equipo. Las pruebas que fijaban la barra antigua se movieron al tamaño donde su regla sigue siendo comprobable. Ruta: parent. Commit: —.
- E2 [S2, S4] — Zonas: biblioteca, tabla y cola/contexto con el cromo de tres cajas (versión A); en medio la cola se abre con `c` y no ocupa columna; en compacto la barra lateral se reduce a una fila; en mínimo van el contexto y el reproductor, y la tabla también cuando el alto deja filas para su cabecera (a 40×12 no las deja). Aceptación: las cuatro bandas de la matriz dibujan completo; la tabla es la única que cede filas; las pruebas de ancho pasan en 140×40, 120×34, 100×30, 80×24, 79×23, 40×12 y 39×11. Hecho: anchos de zona 25 (biblioteca amplia), 24 (biblioteca media) y 30 (cola); la tabla toma el resto. Una frase de estado de sesión que no cabe en su zona se muestra una vez a todo el ancho encima de la caja del reproductor, nunca truncada, y la zona conserva solo su marcador. Ruta: parent. Commit: —.
- E3 [S2, S4] — Controles: quitar la línea de pistas y `Tab`/`Shift+Tab`; `c`, `b`, `Esc` que vuelve y `q` que sale; `s` acotado por zona; retirar `p`, `S` y `F1` duplicados; ayuda como única referencia; el campo de texto se traga `q`, `s`, `r`, `+`, `-`. Aceptación: existe una prueba por cada fila de la tabla de controles; ninguna tecla hace dos cosas; `q` no sale con la búsqueda enfocada y sí sale desde la tabla. Hecho: el pie de pistas y `Tab`/`Shift+Tab` desaparecen con `playerStep` y `playerFocusPlaying`; `b` es siempre anterior y la lista restringida se reproduce con `Enter`; `s` se acota por zona; `F1`, `p` y `S` ya no existen; `q`/`Ctrl+C` salen y `Esc` solo vuelve o cancela. La ayuda se reescribió por ámbitos y es la única referencia. Las filas que ocupaba el pie pasan a la tabla, así que el marco vuelve a llenar la ventana en todas las bandas con marco. Ruta: parent. Commit: —.
- E4 [S2, S4] — Carátula: campos `album.images` en las respuestas ya pedidas, mapa `URI → URL`, una llamada `GET /v1/tracks/{id}?fields=album.images` cuando la pista no está cargada, descarga y decodificación con `image/jpeg` y `image/png` de la biblioteca estándar, dibujo con `▀` y color de 24 bits, caché en memoria y bloque de iniciales como respaldo. Aceptación: sin dependencia nueva en `go.mod`; sin scope nuevo; una descarga fallida deja iniciales y no emite aviso; la primera pintura no espera la imagen; se dibuja solo donde existe la ranura de carátula —el mismo umbral de E1: ancho ≥ 80 y ocho filas interiores—. Hecho: `album.images` se lee de las respuestas ya pedidas, la pista que ninguna página trajo se resuelve con una sola consulta estrecha, la imagen se descarga y decodifica con la biblioteca estándar, se reduce por promedio de bloque y se dibuja con medio bloque y color de 24 bits; la caché recuerda la imagen y el fallo por URL, la primera pintura nunca espera y una ventana sin ranura no descarga nada. Ruta: parent. Commit: —.
- E5 [S1, S4] — Documentación y regresión: `INSTRUCTIONS.md`, `INSTRUCTIONS.en.md`, `docs/UI-PLAN.md` con la matriz y los controles nuevos, ayuda actualizada y las pruebas que fijan la matriz de bandas, las teclas retiradas y la eliminación de la línea de pistas. Hecho: `INSTRUCTIONS.md` y su par inglés describen las tres zonas, la caja con carátula o iniciales y las cinco bandas, sin `Tab`, `F1`, `p`, `S` ni pie; `docs/UI-PLAN.md` pasa de plan a interfaz entregada con la misma matriz y la misma tabla de controles. Se cierra además el aviso de la posición obsoleta: una sesión desconectada muestra `0:00 / --:--`. Aceptación: `go test ./...` verde en Linux; ninguna prueba *depende* de `Tab`, `F1` ni de la línea de pistas —las que los nombran solo comprueban que no hacen nada—; `docs/UI-PLAN.md` y este documento declaran la misma matriz. Ruta: parent. Commit: —.
- E6 [S1, S4] — Verificación visual: cinco capturas reales —antes y después a 120×34 y 80×24, y una con carátula—, `go test ./...` y el estado de la compilación para Windows. Aceptación: las capturas están en `## Capturas de E6`, cada una mide el ancho de su banda y las cuatro del antes y el después coinciden con lo que la vista dibuja con el estado declarado. Hecho: las cinco capturas están guardadas y comprobadas por una prueba; `GOOS=windows go build` y `go vet` pasan, pero sus pruebas no se pueden ejecutar en esta máquina y no se registra ninguna falla de Windows que no se haya observado; la descarga real contra la CDN de Spotify queda como paso manual por falta de red y credenciales. Ruta: parent. Commit: —.

## Capturas de E6

Verificación visual del estado entregado contra la línea base `947e4ab`. Las cuatro capturas del antes y el después comparten el mismo estado de reproducción: sesión conectada, en reproducción, aleatorio activado, repetición de lista y volumen 85 %. La quinta reproduce la carátula real con su propio estado y la interfaz en inglés, porque su escena es la de la prueba que dibuja la imagen; no comparte el estado de las otras cuatro y no se afirma que lo haga. Todas las filas miden exactamente el ancho que declara su banda.

### Antes (`947e4ab`) — 120×34

```
Pulso                                                                                                            playing
Search: type to search                                                                                             @user
────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────
LIBRARY                       │Rock Chileno · 42 tracks                                                                 
> Rock Chileno · ana          │#  Title                                  Artist                                 Duration
  Jazz · beto                 │─────────────────────────────────────────────────────────────────────────────────────────
                              │>1 Tren al Sur                            Los Prisioneros                        5:36    
                              │ 2 Estrechez de Corazón                   Los Prisioneros                        6:21    
                              │ 3 Llueve sobre la Ciudad                 Los Bunkers                            3:56    
                              │                                                                                         
                              │                                                                                         
                              │                                                                                         
                              │                                                                                         
                              │                                                                                         
                              │                                                                                         
                              │                                                                                         
                              │                                                                                         
                              │                                                                                         
                              │                                                                                         
                              │                                                                                         
                              │                                                                                         
                              │                                                                                         
                              │                                                                                         
                              │                                                                                         
                              │                                                                                         
                              │                                                                                         
                              │                                                                                         
                              │                                                                                         
────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────
  ▶ Estrechez de Corazón · Los Prisioneros                                                                   ⇄ on  ↻ all
La Cultura de la Basura  0:00 ╸─────────────────── 6:21                                                       Volume 85%
────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────
 Tab focus  ↑↓/jk move  Enter play  Space play/pause  s shuffle  r repeat  +/- volume  / search  l library              
 S shuffle-list  F1 help  F2 language  F3 theme  q quit  Esc back/quit                                                  
```

### Antes (`947e4ab`) — 80×24

```
Pulso                                                                    playing
Search: type to search                                                     @user
────────────────────────────────────────────────────────────────────────────────
LIBRARY: Rock Chileno                                               Tab: library
Rock Chileno · 42 tracks                                                        
#  Title                             Artist                             Duration
────────────────────────────────────────────────────────────────────────────────
>1 Tren al Sur                       Los Prisioneros                    5:36    
 2 Estrechez de Corazón              Los Prisioneros                    6:21    
 3 Llueve sobre la Ciudad            Los Bunkers                        3:56    
────────────────────────────────────────────────────────────────────────────────
  ▶ Estrechez de Corazón · Los Prisioneros                           ⇄ on  ↻ all
0:00 ╸─────────────────── 6:21                                        Volume 85%
────────────────────────────────────────────────────────────────────────────────
 Tab focus  ↑↓/jk move  Enter play  Space play/pause  s shuffle  r repeat       
 +/- volume  / search  l library  S shuffle-list  F1 help  F2 language          
 F3 theme  q quit  Esc back/quit                                                
```

### Después — 120×34

```
Pulso                                                                                                            playing
Buscar: escribe para buscar                                                                                     @usuario
────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────
┌─ BIBLIOTECA ──────────┐ ┌─ Rock Chileno · 42 canciones ───────────────────────────────┐ ┌─ A CONTINUACIÓN ───────────┐
│> Rock Chileno · ana   │ │#  Título                   Artista                  Duración│ │1 Huelga de Dolores         │
│  Jazz · beto          │ │─────────────────────────────────────────────────────────────│ │  Los Miserables            │
│                       │ │>1 Tren al Sur              Los Prisioneros          5:36    │ │                            │
│                       │ │ 2 Estrechez de Corazón     Los Prisioneros          6:21    │ │                            │
│                       │ │ 3 Llueve sobre la Ciudad   Los Bunkers              3:56    │ │                            │
│                       │ │                                                             │ │                            │
│                       │ │                                                             │ │                            │
│                       │ │                                                             │ │                            │
│                       │ │                                                             │ │                            │
│                       │ │                                                             │ │                            │
│                       │ │                                                             │ │CONTEXTO                    │
│                       │ │                                                             │ │Rock Chileno                │
│                       │ │                                                             │ │⇄ activado · ↻ todo         │
└───────────────────────┘ └─────────────────────────────────────────────────────────────┘ └────────────────────────────┘
────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────
                                                                                                                        
├─ ▶ Reproduciendo  ▸  ⇄ activado  ▸  ↻ todo  ▸  Volumen 85% ──────────────────────────────────────────────────────────┤
│                        Estrechez de Corazón                                                                          │
│  ┌──────────────────┐  Los Prisioneros                                                                               │
│  │                  │  La Cultura de la Basura                                                                       │
│  │                  │  0:00 / 6:21 (-6:21)                                                                           │
│  │                  │  ╸─────────────────────────────────────────────────────────────────────────────────────────────│
│  │                  │                                                                                                │
│  │        EC        │                                                                                                │
│  │                  │                                                                                                │
│  │                  │                                                                                                │
│  │                  │                                                                                                │
│  └──────────────────┘                                                                                                │
│                        Lista: Rock Chileno                                                                           │
└──────────────────────────────────────────────────────────────────────────────────────────────────────────────────────┘
```

### Después — 80×24

```
Pulso                                                                    playing
Buscar: escribe para buscar                                             @usuario
────────────────────────────────────────────────────────────────────────────────
BIBLIOTECA: Rock Chileno                                            l biblioteca
Rock Chileno · 42 canciones                                                     
#  Título                            Artista                            Duración
────────────────────────────────────────────────────────────────────────────────
>1 Tren al Sur                       Los Prisioneros                    5:36    
 2 Estrechez de Corazón              Los Prisioneros                    6:21    
 3 Llueve sobre la Ciudad            Los Bunkers                        3:56    
                                                                                
                                                                                
────────────────────────────────────────────────────────────────────────────────
                                                                                
├─ ▶ Reproduciendo  ▸  ⇄ activado  ▸  ↻ todo  ▸  Volumen 85% ──────────────────┤
│              Estrechez de Corazón                                            │
│  ┌────────┐  Los Prisioneros                                                 │
│  │        │  La Cultura de la Basura                                         │
│  │   EC   │  0:00 / 6:21 (-6:21)                                             │
│  │        │  ╸───────────────────────────────────────────────────────────────│
│  └────────┘                                                                  │
│                                                                              │
│              Lista: Rock Chileno                                             │
└──────────────────────────────────────────────────────────────────────────────┘
```

### Después con carátula — 120×34

La carátula real sustituye al bloque de iniciales dentro de la misma ranura. Se dibuja con una imagen de dos tonos para que el medio bloque se lea en texto plano; las pruebas de la etapa comprueban las diez filas de veinte medios bloques `▀` con primer plano y fondo de 24 bits, y el título, el tiempo, la barra y el contexto repartidos alrededor de la imagen.

```
Pulso                                                                                                            playing
Search: type to search                                                                                             @user
────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────
┌─ LIBRARY ─────────────┐ ┌─ Rock Chileno · 1 tracks ───────────────────────────────────┐ ┌─ UP NEXT ──────────────────┐
│> Rock Chileno · ana   │ │#  Title                    Artist                   Duration│ │○ Nothing here yet          │
│  Jazz · beto          │ │─────────────────────────────────────────────────────────────│ │                            │
│                       │ │>1 Estrechez de Corazón     Los Prisioneros          6:21    │ │                            │
│                       │ │                                                             │ │                            │
│                       │ │                                                             │ │                            │
│                       │ │                                                             │ │                            │
│                       │ │                                                             │ │                            │
│                       │ │                                                             │ │                            │
│                       │ │                                                             │ │                            │
│                       │ │                                                             │ │                            │
│                       │ │                                                             │ │CONTEXT                     │
│                       │ │                                                             │ │Rock Chileno                │
│                       │ │                                                             │ │⇄ off · ↻ off               │
└───────────────────────┘ └─────────────────────────────────────────────────────────────┘ └────────────────────────────┘
────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────
                                                                                                                        
├─ ▶ Playing  ▸  ⇄ off  ▸  ↻ off  ▸  Volume 37% ───────────────────────────────────────────────────────────────────────┤
│                        Estrechez de Corazón                                                                          │
│  ▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀  Los Prisioneros                                                                               │
│  ▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀  La Cultura de la Basura                                                                       │
│  ▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀  0:00 / 6:21 (-6:21)                                                                           │
│  ▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀  ╸─────────────────────────────────────────────────────────────────────────────────────────────│
│  ▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀                                                                                                │
│  ▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀                                                                                                │
│  ▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀                                                                                                │
│  ▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀                                                                                                │
│  ▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀                                                                                                │
│  ▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀▀                                                                                                │
│                                                                                                                      │
└──────────────────────────────────────────────────────────────────────────────────────────────────────────────────────┘
```

Una descarga fallida deja la vista idéntica a la de una pista sin carátula, sin aviso ni fila de error.

## Corte de revisión

La rama `feat/tui-reformulation` acumula cinco commits y unas 771 líneas autoradas desde `947e4ab`, por encima de la rebanada de ~400 que conviene revisar de una vez. El corte queda aquí, al cerrar E1 con sus correcciones:

| Commit | Contenido |
| --- | --- |
| `a4a7031` | `docs(agentes)`: convenciones de commits |
| `6e068c1` | `docs(odd)`: plan por etapas con la versión A |
| `eaee570` | `feat(tui)`: caja del reproductor (E1) |
| `1d5963f` | `fix(tui)`: correcciones de la revisión de E1 |

La entrega va como PR encadenadas, todas en borrador:

- **#9** — plan, E1 y sus correcciones, sobre `master` (7 commits).
- **#10** — E2 y sus correcciones, sobre la rama de #9 (9 commits).
- **#11** — E3 y sus correcciones, sobre la rama de #10.
- **#12** — E4 y la carátula real, sobre la rama de #11.
- **#13** — E5, documentación y regresión, sobre la rama de #12.
- **#14** — E6, verificación visual, sobre la rama de #13.

Conviene revisar en orden. Con #14 el plan queda cubierto salvo los dos huecos que declara E6, ambos de entorno.

## Log

- L1 — User: “quiero una reformulacion de la ui https://github.com/LargeModGames/spotatui quiero que hagas una tui muestrame la propuesta en formato tui”
- L2 — Proposal shown as terminal mockups: three zones with focus, upper status line and a transport footer; taken from spotatui the pane focus, density and control bar, not its sources, visualizer or plugins.
- L3 — User: “hay que quitar los comandos de abajo, tambien hay que quitar el tab para pasar de foco sino que cada parte tendra un comando especifico el reproductor estara abajo …”. Revised proposal: no hint bar, no `Tab`, per-zone keys, and the player as a bottom box whose top border carries play state, shuffle, repeat and volume, with cover, title, album/artist and the time bar inside.
- L4 — User: “quiero que generes el plan”. Delivery is split into player, layout, command model, docs/tests and visual verification. No implementation started and no branch created yet.
- L5 — User: “1. es posible agregar la caratula? verifica si es posible 2. genera varias versiones en formato tui 3. arregla los tamaos, 4. la aplicacion se cerrar con q hay que definir bien los controles y evitar duplicaciones 5. en plan debe estar por etapas repara las etapas actualiza el plan”
- L6 — Carátula verificada como posible sin dependencias nuevas: la URL viaja en las respuestas ya pedidas (`catalog.go:73-80`, `library_source.go:44-49`) y el dibujo usa `▀` con color de 24 bits de la biblioteca estándar; Soloist no transporta imagen (`transport.go:33-40`, `219-236`), así que el mapa `URI → URL` y una llamada de respaldo resuelven la pista sonando.
- L7 — Tres versiones dibujadas con ancho exacto comprobado fila por fila y la banda estrecha 80×24 común; la matriz de tamaños, la tabla de controles y las seis etapas reemplazan las cinco tareas anteriores. Se descartan B (un solo marco) y C (foco expandido).
- L8 — Riesgo vigente: E3 cambia contratos de teclado que las pruebas actuales fijan (`player_test.go`, `help_test.go`, `modes_regression_test.go`, `u3_regression_test.go`), así que E5 debe actualizarlas en el mismo commit que documenta el cambio; E1 y E2 reescriben la misma `View()` de 2318 líneas y conviene vigilar el tamaño de cada commit. Comprobado al cerrar E3: la predicción de que `modes_regression_test.go` y `u3_regression_test.go` fijaban `Tab` o `F1` era falsa —no los nombran— y los seis archivos que sí los fijaban se adaptaron, con la suite completa en verde.
- L9 — Decisión del usuario: versión **A — tres cajas** como cromo de E2. B y C quedan descartadas y solo se registran como alternativas; E1, E3, E4, E5 y E6 no cambian.
- L10 — Las cinco bandas quedan dibujadas por un prototipo ejecutable (`/tmp/pulso-preview.py`, modo `--live` para verlo en la terminal con colores y teclas) y sus capturas, comprobadas fila por fila, reemplazan las maquetas manuales del documento. La banda mínima no lleva tabla: a 40×12 el marco, el encabezado y el reproductor dejan una sola fila de contenido.
- L11 — User: “lo de desk o spotify connect deks no tiene sentido porque siempre se ocupara en la consola”. El diseño pierde el dispositivo: la línea de estado queda con reproducción, aleatorio, repetición y volumen, y las maquetas se regeneran sin esa columna.
- L12 — E1 implementada en `internal/ui/player.go`: la caja dibuja su línea de estado en el borde superior con degradación por ancho (palabra de estado → etiqueta de volumen → porcentaje → puntos suspensivos), bloque de iniciales de 10×5 desde 80 columnas, título, artista, álbum, barra de tiempo y línea de contexto. El pie de pistas pasa a ceder filas y la reproducción remota deja de ocupar una columna fija. `go test ./...` verde en Linux; las pruebas que fijaban la barra plana se adaptaron al tamaño donde su regla sigue siendo observable.
- L13 — Revisión nativa del candidato (lentes readability y reliability): aprobada con cinco hallazgos informativos. Se corrigen los cinco: la caja comparte una sola decisión de estado con su línea superior, así que una desconexión ya no muestra el título guardado ni el bloque de iniciales; la altura interior se recorta cuando la ventana es baja, para que el borde inferior nunca se pierda; las filas interiores se reparten con una prioridad declarada en vez de offsets literales; el bloque de iniciales se construye con `playerArtRows` como fuente de altura; y `hintBudget` pierde el parámetro muerto y su comentario dice la verdad: el pie puede quedarse sin filas. Dos pruebas nuevas fijan la pista obsoleta y la ventana ancha y baja.
- L14 — Segunda revisión nativa del candidato de correcciones (lente reliability): el revisor pidió refuter y el refuter confirmó un hallazgo crítico `R3-001` — a 40×12 el pie volvía a dibujarse y la caja se encogía, cambiando el marco de línea base. Corrección aceptada con un plan de 40 líneas de diff: el presupuesto del pie exige ahora que el cuerpo tenga sus filas mínimas, así que en ventanas bajas el pie cede entero antes que la tabla. El validador dirigido aprobó la corrección; queda un aviso informativo `R3-002` sobre la posición guardada frente a un total en cero, pendiente para E5.
- L15 — E2 implementada: la vista pasa de pila plana a la matriz de bandas con el cromo de tres cajas —biblioteca, tabla y cola con su bloque de contexto—, el borde acentuado de la zona con foco, la forma apilada en compacta, solo contexto en la mínima y un mensaje de tamaño por debajo de 40×12. Las frases de estado de sesión que la zona no puede mostrar se promueven a una fila de ancho completo sobre la caja del reproductor en vez de truncarse: el aviso de autorización de listas mide 74 celdas y a 120 columnas la caja de la tabla tiene 61. La biblioteca media crece a 24 columnas para que un nombre de lista de 21 celdas quepa entero.
- L16 — Revisión nativa de E2: el revisor pidió refuter y el refuter confirmó un hallazgo crítico `R3-001` — la matriz saltaba a la banda mínima cuando faltaba un solo renglón de altura (120×29, 100×25, 80×19) y la tabla desaparecía. Corrección con plan de 90 líneas de diff: escalera monótona de bandas, tabla también en la banda mínima cuando hay filas, y pruebas de frontera en 120×29, 100×25, 80×19, 119×40, 99×40 y 79×24. El validador dirigido aprobó. Queda el aviso `R3-002`: el prompt de confirmación de quitar una pista no lleva marcador y por eso se recorta en la caja en vez de promoverse a todo el ancho; las pruebas de desborde cubren la oferta de permiso pero no esa confirmación.
- L17 — Corte de revisión actualizado: la rama acumula diez commits y unas 1.770 líneas autoradas desde `947e4ab`. E2 por sí sola son 951 líneas y una revisión nativa con refuter y validador. La entrega recomendada sigue siendo PR encadenado: el primero con el plan, E1 y sus correcciones; el segundo con E2.
- L18 — Verificación independiente de E2: la suite completa en verde, las diez bandas con ancho exacto por fila y sin desborde, el modelo de teclado intacto, la caja de E1 idéntica byte a byte y las frases largas completas, salvo una: la confirmación de quitar una pista se recortaba a 140×40 y 120×34 porque la promoción exigía que la frase empezara con un glifo de estado. Corregido en la causa: la promoción lee la marca de la fila, no su texto, y la confirmación de 92 celdas se ve entera sobre la caja del reproductor. Se retiraron los dos escalones inalcanzables de la línea de estado y se corrigió el comentario de `sidebarRows`, que ninguna banda dibuja ya. Cinco pruebas nuevas fijan la matriz exacta, los tamaños límite, la etiqueta de volumen desde 40 columnas, la presencia del pie y la confirmación completa.
- L19 — Segunda verificación independiente, sobre la primera corrección: confirmó el arreglo de la confirmación de quitar y encontró un defecto que yo había introducido al retirar «código muerto». Los dos escalones porcentuales de la línea de estado solo eran inalcanzables en inglés: en español «Volumen 37%» no cabe en la banda mínima, así que a 40 y 41 columnas el valor del volumen desaparecía y a 42 se leía un número falso. Restaurado el escalón alcanzable y retirado solo el que ninguna ventana alcanza. La prueba del volumen se reescribió para las dos lenguas y las nueve bandas —la anterior pasaba con y sin arreglo, era vacua— y se agregó la promoción en español. Tercera verificación: suite verde, banda mínima con el porcentaje en español, forma con etiqueta idéntica byte a byte donde cabe, y las dos pruebas nuevas se comprueban load-bearing al revertir cada arreglo.
- L20 — Tercera revisión nativa, sobre las correcciones de E2 (252 líneas, lente reliability): aprobada en la primera pasada, sin refuter y sin hallazgos, con el revisor comprobando el volumen en español y la promoción sin glifo. La autoridad quedó consumida; la rama suma quince commits y unas 1.994 líneas autoradas.
- L21 — E3 implementada: el modelo de teclado pasa a ser por zonas, sin `Tab` y sin pie de pistas. Se retiran `Tab`/`Shift+Tab` con `playerStep` y `playerFocusPlaying`, `F1`, `p` en sus dos sentidos y `S`; `b` queda como anterior único, la lista restringida se reproduce con `Enter`, `s` se acota por zona, y `q`/`Ctrl+C` salen mientras `Esc` solo vuelve a la tabla o cancela. La ayuda se reescribe por ámbitos —Global, Zonas, Biblioteca, Tabla, Búsqueda— y es la única referencia de teclas. Donde estaba el pie queda una fila en blanco: ninguna banda crece. Seis archivos de prueba adaptados; los ayudantes que movían el foco con `Tab` ahora usan la letra de su zona.
- L22 — Verificación independiente de E3: encontró un bloqueante y una afirmación falsa mía. `s` conservaba un tercer ámbito —con la lista abierta y el foco en la tabla mezclaba la lista— que el plan no declara: ahora la lista solo se mezcla desde la biblioteca. Y la fila liberada por el pie no quedaba en blanco: el marco perdía dos filas de alto (38 de 40, 32 de 34). Retirada la reserva, las filas vuelven a la tabla y el marco llena la ventana; 40×12 pasa de 11 a 12 filas y el borde inferior de la caja queda en la última.
- L23 — Corrección exigida por el refuter de E3 (hallazgo `R3-002`, inferencial): afirmaba que dos suites sin adaptar seguían fijando `Tab`/`F1` y que la suite no estaba verde, deduciéndolo de la nota de riesgo de L8. La nota era una predicción mía, no un hecho: `modes_regression_test.go`, `u3_regression_test.go` y `support_test.go` no nombran esas teclas, y `go test ./... -count=1` pasa en los ocho paquetes. La corrección es documental: L8 queda con lo comprobado y no con lo supuesto.
- L24 — Revisión nativa de E3 (lente reliability): el revisor pidió refuter, el refuter confirmó un bloqueante inferencial `R3-002` —que dos suites sin adaptar seguían fijando `Tab`/`F1`— y ese bloqueante resultó ser falso: nacía de la nota de riesgo de L8, que era una predicción mía. `go test ./... -count=1` pasa en los ocho paquetes y ni `modes_regression_test.go` ni `u3_regression_test.go` nombran esas teclas. Corrección documental, validador dirigido aprobado y autoridad consumida. Queda un aviso informativo con disposición `refuted` sobre `closePlaylist` y las listas restringidas, para revisar en E5.
- L25 — E4 implementada: la carátula real sustituye al bloque de iniciales dentro de la ranura de E1. `album.images` se parsea de `/v1/search` y `/v1/playlists/{id}/items` —sin endpoint, payload ni scope nuevos— y la pista que ninguna página cargada trajo se resuelve con una sola consulta `GET /v1/tracks/{id}?fields=album.images`. La descarga usa la biblioteca estándar (`image/png`, `image/jpeg`), la imagen se reduce a 10×10 píxeles por promedio de bloque y se dibuja con `▀` y color de 24 bits. La verificación independiente pasó los siete puntos y encontró dos avisos: se pedía carátula en ventanas donde la ranura no existe —ahora la regla de la ranura es una sola y la comparten el dibujo y la petición— y el tope de bytes no acotaba los píxeles decodificados —ahora `DecodeConfig` rechaza más de un millón de píxeles antes de decodificar—. La matriz de tamaños corrige la banda compacta: sí lleva carátula.
- L26 — Revisión nativa de E4 con dos lentes (`risk` y `reliability`): aprobada en la primera pasada, sin refuter y sin hallazgos. Se revisaron la descarga de la carátula, la consulta de respaldo, la caché, el tope de píxeles y la regla única de la ranura. Autoridad consumida; la etapa se publica como PR #12 encadenada sobre #11.
- L27 — E5 implementada: la documentación de usuario se reescribe en las dos lenguas sobre la interfaz entregada —tres zonas, caja con carátula, cinco bandas, tabla de controles de dieciséis filas y `?` como única referencia—, y `docs/UI-PLAN.md` deja de prometer una interfaz futura. Se cierra el aviso `R3-002` de E1: la caja desconectada ya no conserva la posición guardada, muestra `0:00 / --:--`. El aviso `refuted` sobre `closePlaylist` se revisó contra el código: el refuter tenía razón y no hay defecto, porque `applyTrackPage` marca `loadedTracks` antes de mirar el error.
- L28 — Revisión nativa de E5 con dos lentes (`readability` y `reliability`): aprobada con un aviso informativo. La matriz de tamaños escribía las bandas con cotas superiores excluyentes (`100 ≤ ancho < 120`), así que 120×29 no caía en ninguna fila aunque la escalera lo resuelve a la banda media; la prueba de guarda reproducía la misma contradicción. Corregido en los cuatro documentos y en la prueba: las condiciones de ancho son pisos, el alto decide, y las filas dicen qué banda usan 120×29 y 100×25.
- L29 — La revisión del candidato acumulado de la rama completa fue rechazada nativamente con `lens_context_budget_exceeded`: diecinueve archivos y cinco etapas superan el presupuesto de contexto del revisor, y reintentar el mismo candidato no puede funcionar. La continuación válida es reducir el alcance, así que se revisó aparte el único commit pendiente —las dos líneas del registro de la quinta PR—, que el proveedor evaluó como riesgo bajo y aprobó sin lentes ni ejecuciones. Cada etapa E1–E5 conserva su propia revisión aprobada.
- L30 — E6 ejecutada: capturas reales del estado entregado y de la línea base a 120×34 y 80×24 con el mismo estado de reproducción, guardadas aquí; filas de ancho exacto en ambas. La carátula quedó verificada en E4 con sus filas y color de 24 bits. `GOOS=windows GOARCH=amd64 go build ./...` y `GOOS=windows go vet ./...` pasan, así que el árbol compila y se analiza para Windows; sus pruebas no se pueden ejecutar en esta máquina Linux y no se inventa un resultado. La descarga real contra la CDN de Spotify sigue sin ejercerse por falta de red y credenciales: queda como paso manual documentado.
