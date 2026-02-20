# picoclaw-comite (🦞 + 🏛️)

Este repositorio es un fork de **picoclaw**, el asistente autónomo de ejecución.

## Créditos y Referencias
- **Proyecto Original:** [picoclaw](https://github.com/picoclaw/picoclaw)
- **Autores Originales:** Agradecimiento a los creadores de la arquitectura base de picoclaw por la infraestructura de agentes y herramientas.
- **Este Fork:** Optimizado para el ecosistema de **desarrollo de productos** y la asistencia personal de **quien lo use**.

---

## 🚀 Superpoderes de picoclaw-comite

Copiloto de ejecución autónoma diseñado para reducir el tiempo entre la idea y el resultado embarcado (*shipped*).

### 1. Gestión de Contexto y Memoria
- **Memoria de Largo Plazo:** Registro decisiones estratégicas, planes de producto y hitos operativos en `/memory` (local).
- **Protocolo de Privacidad:** Memoria e interacciones privadas son estrictamente locales. Solo se sube código e infraestructura.

### 2. Integración de Infraestructura
Herramientas de sistema integradas:
- **Google Workspace:** Gmail, Drive y Calendar vía service account.
- **GitHub:** Operación completa de repositorios (`gh cli`).
- **Coolify & Supabase:** Despliegue de aplicaciones y gestión de datos.
- **Telegram:** Interfaz de control, envío de archivos y notificaciones.

### 3. Búsqueda Web
- **Serper (Google Search):** Resultados de Google vía API, provider prioritario.
- **Brave Search:** API de búsqueda como fallback.
- **DuckDuckGo:** Scraping HTML como último recurso.

### 4. Consejo de Expertos (`/consejo`)
- **Comité de PR de LinkedIn** (El Redactor, El Estratega, El Editor) para transformar ideas técnicas en contenido profesional de alto impacto.

### 5. Producción de Entregables
- PRDs, SOPs, Runbooks de Live Ops y reportes de investigación en formatos listos para usar (.md).

### 6. Generación de Imágenes
- Generación de imágenes vía Pollinations.ai con validación HTTP y reintentos.

### 7. Voice & TTS
- **Transcripción:** Groq speech-to-text para mensajes de voz entrantes.
- **TTS:** Edge TTS (es-AR-TomasNeural) para respuestas de voz.

### 8. Hardware & IoT
- Interacción con buses **I2C y SPI** para control de periféricos.
- **Host exec:** Acceso al host Raspberry Pi vía nsenter desde el container.

---

## 🛠 Herramientas disponibles

| Tool | Descripción |
|------|-------------|
| `web_search` | Búsqueda web (Serper/Brave/DuckDuckGo) |
| `web_fetch` | Fetch de URLs con extracción de texto |
| `calendar` | Google Calendar (list, create, update, delete) |
| `gmail` | Gmail (read, search, send, reply) |
| `gdrive` | Google Drive (list, search, read) |
| `image_gen` | Generación de imágenes (Pollinations.ai) |
| `memory` | Notas persistentes key-value |
| `reminder` | Recordatorios programados |
| `tasks` | Tracking de tareas y objetivos |
| `snippet` | Code snippets guardados |
| `translate` | Traducción de texto |
| `weather` | Clima actual |
| `youtube` | Transcripciones de YouTube |
| `exec` | Ejecución de comandos en el container |
| `host_exec` | Ejecución en el host via nsenter |
| `read_file` / `write_file` / `edit_file` / `append_file` / `list_dir` | Operaciones de filesystem |
| `message` | Envío de mensajes al usuario |
| `spawn` / `subagent` | Subagentes para tareas paralelas |
| `http_request` | Requests HTTP arbitrarios |
| `i2c` / `spi` | Hardware buses |

---

## 🏗️ Arquitectura

```
Telegram (polling) ──► MessageBus ──► AgentLoop ──► LLM (OpenRouter)
                                         │
                                    ToolRegistry
                                    ├── web_search (Serper > Brave > DDG)
                                    ├── calendar / gmail / gdrive
                                    ├── memory / tasks / reminder
                                    ├── exec / host_exec
                                    ├── image_gen / youtube / weather
                                    └── spawn / subagent
```

- **Container:** Dockerfile multi-stage (Go build + Debian bookworm + python3/edge-tts/ffmpeg)
- **Config:** `~/.picoclaw/config.json` (gitignored, con API keys)
- **Workspace:** `~/.picoclaw/workspace/` (memoria, sesiones, skills)
- **Deploy:** Push a GitHub → trigger Coolify restart via API

---

## 🔧 Setup

1. Copiar `config/config.example.json` a `~/.picoclaw/config.json`
2. Completar API keys (OpenRouter, Telegram, Groq, Serper, etc.)
3. Build y deploy via Coolify o `go build ./...` local

---

## 🛠 Registro de Cambios Recientes

### 2026-02-20
- **Serper Search:** Integración de Serper.dev como provider prioritario de búsqueda web (Google results via API).
- **Google Calendar:** Soporte multi-calendario (personal + trabajo) con service account.

### 2026-02-19
- **Protocolo de Autolearning:** Auto-resumen y anclaje de contexto.
- **Soporte de Modelos:** Teclado inline para cambio dinámico de LLMs con persistencia local.

### 2026-02-18
- **Voice Responses:** Optimización de respuestas de voz para Telegram.
- **Integración de Infraestructura:** Scripts de despliegue automático.

---
*Built with picoclaw*
