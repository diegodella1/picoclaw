<p align="center">
  <img src="https://em-content.zobj.net/source/apple/391/lobster_1f99e.png" width="120" alt="Chango">
</p>

<h1 align="center">Chango Assistant</h1>

<p align="center">
  <strong>AI-powered personal assistant running on a Raspberry Pi 5</strong>
</p>

<p align="center">
  <em>Fork of <a href="https://github.com/picoclaw/picoclaw">picoclaw</a> — optimized for product development workflows and personal automation</em>
</p>

<p align="center">
  <img src="https://img.shields.io/badge/Go-1.22+-00ADD8?style=flat-square&logo=go&logoColor=white" alt="Go">
  <img src="https://img.shields.io/badge/Platform-Raspberry%20Pi%205-C51A4A?style=flat-square&logo=raspberrypi&logoColor=white" alt="Raspberry Pi">
  <img src="https://img.shields.io/badge/Channel-Telegram-26A5E4?style=flat-square&logo=telegram&logoColor=white" alt="Telegram">
  <img src="https://img.shields.io/badge/LLM-OpenRouter-8B5CF6?style=flat-square" alt="OpenRouter">
  <img src="https://img.shields.io/badge/Deploy-Coolify-6C47FF?style=flat-square" alt="Coolify">
</p>

---

## What is Chango?

Chango is an autonomous execution copilot that lives on a Raspberry Pi 5 and communicates via Telegram. It can search the web, manage your Google Calendar, generate images, transcribe voice messages, run shell commands on the host, and much more — all through natural conversation.

Built in Go, containerized with Docker, and deployed via Coolify. Zero cloud dependencies beyond the LLM API.

---

## Features

### Conversation & Intelligence
- **Multi-model support** — Switch between 9+ LLMs on-the-fly via `/model` command (GPT-4o, Claude, Gemini, DeepSeek, etc.)
- **Persistent memory** — Key-value store for long-term context across sessions
- **Session summarization** — Automatic context compression to stay within token limits
- **Subagent system** — Spawn parallel agents for complex multi-step tasks

### Web & Search
- **Google Search (Serper)** — Priority search provider with Google-quality results
- **Brave Search** — API-based fallback
- **DuckDuckGo** — HTML scraping as last resort
- **Web Fetch** — Extract readable content from any URL

### Google Workspace
- **Calendar** — Read events, create appointments, manage multiple calendars via service account
- **Gmail** — Read, search, send, and reply to emails
- **Drive** — List, search, and read documents

### Voice & Media
- **Voice transcription** — Groq-powered speech-to-text for incoming voice messages
- **Text-to-speech** — Edge TTS with Argentine Spanish voice (es-AR-TomasNeural)
- **Image generation** — Pollinations.ai with HTTP validation and automatic retries
- **YouTube** — Extract transcripts from YouTube videos

### Automation & Productivity
- **Reminders** — Schedule notifications delivered via Telegram
- **Tasks** — Persistent goal and task tracking
- **Snippets** — Save and retrieve code snippets
- **Cron jobs** — Scheduled background tasks (JSON-configured)
- **Heartbeat** — Periodic check-ins with proactive notifications (every 45min)
- **Telemetry** — Token usage tracking per feature (chat, heartbeat, cron, summarize) with daily breakdown, 30-day retention

### Monitoring & Hardware
- **Sentinel** — Go-pure system health monitor (CPU temp, RAM, disk) every 2min with critical alerts direct to Telegram
- **Shell execution** — Run commands inside the container
- **Host access** — Execute commands on the Raspberry Pi host via `nsenter`
- **I2C / SPI** — Direct hardware bus interaction for IoT peripherals
- **HTTP requests** — Arbitrary API calls to external services

### Council of Experts
- **LinkedIn PR Committee** — Three specialized AI personas (Writer, Strategist, Editor) collaborate in a Telegram group to craft professional content

---

## Architecture

