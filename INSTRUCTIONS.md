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

## Desinstalar

`pulso uninstall` borra lo que Pulso y Soloist guardan en tu usuario. Primero imprime la ruta absoluta de cada objetivo y después espera una confirmación escrita: hay que escribir exactamente `uninstall`. Un Enter vacío o cualquier otra palabra responde «Cancelled; nothing was deleted.» y sale con código 1 sin borrar nada.

Si Soloist está corriendo, el comando no borra nada por su cuenta: lo informa y pregunta `Close it and continue? [y/N]`. Solo con `y` o `Y` pide al demonio que termine y espera a que salga; si sigue vivo al agotarse la espera, aborta sin borrar nada. Nunca envía `SIGKILL`; en Windows usa la terminación del sistema.

Se borra el binario de Soloist que descargó Pulso (`bin/soloist` o `bin/soloist.exe`), el Client ID, la sesión de Spotify, las preferencias y los directorios de datos y de caché de Soloist, donde viven el emparejamiento y su caché.

Cada objetivo se informa como `Removed`, `Absent` o `Failed` con el error. Un objetivo que ya no existe no es un problema: ejecutar el comando dos veces termina bien. Si un borrado falla, se informa y se sigue con los demás, pero el código de salida es 1; solo es 0 cuando todo se borró o ya no estaba.

El emparejamiento de Spotify y la autorización de la Web API no se pueden recuperar: después de desinstalar hay que repetir los dos pasos, con la clave de Soloist y con el Client ID. La desinstalación tampoco borra el binario de Pulso: indica su ruta para que lo borres tú, porque `go install` es su dueño.

```sh
bin="$(go env GOBIN)"; [ -n "$bin" ] || bin="$(go env GOPATH)/bin"
rm "$bin/pulso"
```

En Windows el binario está en la misma carpeta como `pulso.exe`. Si una ruta no pertenece a Pulso o Soloist, es tu carpeta personal, la raíz del sistema o el directorio donde estás trabajando, el comando se niega a borrar y termina sin tocar nada.

