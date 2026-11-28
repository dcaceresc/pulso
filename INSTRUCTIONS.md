# Instrucciones de Pulso

Español (predeterminado) · [English](INSTRUCTIONS.en.md)

Esta guía explica cómo instalar, configurar y usar Pulso. La aplicación está pensada para mi flujo personal: escuchar Spotify desde la terminal en Windows y Linux usando el reproductor oficial de Spotify, sin depender de `librespot`.

## Contenido

- [Instalación recomendada](#instalación-recomendada)
- [Primer arranque](#primer-arranque)
- [Emparejar Soloist](#emparejar-soloist)
- [Conectar Spotify para buscar, ver listas y agregar o quitar canciones](#conectar-spotify-para-buscar-ver-listas-y-agregar-o-quitar-canciones)
- [Uso diario](#uso-diario)
- [Atajos](#atajos)
- [Idioma y temas](#idioma-y-temas)
- [Dónde se guardan los datos](#dónde-se-guardan-los-datos)
- [Diagnóstico y solución de problemas](#diagnóstico-y-solución-de-problemas)
- [Pruebas](#pruebas)
- [Límites conocidos](#límites-conocidos)
- [Estructura del repositorio](#estructura-del-repositorio)

## Instalación recomendada

Instala Pulso una vez desde la raíz del repositorio y ejecútalo después desde cualquier carpeta:

```sh
go install ./cmd/pulso
pulso
```

Go 1.22 o posterior solo es necesario para compilar. El binario instalado no necesita Go. La reproducción la hace **Spotify Soloist**, que pulso descarga e instala en tu carpeta de usuario si falta; no requiere permisos de administrador ni modifica `PATH`.

La versión instalada con `go install` no se actualiza sola: repite el comando cuando el repositorio cambie.

## Primer arranque

Al ejecutar `pulso` sin argumentos se abre el asistente a pantalla completa. Comprueba en este orden:

1. Si Soloist está instalado y responde en su WebSocket local.
2. Si falta, ofrece descargar el binario oficial de Spotify para tu sistema y arquitectura. Nada se descarga sin que confirmes con `Y`.
3. Si Soloist está instalado pero detenido, pide la clave de Soloist para arrancarlo.
4. Si la sesión de Spotify está guardada y vigente, entra directo al reproductor.

## Emparejar Soloist

El emparejamiento lo autoriza Spotify desde su aplicación; no ocurre dentro de Pulso:

1. Crea tu clave de Soloist en el [panel de Spotify para desarrolladores](https://developer.spotify.com/dashboard/soloist). Necesitas Spotify Premium y la clave es personal: no se comparte ni se publica.
2. Deja el asistente abierto y conecta el teléfono y el equipo a la misma red local.
3. En la aplicación de Spotify del teléfono, reproduce algo, abre el selector de dispositivos y elige **Pulso** (o el nombre de tu dispositivo Soloist existente).
4. En cuanto Spotify confirma la conexión, Pulso abre el reproductor solo. No hace falta pulsar Enter, aunque puedes usarlo para comprobar de inmediato.

Si el dispositivo no aparece, revisa que no haya una red de invitados, aislamiento de clientes o una VPN bloqueando el descubrimiento local.

Si el emparejamiento no avanza porque la clave quedó mal escrita, pulsa `b`: vuelves al paso de la clave con el campo vacío. Al enviar la nueva, Pulso detiene el Soloist que había arrancado con la clave anterior y lo inicia otra vez; `Enter` solo vuelve a comprobar el estado.

### Diagnóstico del emparejamiento

En el paso de emparejamiento el asistente diagnostica por qué el teléfono puede no ver el equipo y resume el resultado con un indicador al principio de la línea: `● OK` en verde cuando no hay nada que revisar, `● Revisar` en ámbar cuando hay un riesgo pero el emparejamiento puede funcionar (por ejemplo, un puente de contenedores compitiendo por el anuncio), y `● Bloqueado` en rojo cuando el diagnóstico identifica una política o una regla que impide el acceso. La palabra acompaña siempre al color, así que se entiende también en una terminal sin color.

El atajo `d: diagnóstico` aparece solo cuando hay algo que revisar: abre el detalle con los hallazgos y, si hay algo que aplicar, los comandos exactos. `Enter` vuelve al paso de emparejamiento. Si el detalle no puede proponer ningún comando, lo dice y no ofrece `a`.

El diagnóstico revisa tres cosas:

- La dirección que usa tu equipo para llegar a la red local.
- Las interfaces que compiten por la única dirección que Soloist anuncia: puentes de contenedores (los de Docker, por ejemplo), VPN y túneles.
- El estado del cortafuegos y los bloqueos que el diagnóstico puede identificar.

Un puente o una VPN rompen el emparejamiento porque Soloist anuncia una sola dirección al arrancar y no ofrece ninguna opción para fijar la interfaz: puede anunciar la dirección del puente en lugar de la de tu red local, y el teléfono busca en la red equivocada. La recuperación concreta es arrancar Pulso con ese puente ausente, emparejar y volver a levantar el puente después. El anuncio sigue siendo válido hasta que Soloist se reinicie.

El puerto de control de Soloist se elige al arrancar y cambia, así que una regla para un puerto concreto queda obsoleta en el siguiente arranque. Lo que se recomienda es un permiso acotado a la red local (por ejemplo `sudo ufw allow from 192.168.1.0/24`), que sigue sirviendo después de que cambie el puerto; es un permiso deliberadamente más amplio que un solo puerto. En Windows el asistente consulta la política efectiva del perfil de la interfaz LAN. Marca el descubrimiento (UDP 5353) como bloqueado solo si confirma que ese perfil bloquea en general las conexiones entrantes no solicitadas. Si no puede concluirlo, muestra «No verificado»; eso no es una prueba de conectividad. El puerto TCP de Spotify Connect no se obtiene del WebSocket local de Soloist y se muestra como desconocido.

El asistente nunca cambia el cortafuegos por sí solo: solo añade reglas después de una confirmación en dos pasos, con `a: aplicar reglas` y una segunda `a`. Cualquier otra tecla cancela la aplicación, y `Esc` sigue cerrando Pulso. Tampoco elimina ni reemplaza reglas existentes, y no necesita permisos de administrador solo para inspeccionar: si no puede leer las reglas, lo dice en pantalla en lugar de suponer que están bien.

### Reglas manuales de cortafuegos

Sustituye `192.168.1.0/24` por el prefijo de tu red local. Para firewalld y nftables, donde el asistente no propone comandos, se indican equivalentes manuales.

**ufw (Debian, Ubuntu):** la regla de descubrimiento ya viene en `before.rules` por defecto (`5353/udp` hacia `224.0.0.251`), así que lo que suele faltar es el tráfico desde la red local:

```sh
sudo ufw allow from 192.168.1.0/24
```

**firewalld (Fedora, RHEL):**

```sh
sudo firewall-cmd --permanent --add-service=mdns
sudo firewall-cmd --permanent --add-rich-rule='rule family="ipv4" source address="192.168.1.0/24" accept'
sudo firewall-cmd --reload
```

Si tu versión de firewalld no trae el servicio `mdns`, sustituye la primera línea por `sudo firewall-cmd --permanent --add-port=5353/udp`.

**nftables:**

```sh
sudo nft add rule inet filter input udp dport 5353 accept
sudo nft add rule inet filter input ip saddr 192.168.1.0/24 accept
```

Estas dos reglas se pierden al reiniciar: escríbelas en `/etc/nftables.conf` y recarga con `sudo nft -f /etc/nftables.conf`. El nombre de la tabla y de la cadena depende de tu configuración; usa los de tu cadena de entrada.

**Firewall de Windows Defender:** el asistente no ofrece cambios de reglas. Una excepción no corrige una política que ignora los permisos entrantes. Si hay un bloqueo confirmado y el dispositivo no aparece en Spotify, revisa la política del perfil LAN con quien administre el equipo. Si el emparejamiento funciona, no cambies el firewall por un estado «No verificado».

## Conectar Spotify para buscar, ver listas y agregar o quitar canciones

Buscar, listar playlists y agregar o quitar una canción de una lista usan la **Spotify Web API**, una autorización distinta del emparejamiento de Soloist:

1. Crea una app gratuita en el [panel de Spotify para desarrolladores](https://developer.spotify.com/dashboard) y copia su **Client ID**. Tiene 32 caracteres hexadecimales; no es la clave de Soloist.
2. En la configuración de esa app, añade exactamente `http://127.0.0.1:8888/callback` como URI de redirección. Spotify exige el puerto explícito en las direcciones locales.
3. El asistente te pide el Client ID una vez y lo guarda. También puedes definirlo antes de arrancar con `PULSO_SPOTIFY_CLIENT_ID`.
4. Aprueba el acceso en el navegador que Pulso abre. El puerto 8888 debe estar libre; si está ocupado, ciérralo y reintenta.

Después de la primera aprobación, la sesión queda guardada y se renueva sola. Solo tendrás que aprobar de nuevo si revocas la app en Spotify o borras el archivo de sesión. Guardar o quitar una canción necesita además permisos de escritura: una aprobación anterior que solo permitía leer no los tiene, y Pulso te ofrece aprobar otra vez en el momento de guardar o quitar. Si Spotify rechaza el cambio sin un motivo reconocible, Pulso te ofrece esa misma aprobación única en lugar de afirmar que no puedes cambiar la lista; solo cuando Spotify dice claramente que la cuenta no puede cambiar esa lista concreta el aviso lo repite. La reproducción nunca depende de esta autorización: si falla o la omites con `s`, los controles y el audio siguen funcionando.

## Uso diario

La pantalla es un único espacio de trabajo con tres zonas y el reproductor al pie:

- **Barra superior:** la búsqueda y tu usuario de Spotify.
- **Biblioteca:** tus listas de reproducción, con el cursor y la letra `l` que lleva el foco ahí.
- **Tabla:** las canciones de la lista elegida, los resultados de la búsqueda o la cola, con número, título, artista y duración.
- **Cola:** en las ventanas anchas una tercera caja muestra lo que sigue y el contexto de la reproducción; `c` devuelve la cola a la tabla y le da el foco.
- **Reproductor:** una caja al pie. Su borde superior lleva el estado —reproduciendo, en pausa, sin conexión—, el aleatorio, la repetición y el volumen; dentro van la carátula de la pista en reproducción —o un bloque con sus iniciales cuando no hay imagen—, el título, el artista, el álbum, el tiempo con la forma `transcurrido / total (restante)` justo debajo del álbum, la barra, que llega hasta el borde de la caja, y la lista o el dispositivo remoto que informa Soloist.

El foco se mueve solo con la letra de cada zona: `l` lleva a la biblioteca, `/` a la búsqueda y `c` a la cola. Dentro del espacio de trabajo `Esc` nunca cierra Pulso: vuelve a la tabla o cancela lo enfocado; el asistente, en cambio, sí sale con `Esc`. Tampoco hay ciclo de foco ni línea de pistas al pie: cada zona tiene su tecla y `?` abre la ayuda, la única referencia de teclas, que las enumera por ámbitos —global, zonas, biblioteca, tabla y búsqueda—.

El tamaño de la ventana decide la forma de la pantalla; las condiciones de ancho son pisos y el alto decide: si el alto no alcanza para una banda, Pulso usa la forma siguiente más rica que sí quepa, nunca la más pobre. Por eso 120×29 usa la banda media y 100×25 la compacta.

| Tamaño | Qué se ve | Reproductor | Carátula |
| --- | --- | --- | --- |
| ancho ≥ 120 y alto ≥ 30 | biblioteca, tabla y cola con su contexto | 12 filas interiores | sí |
| ancho ≥ 100 y alto ≥ 26, también con más de 120 columnas si el alto no llega a la banda amplia | biblioteca y tabla; la cola se abre con `c` | 12 filas | sí |
| ancho ≥ 80 y alto ≥ 20 | una fila con la biblioteca encima de la tabla | 8 filas | no |
| ancho ≥ 40 y alto ≥ 12 | la fila de la biblioteca y el reproductor, y la tabla cuando el alto deja filas para su encabezado | 6 filas | no |
| ancho < 40 o alto < 12 | un mensaje con el tamaño mínimo, sin marco | — | — |

La tabla es la que cede filas y ninguna fila desborda el ancho. La carátula mide veinte columnas por diez filas y solo se dibuja en las bandas amplia y media, que son las que declaran doce filas interiores: la compacta y la mínima no gastan ranura. En la banda mínima la caja del reproductor declara seis filas interiores y una ventana baja las recorta —a 40×12 dibuja tres: título, artista y álbum— para que su borde inferior no se pierda. Solo las bandas con zonas, la amplia y la media, promueven a todo el ancho una frase de estado que no cabe en su caja —un aviso de autorización largo, por ejemplo—; en la compacta y la mínima el aviso es una fila normal y, si es más ancho que la ventana, se recorta.

`a` abre el selector de lista para guardar la canción que suena: `↑` `↓` o `j` `k` eligen la lista, el título del panel nombra la lista apuntada, `Enter` guarda y `Esc` cancela. El aviso de éxito aparece solo cuando Spotify confirmó la lista, y un fallo se muestra como una línea que no interrumpe la reproducción.

`x` pide quitar de la lista abierta la canción marcada: la confirmación nombra la canción y la lista, `Enter` la quita y `Esc` cancela. Cuando Spotify acepta, el aviso nombra la canción y la lista, y la lista se vuelve a leer para que la fila desaparezca. La reproducción y la cola no se tocan.

Con la búsqueda enfocada puedes escribir `q`, `s`, `r`, `+` y `-` sin que se disparen atajos; `Enter` busca y `Esc` sale del campo. `q` y `Ctrl+C` cierran Pulso: `q` sale cuando ningún campo de texto tiene el foco y `Ctrl+C` sale siempre.

Al cerrar Pulso, la reproducción se pausa. Soloist sigue en segundo plano con su emparejamiento para que el siguiente arranque sea inmediato; si quieres detener el proceso, termínalo desde el sistema.

## Atajos

Cada tecla hace una sola cosa. `?` abre la ayuda, la referencia completa por ámbitos: repite esta tabla y añade las teclas del campo de búsqueda y `s` como aleatorio global de la reproducción.

| Ámbito | Tecla | Acción |
| --- | --- | --- |
| Global | `q` / `Ctrl+C` | Salir |
| Global | `Space` | Reproducir o pausar |
| Global | `n` / `b` | Pista siguiente o anterior |
| Global | `+` / `-` | Volumen |
| Global | `←` / `→` | Retroceder o avanzar 10 segundos |
| Global | `r` | Repetir: apagado, lista, pista |
| Global | `F2` / `F3` | Idioma o tema |
| Global | `?` | Ayuda: la única referencia de teclas |
| Global | `Esc` | Volver a la tabla o cancelar lo enfocado |
| Zona | `l` | Biblioteca |
| Zona | `/` | Búsqueda: `q`, `s`, `r`, `+` y `-` son texto |
| Zona | `c` | La tabla muestra la cola y toma el foco; la columna de la ventana ancha solo informa |
| Biblioteca | `j` / `k`, `Enter` | Mover el cursor, abrir la lista |
| Biblioteca | `s` | Reproducir la lista seleccionada en aleatorio |
| Tabla | `j` / `k`, `Enter` | Mover el cursor, reproducir la fila |
| Tabla | `a` / `x` | Guardar la pista sonando o quitar la fila resaltada |

Las flechas `↑` `↓` mueven el cursor igual que `j` `k`. `s` alterna el aleatorio de la reproducción cuando el foco no está en la biblioteca; ahí reproduce en aleatorio la lista seleccionada. La lista restringida, cuyas canciones Spotify no entrega, se reproduce con `Enter`.

`?` o `Esc` cierran la ayuda y vuelven a la zona donde estabas.

`s` en la biblioteca funciona con la URI de la lista aunque no se puedan consultar sus canciones. Pulso espera a que Soloist acepte la reproducción y confirme la lista antes de enviar una segunda orden para activar el aleatorio. No se garantiza que la primera canción sea aleatoria, y otro cliente de Spotify puede cambiar el contexto entre la confirmación y la segunda orden. Si la confirmación no llega a tiempo, Pulso no envía la segunda orden; la lista aún puede empezar a reproducirse.

Pulso no cambia Autoplay. Si está activado y Spotify continúa la reproducción al terminar una lista, Spotify elige las canciones.

`a` solo aparece con una canción en reproducción y un catálogo de Spotify que puede escribir. Después de `Enter`, Pulso espera la respuesta de Spotify: si la acepta, muestra «Guardada en …» con el nombre de la lista; si falla, muestra una línea como «No se pudo guardar en …» y la reproducción sigue igual. Cuando Spotify responde que falta el permiso de escritura (por ejemplo, una sesión aprobada antes de existir esta acción), Pulso ofrece aprobar otra vez, abre el navegador, muestra el enlace de aprobación y reintenta el guardado una sola vez. Si la lista es de otra cuenta y no se puede editar, o si la aprobación no se completa, no hay reintento. El texto de la respuesta de Spotify nunca se muestra.

`x` solo aparece con una lista abierta en la tabla, una fila con URI de pista y el snapshot de la lista que estabas viendo, que Pulso lee del objeto de la lista porque la página de elementos no lo trae; si Spotify no lo devuelve, la tecla no aparece. En resultados de búsqueda, en una lista restringida o sin un catálogo que pueda escribir la tecla no hace nada. Pulso quita exactamente una aparición: la que corresponde al snapshot de la lista que estabas viendo, así que una canción repetida conserva sus demás apariciones y se quita una por vez. Después de `Enter` muestra «Se quitó … de …» y vuelve a leer la lista; un fallo queda como una línea como «No se pudo quitar … de …». Si Spotify responde que la lista cambió desde que se leyó, Pulso la relee y pregunta otra vez con la lectura nueva en vez de adivinar cuál aparición borrar. Si falta el permiso de escritura, ofrece la misma aprobación que al guardar y reintenta una sola vez; una lista que esta cuenta no puede editar, o una aprobación que no se completa, no se reintenta. El texto de la respuesta de Spotify nunca se muestra, y la reproducción y la cola siguen igual.

## Idioma y temas

`F2` alterna inglés y español; `F3` recorre los temas estándar, oscuro y claro. La elección se guarda solo cuando pulsas una de esas teclas y se aplica al asistente y al reproductor.

## Dónde se guardan los datos

Todo vive en la carpeta de configuración de tu usuario (`%APPDATA%\pulso` en Windows, `~/.config/pulso` en Linux):

| Archivo | Contenido |
| --- | --- |
| `client-id` | Client ID público de tu app de Spotify |
| `spotify-session.json` | Sesión de Spotify: token de acceso, token de refresco y caducidad |
| `preferences.json` | Idioma y tema |
| `bin/soloist.exe` o `bin/soloist` | Copia de Soloist descargada por Pulso |

Los archivos se crean con permisos privados. Pulso nunca escribe tokens en registros ni en la salida. El audio y el emparejamiento los guarda Soloist en su propio directorio: `%LOCALAPPDATA%\soloist\data` en Windows, o `$XDG_DATA_HOME/soloist` (con `~/.local/share/soloist` por defecto) en Linux.

## Desinstalar

`pulso uninstall` borra lo que Pulso y Soloist guardan en tu usuario. Primero imprime la ruta absoluta de cada objetivo y después espera una confirmación escrita: hay que escribir exactamente `uninstall`. Un Enter vacío o cualquier otra palabra responde «Cancelled; nothing was deleted.» y sale con código 1 sin borrar nada.

Si Soloist está corriendo, el comando no borra nada por su cuenta: lo informa y pregunta `Close it and continue? [y/N]`. Solo con `y` o `Y` comprueba que el proceso del marcador sea realmente Soloist y, si lo es, le pide que termine y espera a que salga; si sigue vivo al agotarse la espera, aborta sin borrar nada. Si no puede confirmar que ese proceso es Soloist, no le envía ninguna señal y aborta. Nunca envía `SIGKILL`; en Windows usa la terminación del sistema.

Se borra el binario de Soloist que descargó Pulso (`bin/soloist` o `bin/soloist.exe`), el Client ID, la sesión de Spotify, las preferencias y los directorios de datos y de caché de Soloist, donde viven el emparejamiento y su caché.

Cada objetivo se informa como `Removed`, `Absent` o `Failed` con el error. Un objetivo que ya no existe no es un problema: ejecutar el comando dos veces termina bien. Si un borrado falla, se informa y se sigue con los demás, pero el código de salida es 1; solo es 0 cuando todo se borró o ya no estaba.

El emparejamiento de Spotify y la autorización de la Web API no se pueden recuperar: después de desinstalar hay que repetir los dos pasos, con la clave de Soloist y con el Client ID. La desinstalación tampoco borra el binario de Pulso: indica su ruta para que lo borres tú, porque `go install` es su dueño.

```sh
bin="$(go env GOBIN)"; [ -n "$bin" ] || bin="$(go env GOPATH)/bin"
rm "$bin/pulso"
```

En Windows el binario está en la misma carpeta como `pulso.exe`. Si una ruta no pertenece a Pulso o Soloist, es tu carpeta personal, la raíz del sistema o el directorio donde estás trabajando, el comando se niega a borrar y termina sin tocar nada.

## Diagnóstico y solución de problemas

`pulso doctor` informa el estado del demonio de Soloist, su WebSocket y el inicio de sesión, sin instalar ni arrancar nada. Usa `pulso doctor --data-dir RUTA` si tu instancia guarda los datos en otro sitio.

| Síntoma | Qué revisar |
| --- | --- |
| El dispositivo no aparece en Spotify | Misma red local, sin VPN ni aislamiento de invitados; en Windows, revisa el diagnóstico de emparejamiento |
| No hay sonido | Salida de audio del sistema; en Linux, PipeWire o PulseAudio |
| Se pide el Client ID otra vez | Revisa `client-id`; debe tener 32 caracteres hexadecimales |
| Spotify rechaza la URI de redirección | Debe ser exactamente `http://127.0.0.1:8888/callback` |
| El puerto 8888 está ocupado | Cierra el proceso que lo usa y reintenta |
| Error de autorización | Revoca la app en Spotify, borra `spotify-session.json` y vuelve a aprobar |
| Soloist sale con código 10 | La compilación caducó a los 90 días: el asistente ofrece actualizarla |
| Playlist sin canciones | Spotify solo entrega el contenido de playlists propias o colaborativas; puedes reproducirla completa igualmente |

## Pruebas

```sh
go test ./...
go vet ./...
```

Las pruebas usan servidores y transportes locales simulados: no hacen peticiones reales a Spotify ni necesitan credenciales.

## Límites conocidos

- No crea, renombra ni borra listas: agrega la canción en reproducción a una lista existente y quita una aparición de una pista de la lista abierta, siempre donde tu cuenta pueda escribir.
- No guarda canciones sin el permiso de escritura de la Web API, y en el modo de desarrollo de Spotify solo puede editar listas propias o colaborativas.
- No funciona sin Spotify Premium.
- Solo reproduce en plataformas con binario oficial de Soloist.
- La búsqueda devuelve diez resultados por página y exige paginar.
- La barra de progreso se interpola entre los informes de Soloist; puede desviarse menos de un segundo.
- El emparejamiento inicial requiere la aplicación oficial de Spotify una vez.

## Estructura del repositorio

```
cmd/pulso      punto de entrada
internal/app   código y pruebas
docs           plan, hoja de ruta y plan de interfaz
odd            documento de la funcionalidad y su registro
```

Las reglas para agentes y colaboradores están en [AGENTS.md](AGENTS.md).

## Desinstalar

`pulso uninstall` borra lo que Pulso y Soloist guardan en tu usuario. Primero imprime la ruta absoluta de cada objetivo y después espera una confirmación escrita: hay que escribir exactamente `uninstall`. Un Enter vacío o cualquier otra palabra responde «Cancelled; nothing was deleted.» y sale con código 1 sin borrar nada.

Si Soloist está corriendo, el comando no borra nada por su cuenta: lo informa y pregunta `Close it and continue? [y/N]`. Solo con `y` o `Y` comprueba que el proceso del marcador sea realmente Soloist y, si lo es, le pide que termine y espera a que salga; si sigue vivo al agotarse la espera, aborta sin borrar nada. Si no puede confirmar que ese proceso es Soloist, no le envía ninguna señal y aborta. Nunca envía `SIGKILL`; en Windows usa la terminación del sistema.

Se borra el binario de Soloist que descargó Pulso (`bin/soloist` o `bin/soloist.exe`), el Client ID, la sesión de Spotify, las preferencias y los directorios de datos y de caché de Soloist, donde viven el emparejamiento y su caché.

Cada objetivo se informa como `Removed`, `Absent` o `Failed` con el error. Un objetivo que ya no existe no es un problema: ejecutar el comando dos veces termina bien. Si un borrado falla, se informa y se sigue con los demás, pero el código de salida es 1; solo es 0 cuando todo se borró o ya no estaba.

El emparejamiento de Spotify y la autorización de la Web API no se pueden recuperar: después de desinstalar hay que repetir los dos pasos, con la clave de Soloist y con el Client ID. La desinstalación tampoco borra el binario de Pulso: indica su ruta para que lo borres tú, porque `go install` es su dueño.

```sh
bin="$(go env GOBIN)"; [ -n "$bin" ] || bin="$(go env GOPATH)/bin"
rm "$bin/pulso"
```

En Windows el binario está en la misma carpeta como `pulso.exe`. Si una ruta no pertenece a Pulso o Soloist, es tu carpeta personal, la raíz del sistema o el directorio donde estás trabajando, el comando se niega a borrar y termina sin tocar nada.

