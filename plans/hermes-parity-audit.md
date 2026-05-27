# Hermes → Overkill Feature Parity Audit

## 🔴 CRITICAL (user-facing, immediately noticeable)

### Gateways (Overkill has 7, Hermes has 16)
| Platform | Hermes | Overkill | Action |
|---|---|---|---|
| Telegram | ✅ | ✅ | Polish done |
| Discord | ✅ | ✅ | Hardened — backoff, rate limits, health |
| WhatsApp | ✅ | ✅ Cloud + Meow | Hardened — backoff, rate limits, health |
| Slack | ✅ | ✅ | Socket Mode, integrated |
| **Signal** | ✅ | ✅ | signal-cli REST API, integrated |
| **Matrix** | ✅ | ✅ | Raw HTTP Client-Server API, integrated |
| Mattermost | ✅ | ❌ | Low priority |
| Email | ✅ | ❌ | Low priority |
| SMS | ✅ | ❌ | Low priority |
| DingTalk | ✅ | ❌ | Skip (China) |
| WeCom | ✅ | ❌ | Skip (China) |
| WeChat | ✅ | ❌ | Skip (China) |
| Feishu | ✅ | ❌ | Skip (China) |
| QQ | ✅ | ❌ | Skip (China) |
| BlueBubbles | ✅ | ❌ | Skip (Apple) |
| HomeAssistant | ✅ | ❌ | Nice to have |
| Yuanbao | ✅ | ❌ | Skip (China) |
| Webhook | ✅ | ❌ via bridge | Partial |
| API Server | ✅ | ✅ | Exists |

### TUI Features
| Feature | Hermes | Overkill | Action |
|---|---|---|---|
| Multiline input | ✅ | ✅ Section 1 done | ✅ |
| Streaming markdown | ✅ streamingMarkdown.tsx | ✅ SSE + progressive render | ✅ |
| Reasoning/thinking toggle | ✅ thinking.tsx | ✅ thinking-block.tsx | ✅ |
| Queued messages badge | ✅ queuedMessages.tsx | ✅ status bar | ✅ |
| Git branch display | ✅ useGitBranch.ts | ✅ status bar | ✅ |
| Context-fill phase display | ✅ | ✅ status bar [phase] | ✅ |
| Subagent status panel | ✅ agentsOverlay.tsx | ✅ sidebar agents tab | ✅ |
| Onboarding/setup wizard | ✅ | ✅ 6-step wizard (providers, models, TTS, vision, gateways) | ✅ |
| Todo panel | ✅ todoPanel.tsx | ❌ | Low priority |
| Skills hub | ✅ skillsHub.tsx | ❌ | Low priority |
| Session picker | ✅ sessionPicker.tsx | ✅ session-manager.tsx | ✅ |
| Model picker | ✅ modelPicker.tsx | ✅ model-switcher.tsx | ✅ |
| Input history | ✅ useInputHistory.ts | ❌ | Low priority |
| FPS overlay | ✅ fpsOverlay.tsx | ❌ | Low priority |
| Masked prompt | ✅ maskedPrompt.tsx | ❌ | Low priority |
| Branding/skinning | ✅ branding.tsx + themed.tsx | ✅ boot-animation + themes | ✅ |
| Command palette | ✅ slash/commands/ | ✅ command-palette.tsx | ✅ |
| Mouse support | ✅ textInput mouse handlers | ❌ | Low priority |
| Virtual scrolling | ✅ useVirtualHistory.ts | ❌ | Low priority |

## 🟡 HIGH (major UX gaps)

### Agent Features
| Feature | Hermes | Overkill | Action |
|---|---|---|---|
| Agent steering (mid-run redirect) | ✅ run_agent.py steering | ❌ plan mentions it | Build Pi-style dual-loop |
| Per-task adaptive timeout | ✅ | ✅ task_timeout.go | ✅ |
| Compaction (LLM-based) | ✅ agent/compact.py | ✅ compaction/ | ✅ |
| Model routing (complexity-based) | ✅ | ✅ routing/ | ✅ |
| Prompt caching awareness | ✅ cache tracking | ✅ | ✅ |
| Budget/enforcement | ✅ | ✅ cost/tracker.go | ✅ |
| Sub-agents (delegate_task) | ✅ | ✅ subagent/ | ✅ |

