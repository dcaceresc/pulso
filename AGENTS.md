# Instrucciones para agentes

## Idioma

- Escribe los mensajes de commit en español.
- Redacta siempre el título y la descripción de los pull requests en español.
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

## Convenciones de commits

Todos los commits deben seguir la especificación [Conventional Commits](https://www.conventionalcommits.org/).

### Reglas obligatorias

- Escribir el título y la descripción del commit en español.
- Mantener los tipos de Conventional Commits en inglés.
- Utilizar verbos en infinitivo: agregar, corregir, actualizar, eliminar, refactorizar, etc.
- Mantener el título conciso, descriptivo y sin punto final.
- Incluir obligatoriamente un cuerpo descriptivo separado del título por una línea en blanco.
- Explicar qué se modificó y por qué se realizó el cambio.
- Evitar descripciones genéricas o ambiguas.
- Mantener cada commit enfocado en un único propósito.
- Utilizar un alcance (`scope`) cuando corresponda.

### Formato

```text
<tipo>(<alcance>): <título en español>

<descripción detallada en español>
```

### Tipos permitidos

- `feat`: nueva funcionalidad.
- `fix`: corrección de errores.
- `refactor`: reestructuración de código sin modificar su comportamiento.
- `docs`: cambios en la documentación.
- `style`: cambios de formato sin afectar la lógica.
- `test`: incorporación o modificación de pruebas.
- `perf`: mejoras de rendimiento.
- `build`: cambios en dependencias o compilación.
- `ci`: cambios en integración y despliegue continuo.
- `chore`: tareas de mantenimiento.
- `revert`: reversión de cambios anteriores.

### Ejemplos

```text
feat(auth): agregar autenticación mediante Keycloak

Implementar la validación de tokens y la verificación de roles
y permisos para restringir el acceso a los endpoints protegidos.
```

```text
fix(api): corregir la validación de solicitudes

Ajustar la validación de los campos obligatorios para evitar
errores al procesar solicitudes incompletas.
```

```text
refactor(database): simplificar las consultas de usuarios

Eliminar consultas redundantes y centralizar la lógica de acceso
a datos para mejorar la mantenibilidad.
```

### Cambios incompatibles

Cuando un cambio rompa la compatibilidad existente, utilizar `!` en el encabezado e incluir el pie `BREAKING CHANGE:` con una explicación en español.

```text
feat(api)!: modificar el contrato de usuarios

Actualizar la estructura de respuesta para unificar el formato
de los endpoints de usuarios.

BREAKING CHANGE: se modifica la estructura de respuesta del endpoint.
```

No generar commits que incumplan estas convenciones.

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
