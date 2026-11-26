# Pulso

Español (predeterminado) · [English](README.en.md)

Pulso es un reproductor de Spotify para la terminal escrito en Go sobre **Spotify Soloist**, el reproductor oficial de Spotify, y la **Spotify Web API** para buscar y navegar tus listas. Lo creé para mi uso personal porque `ncspot` y `spotify_player` dependen de `librespot` y dejaron de reproducir audio en mi cuenta por los cambios de Spotify en las claves de audio.

No pretende ser el mejor cliente de Spotify ni competir con otros proyectos. Su alcance prioriza lo que uso: abrir la terminal, buscar una canción, elegir una lista y escuchar sin mantener abierta la aplicación oficial. Mientras me funcione a mí, me sirve.

## Características principales

- Reproducción con el motor oficial de Spotify (Soloist), sin `librespot`.
- Emparejamiento guiado una sola vez y detección automática cuando Spotify conecta el dispositivo.
- Reproducción reanudada al abrir: pausa, siguiente, anterior, volumen, avance, aleatorio y repetición.
- Biblioteca con tus listas de reproducción y tabla de canciones con artista y duración.
- Búsqueda de canciones desde la barra superior.
- Interfaz en terminal con paneles, atajos por contexto y pantalla de ayuda.
- Inglés y español, y temas estándar, oscuro y claro.
- Instalación y actualización de Soloist desde la propia aplicación, sin permisos de administrador.
- Sesión de Spotify guardada de forma privada: no repites el Client ID ni la aprobación del navegador en cada arranque.

## Plataformas

Windows x86-64 y Linux x86-64 son las plataformas previstas. macOS compila, pero no lo he probado. Spotify Soloist se distribuye para Linux (x86-64, arm64 y arm32), macOS con Apple Silicon y Windows x86-64: Pulso no puede reproducir audio en una plataforma para la que Spotify no publique Soloist.

## Instalación y uso

Requiere **Go 1.22 o posterior** para compilar y una cuenta de **Spotify Premium**.

```sh
go install ./cmd/pulso
pulso
```

La primera vez, el asistente comprueba si Soloist está instalado, ofrece descargarlo desde Spotify, te guía para crear la clave de Soloist y empareja el dispositivo desde la aplicación de Spotify del teléfono. Después pide, una única vez, el Client ID de tu propia app de Spotify para buscar y listar playlists.

Sigue las [instrucciones completas](INSTRUCTIONS.md) para el emparejamiento, la app de Spotify, los atajos, los temas y la solución de problemas.

## Estado

Funciona en mi equipo con Windows: reproducción, biblioteca, búsqueda, idiomas, temas y sesión guardada. Lo que aún no está verificado en vivo: la ejecución nativa en Linux, la aceptación real del refresco de sesión por parte de Spotify y el aspecto visual en otras terminales.

Los límites de Spotify también aplican: en modo desarrollo solo se ven las canciones de playlists propias o colaborativas, y la búsqueda devuelve diez resultados por página.
