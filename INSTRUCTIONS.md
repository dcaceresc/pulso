# Instrucciones de Pulso

Español (predeterminado) · [English](INSTRUCTIONS.en.md)

Esta guía explica cómo instalar, configurar y usar Pulso. La aplicación está pensada para mi flujo personal: escuchar Spotify desde la terminal en Windows y Linux usando el reproductor oficial de Spotify, sin depender de `librespot`.

## Contenido

- [Instalación recomendada](#instalación-recomendada)
- [Primer arranque](#primer-arranque)
- [Emparejar Soloist](#emparejar-soloist)
- [Conectar Spotify para buscar y ver listas](#conectar-spotify-para-buscar-y-ver-listas)
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

En el paso de emparejamiento el asistente diagnostica por qué el teléfono puede no ver el equipo y resume el resultado con un indicador al principio de la línea: `● OK` en verde cuando no hay nada que revisar, `● Revisar` en ámbar cuando hay un riesgo pero el emparejamiento puede funcionar (por ejemplo, un puente de contenedores compitiendo por el anuncio), y `● Bloqueado` en rojo cuando falta una regla y el teléfono no podrá conectar. La palabra acompaña siempre al color, así que se entiende también en una terminal sin color.

El atajo `d: diagnóstico` aparece solo cuando hay algo que revisar: abre el detalle con los hallazgos y, si hay algo que aplicar, los comandos exactos. `Enter` vuelve al paso de emparejamiento. Si el detalle no puede proponer ningún comando, lo dice y no ofrece `a`.

El diagnóstico revisa tres cosas:

- La dirección que usa tu equipo para llegar a la red local.
- Las interfaces que compiten por la única dirección que Soloist anuncia: puentes de contenedores (los de Docker, por ejemplo), VPN y túneles.
- El estado del cortafuegos y las reglas que faltan.

Un puente o una VPN rompen el emparejamiento porque Soloist anuncia una sola dirección al arrancar y no ofrece ninguna opción para fijar la interfaz: puede anunciar la dirección del puente en lugar de la de tu red local, y el teléfono busca en la red equivocada. La recuperación concreta es arrancar Pulso con ese puente ausente, emparejar y volver a levantar el puente después. El anuncio sigue siendo válido hasta que Soloist se reinicie.

El puerto de control de Soloist se elige al arrancar y cambia, así que una regla para un puerto concreto queda obsoleta en el siguiente arranque. Lo que se recomienda es un permiso acotado a la red local (por ejemplo `sudo ufw allow from 192.168.1.0/24`), que sigue sirviendo después de que cambie el puerto; es un permiso deliberadamente más amplio que un solo puerto. En Windows el asistente añade el puerto que leyó en ese momento, así que la regla manual de la red local de más abajo es la que evita repetir el trámite.

El asistente nunca cambia el cortafuegos por sí solo: solo añade reglas después de una confirmación en dos pasos, con `a: aplicar reglas` y una segunda `a`. Cualquier otra tecla cancela la aplicación, y `Esc` sigue cerrando Pulso. Tampoco elimina ni reemplaza reglas existentes, y no necesita permisos de administrador solo para inspeccionar: si no puede leer las reglas, lo dice en pantalla en lugar de suponer que están bien.

### Reglas manuales de cortafuegos

Coinciden con lo que propone el asistente, con una excepción: en Windows el asistente añade el puerto que leyó en ese arranque, y aquí se propone un permiso por red de origen para no repetir el trámite. Sustituye `192.168.1.0/24` por el prefijo de tu red local. Donde el asistente todavía no puede proponer un comando (firewalld y nftables), estos son los equivalentes manuales.

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

**Windows Defender Firewall,** en una consola de Windows con permisos de administrador:

```
netsh advfirewall firewall add rule name="Pulso (Soloist)" dir=in action=allow protocol=UDP localport=5353
netsh advfirewall firewall add rule name="Pulso (Soloist) LAN" dir=in action=allow protocol=TCP remoteip=192.168.1.0/24
```

La primera regla abre el descubrimiento y coincide con la que añade el asistente. La segunda permite el tráfico TCP de tu red local, que es lo que cambia de puerto en cada arranque.

## Conectar Spotify para buscar y ver listas

Buscar y listar playlists usa la **Spotify Web API**, una autorización distinta del emparejamiento de Soloist:

1. Crea una app gratuita en el [panel de Spotify para desarrolladores](https://developer.spotify.com/dashboard) y copia su **Client ID**. Tiene 32 caracteres hexadecimales; no es la clave de Soloist.
2. En la configuración de esa app, añade exactamente `http://127.0.0.1:8888/callback` como URI de redirección. Spotify exige el puerto explícito en las direcciones locales.
3. El asistente te pide el Client ID una vez y lo guarda. También puedes definirlo antes de arrancar con `PULSO_SPOTIFY_CLIENT_ID`.
4. Aprueba el acceso en el navegador que Pulso abre. El puerto 8888 debe estar libre; si está ocupado, ciérralo y reintenta.

Después de la primera aprobación, la sesión queda guardada y se renueva sola. Solo tendrás que aprobar de nuevo si revocas la app en Spotify o borras el archivo de sesión. La reproducción nunca depende de esta autorización: si falla o la omites con `s`, los controles y el audio siguen funcionando.

## Uso diario

La pantalla es un único espacio de trabajo:

- **Barra superior:** búsqueda y tu usuario de Spotify.
- **Biblioteca:** tus listas de reproducción.
- **Tabla:** canciones de la lista elegida o resultados de búsqueda, con número, título, artista y duración.
- **Barra de reproducción:** pista, artista, álbum, progreso, aleatorio, repetición, volumen y dispositivo.
- **Pie:** pares `tecla acción` del panel activo; `F1` abre la ayuda.

`Tab` recorre biblioteca, tabla, búsqueda y barra de reproducción. Con la búsqueda enfocada puedes escribir `q`, `s`, `r`, `+` y `-` sin que se disparen atajos; `Enter` busca y `Esc` sale del campo.

Al cerrar Pulso, la reproducción se pausa. Soloist sigue en segundo plano con su emparejamiento para que el siguiente arranque sea inmediato; si quieres detener el proceso, termínalo desde el sistema.

## Atajos

| Tecla | Acción |
| --- | --- |
| `Tab` / `Shift+Tab` | Cambiar de panel |
| `↑` `↓` / `j` `k` | Mover la selección |
| `Enter` | Abrir una lista, buscar o reproducir la selección |
| `Espacio` | Reproducir o pausar |
| `n` / `p` | Siguiente o anterior |
| `←` `→` | Retroceder o avanzar 10 segundos (barra de reproducción) |
| `s` | Aleatorio |
| `r` | Repetición: desactivada, lista, canción |
| `+` / `-` | Volumen |
| `/` | Buscar |
| `l` | Ir a la biblioteca |
| `F1` / `?` | Ayuda con todos los controles |
| `F2` | Inglés o español |
| `F3` | Tema estándar, oscuro o claro |
| `q` / `Esc` | Salir |

`F1`, `?`, `Esc` o `q` cierran la ayuda y vuelven al panel donde estabas.

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
| El dispositivo no aparece en Spotify | Misma red local, sin VPN ni aislamiento de invitados; en Windows, permite Soloist en el firewall |
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

- No implementa listas editables, podcasts, letras, visualizador ni historial.
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