```
                    ┌─────────────────────────────────────────┐
                    │            Raspberry Pi 5               │
                    │                                         │
  Telegram ────────►│  ┌──────────┐    ┌──────────────────┐  │
  (polling)         │  │ Telegram  │───►│    MessageBus    │  │
                    │  │ Channel   │◄───│                  │  │
                    │  └──────────┘    └────────┬─────────┘  │
                    │                           │             │
                    │                  ┌────────▼─────────┐  │
                    │                  │    AgentLoop      │  │
                    │                  │  ┌─────────────┐  │  │
                    │                  │  │ContextBuilder│  │  │
                    │                  │  │  + Skills    │  │  │
                    │                  │  │  + Memory    │  │  │
                    │                  │  └─────────────┘  │  │
                    │                  └────────┬─────────┘  │
                    │                           │             │
                    │              ┌────────────▼──────────┐  │
                    │              │     ToolRegistry      │  │
                    │              │  27 tools available   │  │
                    │              └───────────────────────┘  │
                    │                                         │
                    │  LLM: OpenRouter ──► GPT-4o / Claude /  │
                    │                      Gemini / DeepSeek   │
                    └─────────────────────────────────────────┘
```

### Key Components

| Component | Path | Purpose |
|-----------|------|---------|
| Agent Loop | `pkg/agent/loop.go` | Core message processing, LLM iteration, tool execution |
| Context Builder | `pkg/agent/context.go` | System prompt assembly (identity + skills + memory) |
| Telegram Channel | `pkg/channels/telegram.go` | Polling, TTS, voice transcription, inline keyboards |
| Tool Registry | `pkg/tools/` | 27 tools — web, calendar, exec, memory, media, telemetry, etc. |
| Config | `pkg/config/config.go` | JSON config with env var overrides |
| Session Manager | `pkg/session/` | Conversation history, summarization, persistence |
| Sentinel | `pkg/sentinel/service.go` | System health monitor (CPU temp, RAM, disk) with alerts |
| Telemetry | `pkg/telemetry/tracker.go` | Token usage tracking per feature per day |

### Search Provider Priority

```
Serper (Google) ──► Brave Search ──► DuckDuckGo (HTML scraping)
   (preferred)       (fallback)        (last resort)
```

---

## Quick Start

### 1. Clone and configure

```bash
git clone https://github.com/diegodella1/Chango-Assistant.git
cd Chango-Assistant
cp config/config.example.json ~/.picoclaw/config.json
```

### 2. Fill in your API keys

Edit `~/.picoclaw/config.json`:

```jsonc
{
  "channels": {
    "telegram": {
      "enabled": true,
      "token": "YOUR_TELEGRAM_BOT_TOKEN",
      "allow_from": ["YOUR_TELEGRAM_USER_ID"]
    }
  },
  "providers": {
    "openrouter": {
      "api_key": "sk-or-v1-..."
    }
  },
  "tools": {
    "web": {
      "serper": {
        "enabled": true,
        "api_key": "YOUR_SERPER_API_KEY"
      }
    }
  }
}
```

### 3. Build and run

```bash
# Local
go build -o picoclaw . && ./picoclaw gateway

# Docker
docker build -t chango . && docker run -v ~/.picoclaw:/root/.picoclaw chango
```

---

## Personality & Customization

Chango's behavior is defined by markdown files in the workspace:

| File | Purpose |
|------|---------|
| `IDENTITY.md` | Core personality and communication style |
| `SOUL.md` | Deep behavioral rules and decision-making framework |
| `USER.md` | User profile, preferences, and account information |
| `AGENTS.md` | Agent capabilities and autonomous behavior rules |

These files are loaded into the system prompt at startup. Edit them to customize Chango's personality.

---

## Deployment

Chango runs as a Docker container deployed via [Coolify](https://coolify.io/) on a Raspberry Pi 5:

1. Push to GitHub
2. Trigger Coolify restart via API
3. Multi-stage Dockerfile: Go build + Debian bookworm runtime (python3 + edge-tts + ffmpeg)
4. Volume mount: `~/.picoclaw` for config, workspace, and persistent data

---

## Credits

- **Original project:** [picoclaw](https://github.com/picoclaw/picoclaw) by the picoclaw contributors
- **This fork:** Optimized for product development and personal automation

---

<p align="center">
  <em>Built on a Raspberry Pi 5 with Go, deployed with Coolify, powered by OpenRouter</em>
</p>
