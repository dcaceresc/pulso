# Retoques de la interfaz entregada

Correcciones y cambios pedidos sobre la reformulación ya entregada (etapas E1–E6): un fallo al quitar canciones, carátulas más grandes y nítidas, la caja del reproductor reorganizada, la reproducción de una lista sin cortes al terminar y una tecla nueva para descubrir canciones parecidas.

## Specs

- S1 — “primero si trato de eliminar una cancion de una playlist tengo que reproducir otra playlist y volver a la playlist donde esta la cancion a elminar y ahi se elimina sino hago eso y estoy en la misma playlist no funciona”
- S2 — “las caratulas aparecen pixeladas, tambien en la parte del reproducir hay demasiado espacio puedes aumentar el tamaño de las caractulas que el tiempo quede justo debajo del albun y que el tiempo quede de esta forma tiempo reproduccion /tiempo total (cuanto queda) barra de tiempo, esta barra de tiempo alargala hasta el final”
- S3 — “cuando yo entre a una playlist se reproduciran solo las cancionaes de la playlist hasta que se acaben y se agregarn nuevas, si aprieto s dentro de la playlist se escuharan las canciones de la playlist de forma aleatorio hasta que se acaben y se reproduciran canciones nuevas”
- S4 — “agrega nueva funcionalidad descubrir si yo aprieto d en una cancion se escucharan canciones random asociadas al estilo de la cancion como ahora esta funcionando s en la lista”
- S5 — “si es recomendaciones usa la tecla r no la d”

## Decisiones

- **Tecla de descubrir: `d`.** S5 proponía `r`, pero `r` cicla la repetición y está documentada y fijada por pruebas; el usuario eligió que `r` siga siendo repetir y que descubrir vaya en `d`, que está libre en el reproductor.
- **Fuente de descubrir: `/v1/recommendations`** con la pista en reproducción como semilla. Si la aplicación de Spotify no conserva acceso al endpoint —devolvió 403—, la tecla explica el motivo y no inventa una lista.
- **Carátula de 20×10 celdas**, que con medio bloque son 20×20 píxeles (400 píxeles, el cuádruple de los 100 de hoy), con la caja del reproductor de 12 filas interiores en las bandas amplia y media.
- **Tiempo** con la forma `transcurrido / total (restante)` justo debajo del álbum, y la barra ocupando el ancho restante de la caja.

## Hallazgos del código (evidencia)

| Hecho | Evidencia |
| --- | --- |
| La fila lleva el snapshot de la lectura que la produjo | `internal/ui/library.go:195` |
| El snapshot se lee en una petición aparte | `internal/spotify/library_source.go:118-126` |
| Tras un guardado aceptado no se releía la lista: el snapshot cacheado quedaba atrás del de Spotify | `internal/ui/player.go:1185-1191` |
| La retirada se bloquea con el campo de búsqueda activo y sin snapshot en la fila | `player.go:924`, `player.go:928` |
| La carátula es de 10×5 celdas y pide la imagen más pequeña que cubra 10 px | `player.go:89-90`, `internal/spotify/catalog.go:26,117-143` |
| La caja dibuja 10 filas interiores con tres vacías (0, 7 y 9) | `player.go:2068-2130`, `player.go:1881-1895` |
| La barra se corta a 20 celdas y no llega al borde | `player.go:1513-1526` |
| Soloist no tiene comando de radio ni de autoplay | `internal/soloist/transport.go:673-704` |
| `/v1/recommendations` no está implementado; los scopes actuales son de listas | `internal/spotify/oauth.go:28` |

## Tasks (etapas)