### Tools
| Tool | Hermes | Overkill | Action |
|---|---|---|---|
| Browser automation | ✅ browser_tool.py | ✅ browser/ | ✅ |
| Skill management | ✅ | ✅ skills/ | ✅ |
| Memory tools | ✅ memory_tool.py | ✅ memory/ | ✅ |
| Cron job tools | ✅ cronjob_tools.py | ✅ cron/ | ✅ |
| MCP integration | ✅ mcp_tool.py | ✅ mcp/ | ✅ |
| Code execution | ✅ code_execution_tool.py | ✅ shell tool | ✅ |
| Send message | ✅ sendMessage | ✅ tools/messaging/ (Telegram, Discord, Slack) | ✅ |
| TTS/speech | ✅ neutts_synth.py | ✅ tools/tts/ (edge, KittenTTS, OpenAI, ElevenLabs) | ✅ |
| Image generation | ✅ image_generation_tool.py | ❌ | Build |
| Discord bot actions | ✅ discord_tool | ❌ | Low priority |
| Feishu doc/drive | ✅ | ❌ | Skip |
| HomeAssistant | ✅ | ❌ | Skip |
| Vision (screenshots) | ✅ browser_cdp_tool.py | ✅ vision/ | ✅ |

## 🟢 MEDIUM (nice to have)

### Learning & Adaptation
| Feature | Hermes | Overkill | Action |
|---|---|---|---|
| User fingerprinting | ✅ fingerprint.py | ✅ personality/fingerprint.go | ✅ |
| Relationship tracking | ✅ trust scoring | ✅ personality/relationship.go | ✅ |
| Style adaptation | ✅ style_matching | ✅ personality/style.go | ✅ |
| Frustration detection | ✅ | ✅ personality/frustration.go | ✅ |
| Cold start / onboarding | ✅ | ✅ onboarding wizard | ✅ |
| Learning from corrections | ✅ | ✅ learning/ (BadgerDB, TF retrieval, agent injection) | ✅ |
| Skill auto-trigger learning | ✅ learn_trigger | ✅ skills/learn_trigger.go | ✅ |

### Plugins
| Plugin | Hermes | Overkill | Action |
|---|---|---|---|
| Memory providers | ✅ memory/ | ✅ memory/ | ✅ |
| Model providers | ✅ model-providers/ | ✅ providers/ | ✅ |
| Spotify | ✅ | ❌ | Low priority |
| Observability | ✅ observability/ | ❌ | Low priority |
| Image gen | ✅ image_gen/ | ❌ | Low priority |
| Dashboard | ✅ example-dashboard/ | ❌ | Low priority |

### Platforms (outbound messaging)
| Feature | Hermes | Overkill | Action |
|---|---|---|---|
| Send message tool | ✅ sendMessage | ✅ tools/messaging/ | ✅ |
| Discord bot actions | ✅ discord_tool | ❌ | Low priority |
| Voice notes (Telegram) | ✅ ogg voice | ❌ | Audio message support |

## ⚪ LOW (nice someday)

- ACP/IDE integration (VS Code, Zed, JetBrains)
- Webhook subscriptions
- Achievement system (hermes-achievements plugin)
- Strike Freedom Cockpit (gaming plugin)
- RL training environments (Atropos)
- Google Meet integration
- Offline batch runner
- Red team trigger system

---

## Priority Execution Order

1. ~~**Slack gateway**~~ ✅ Done
2. ~~**TUI: streaming markdown + thinking toggle**~~ ✅ Done
3. ~~**TUI: queued messages badge + git branch**~~ ✅ Done
4. ~~**Discord gateway hardening**~~ ✅ Done
5. ~~**WhatsApp gateway hardening**~~ ✅ Done
6. ~~**TUI: subagent status panel**~~ ✅ Done
7. ~~**Onboarding wizard**~~ ✅ Done
8. ~~**TTS tools**~~ ✅ Done
9. ~~**Learning from corrections**~~ ✅ Done
10. ~~**Send message tool**~~ ✅ Done
11. ~~**Signal + Matrix gateways**~~ ✅ Done
12. **Image gen tool** (multimodal) — last remaining
