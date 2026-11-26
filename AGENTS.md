# Instrucciones para agentes

## Idioma

- Escribe los mensajes de commit en español.
- Escribe los comentarios nuevos o modificados del código en español.
- No traduzcas identificadores, sentencias de Go, rutas ni textos de interfaz por esta regla.

## Implementación técnica

- Mantén todas las implementaciones técnicas en inglés: identificadores, nombres de archivos, APIs, pruebas y mensajes técnicos.
- Los mensajes de commit y los comentarios del código son las excepciones: deben estar en español.
- Conserva las traducciones de interfaz existentes: cada texto visible se escribe en inglés y en español a través de `preferences.text`.

## Organización de commits

- Separa los commits por funcionalidad.
- Incluye las pruebas y la documentación de cada funcionalidad en su mismo commit.
- Conserva los cambios ajenos y no los mezcles con el trabajo solicitado.

## Estructura del repositorio

- `cmd/pulso`: punto de entrada; solo llama a `app.Main`.
- `internal/app`: todo el código y las pruebas en un paquete.
- `docs`: plan, hoja de ruta y plan de interfaz.
- `odd`: documento de la funcionalidad y su registro.
- `README.md` e `INSTRUCTIONS.md` en español son los documentos principales; sus pares `.en.md` son traducciones.

## Reglas de trabajo

- No añadas dependencias sin necesidad comprobable.
- Mantén la reproducción funcionando aunque Spotify Web API no esté disponible.
- Nunca escribas tokens, claves ni datos de sesión en registros, pruebas ni documentación.
- Conserva el comportamiento de los comandos existentes (`pulso`, `doctor`, `--help`) salvo petición explícita.
