# Hermes → Overkill Feature Parity Audit

## 🔴 CRITICAL (user-facing, immediately noticeable)

### Gateways (Overkill has 4, Hermes has 16)
| Platform | Hermes | Overkill | Action |
|---|---|---|---|
| Telegram | ✅ | ✅ | Polish done |
| Discord | ✅ | ✅ | Needs hardening |
| WhatsApp | ✅ | ✅ Cloud + Meow | Needs hardening |
| **Slack** | ✅ | ✅ | Socket Mode, integrated |
| Signal | ✅ | ❌ | Build |
| Matrix | ✅ | ❌ | Build |
| Mattermost | ✅ | ❌ | Build |
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
| Agents/subagent overlay | ✅ agentsOverlay.tsx | ❌ | Build subagent status panel |
| Todo panel | ✅ todoPanel.tsx | ❌ | Build task tracking sidebar |
| Skills hub | ✅ skillsHub.tsx | ❌ | Browse/load skills in TUI |
| Session picker | ✅ sessionPicker.tsx | ✅ session-manager.tsx | ✅ |
| Model picker | ✅ modelPicker.tsx | ✅ model-switcher.tsx | ✅ |
| Input history | ✅ useInputHistory.ts | ❌ | Add to multiline-input |
| FPS overlay | ✅ fpsOverlay.tsx | ❌ | Low priority |
| Masked prompt | ✅ maskedPrompt.tsx | ❌ | For sensitive inputs |
| Branding/skinning | ✅ branding.tsx + themed.tsx | ✅ boot-animation + themes | ✅ |
| Command palette | ✅ slash/commands/ | ✅ command-palette.tsx | ✅ |
| Mouse support | ✅ textInput mouse handlers | ❌ | Add to multiline-input |
| Virtual scrolling | ✅ useVirtualHistory.ts | ❌ | For long transcripts |

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
| Image generation | ✅ image_generation_tool.py | ❌ | Build |
| TTS/speech | ✅ neutts_synth.py | ❌ | Build |
| Discord messaging tool | ✅ discord_tool.py | ❌ | Build send_message |
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
| Cold start / onboarding | ✅ | ✅ personality/coldstart.go | ✅ |
| Learning from corrections | ✅ | ❌ | Build feedback loop |
| Skill auto-trigger learning | ✅ learn_trigger | ✅ skills/learn_trigger.go | ✅ |

### Plugins
| Plugin | Hermes | Overkill | Action |
|---|---|---|---|
| Memory providers | ✅ memory/ | ✅ memory/ | ✅ |
| Model providers | ✅ model-providers/ | ✅ providers/ | ✅ |
| Spotify | ✅ | ❌ | Low priority |
| Observability | ✅ observability/ | ❌ | Build metrics export |
| Image gen | ✅ image_gen/ | ❌ | Low priority |
| Dashboard | ✅ example-dashboard/ | ❌ | Web dashboard |

### Platforms (outbound messaging)
| Feature | Hermes | Overkill | Action |
|---|---|---|---|
| Send message tool | ✅ sendMessage | ❌ | Build cross-platform send |
| Discord bot actions | ✅ discord_tool | ❌ | Reactions, embeds, threads |
| Voice notes (Telegram) | ✅ ogg voice | ❌ | Audio message support |

## ⚪ LOW (nice someday)

- ACP/IDE integration (VS Code, Zed, JetBrains) — Hermes has acp_adapter/
- Webhook subscriptions
- Achievement system (hermes-achievements plugin)
- Strike Freedom Cockpit (gaming plugin)
- RL training environments (Atropos)
- Google Meet integration
- Offline batch runner
- Red team trigger system

---

## Priority Execution Order

1. ~~**Slack gateway**~~ ✅ Done — Socket Mode, integrated
2. ~~**TUI: streaming markdown + thinking toggle**~~ ✅ Done — SSE + progressive render + thinking-block
3. ~~**TUI: queued messages badge + git branch**~~ ✅ Done — status bar
4. **Discord gateway hardening** (exists but fragile)
5. **WhatsApp gateway hardening** (exists but fragile)
6. **TUI: subagent status panel** (Overkill's killer feature)
7. **Learning from corrections** (feedback loop)
8. **Send message tool** (cross-platform)
9. **Signal + Matrix gateways** (encrypted messaging)
10. **Image gen + TTS tools** (multimodal)