- R1 [S1] — Quitar una pista en la misma lista: releer la lista abierta tras cualquier escritura aceptada, para que las filas no viajen con un snapshot anterior, y cubrirlo con una prueba que envíe dos retiradas seguidas sin cambiar de lista. Aceptación: la segunda retirada usa el snapshot de la lectura nueva y no la versión vieja. Hecho: toda escritura aceptada relee la lista abierta y descarta el snapshot pendiente; `TestRemoveAfterAddUsesTheFreshlyReadSnapshot` fija que, tras agregar, la retirada en la misma lista nombra la lectura nueva y no pide confirmación otra vez. Ruta: parent. Commit: —.
- R2 [S2] — Carátula de 20×10 celdas: subir `CoverWidth` a 20, la ranura a 20×10 y la caja a 12 filas interiores en las bandas amplia y media; pedir a Spotify una imagen de al menos 20 px, que es el ancho en píxeles de la ranura. Aceptación: la ranura dibuja 10 filas de 20 medios bloques con color de 24 bits, el descargador pide una imagen mayor, y ninguna fila desborda. Hecho: la ranura se elige por banda —20×10 en amplia y media, 10×5 en compacta, ninguna en la mínima— y una ventana sin ranura no pide ni descarga imagen. Ruta: parent. Commit: —.
- R3 [S2] — Tiempo bajo el álbum: `transcurrido / total (restante)` en la fila siguiente al álbum y la barra ocupando el ancho restante hasta el borde de la caja; sin filas interiores vacías en la banda amplia. Aceptación: la línea muestra los tres valores, la barra llega al borde y ninguna fila queda vacía. Ruta: parent. Commit: —.
- R4 [S3] — Reproducción de una lista: al entrar se reproduce la lista sin forzar repetición, de modo que al terminar Spotify siga con lo que corresponda; `s` en la biblioteca mezcla la lista y continúa igual al agotarse. Aceptación: una lista que termina no se repite sola y la reproducción continúa; documentado en la ayuda y en las dos INSTRUCTIONS. Ruta: parent. Commit: —.
- R5 [S4, S5] — Descubrir con `d`: pedir `/v1/recommendations` con la pista en reproducción como semilla y reproducir el resultado mezclado; si el endpoint responde 403 o falla, explicarlo en una línea y no tocar la reproducción. Aceptación: `d` reproduce recomendaciones cuando hay acceso, explica el motivo cuando no, y aparece en la ayuda y en las dos INSTRUCTIONS. Ruta: parent. Commit: —.

## Log

- L1 — User: “estoy revisando la app y he encontre algunos bugs, primero si trato de eliminar una cancion de una playlist tengo que reproducir otra playlist y volver a la playlist donde esta la cancion a elminar y ahi se elimina sino hago eso y estoy en la misma playlist no funciona, segundo las caratulas aparecen pixeladas, tambien en la parte del reproducir hay demasiado espacio puedes aumentar el tamaño de las caractulas que el tiempo quede justo debajo del albun y que el tiempo quede de esta forma tiempo reproduccion /tiempo total (cuanto queda) barra de tiempo, esta barra de tiempo alargala hasta el final, tambien cosas que hay que cambiar cuando yo entre a una playlist se reproduciran solo las cancionaes de la playlist hasta que se acaben y se agregarn nuevas, si aprieto s dentro de la playlist se escuharan las canciones de la playlist de forma aleatorio hasta que se acaben y se reproduciran canciones nuevas, agrega nueva funcionalidad descubrir si yo aprieto d en una cancion se escucharan canciones random asociadas al estilo de la cancion como ahora esta funcionando s en la lista”
- L2 — User: “si es recomendaciones usa la tecla r no la d”. Como `r` cicla la repetición y está documentada y fijada por pruebas, se preguntó por el choque; el usuario resolvió que `r` siga siendo repetir y que descubrir quede en `d`.
- L3 — Fuente de descubrir confirmada por el usuario: intentar `/v1/recommendations` con la pista actual como semilla. Si Spotify devuelve 403, la tecla lo explica.
- L4 — Carátula confirmada en 20×10 celdas con la caja de 12 filas interiores. La cuenta correcta es 20×20 píxeles —400, el cuádruple de hoy—: el borrador decía 40×20 y era un error de aritmética, corregido al implementar.
- L5 — El código confirma una causa del fallo de retirada: tras un guardado aceptado la lista no se releía, así que las filas conservaban un `snapshot_id` anterior. Se corrigió en R1. Queda por confirmar la segunda causa —la puerta de la búsqueda activa o un snapshot vacío— con el mensaje que la app muestra al pulsar `x`; ese dato no se ha recibido.
- L6 — La revisión nativa de R2 y R3 encontró dos cosas. La primera era un defecto real: la ranura se elegía por filas interiores, así que una ventana de banda mínima como 120×17 dibujaba la carátula de 10×5 y hasta descargaba una imagen. Corregido: la banda resuelta decide, y donde no hay ranura no hay petición ni descarga. La segunda afirmación del hallazgo decía que la banda compacta tampoco debía llevar carátula; el validador dirigido la exigió entera y **detuvo el linaje** `review-1b1868270937a83d` por no quitarla. Esa exigencia contradice la decisión del usuario (L4 y la respuesta posterior: «carátula chica en la compacta»), así que el requisito prevalece: el linaje queda detenido como desacuerdo registrado y la entrega sigue la política ordinaria del repositorio. Nada de esto deja código en rojo: `go test ./...` pasa en los siete paquetes.
