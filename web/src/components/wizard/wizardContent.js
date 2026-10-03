// Static copy and choices for the setup wizard. Kept out of the components so
// they stay about layout and flow.

export const PROVIDERS = [
  { type: 'anthropic', label: 'Anthropic', caption: 'Claude models', keyPlaceholder: 'sk-ant-…', keyURL: 'https://console.anthropic.com/settings/keys', needsKey: true },
  { type: 'openai', label: 'OpenAI', caption: 'GPT models', keyPlaceholder: 'sk-…', keyURL: 'https://platform.openai.com/api-keys', needsKey: true },
  { type: 'openrouter', label: 'OpenRouter', caption: 'Many models', keyPlaceholder: 'sk-or-…', keyURL: 'https://openrouter.ai/keys', needsKey: true },
  { type: 'ollama', label: 'Ollama', caption: 'Local, no key', keyPlaceholder: '', keyURL: 'https://ollama.com/download', needsKey: false, defaultBaseURL: 'http://localhost:11434' },
]

export function providerMeta(type) {
  return PROVIDERS.find(p => p.type === type) || PROVIDERS[0]
}

// Substrings tried in order to preselect a model from the provider's live list.
const MODEL_PREFS = {
  anthropic: { main: ['claude-sonnet', 'claude-opus'], supervisor: ['claude-haiku'] },
  openai: { main: ['gpt-5', 'gpt-4.1', 'gpt-4o'], supervisor: ['mini'] },
  openrouter: { main: ['anthropic/claude-sonnet', 'openai/gpt-5'], supervisor: ['anthropic/claude-haiku', 'mini'] },
  ollama: { main: [], supervisor: [] },
}

// pickModel returns the preferred model from models for the role ('main' or
// 'supervisor'), falling back to the first one. Newest-looking IDs sort
// last alphabetically within a family, so the last match wins.
export function pickModel(type, models, role = 'main') {
  if (!models?.length) return ''
  for (const pref of MODEL_PREFS[type]?.[role] || []) {
    const hits = models.filter(m => m.includes(pref))
    if (hits.length) return [...hits].sort().at(-1)
  }
  return models[0]
}

export const TONES = [
  { id: 'generalist', label: 'Helpful generalist', caption: 'Balanced, has opinions', theme: 'helpful general-purpose assistant' },
  { id: 'concise', label: 'Concise', caption: 'Short and direct', theme: 'concise, direct assistant that skips small talk' },
  { id: 'warm', label: 'Warm', caption: 'Friendly and chatty', theme: 'warm, friendly assistant that checks in' },
]

// toneForTheme maps a saved theme back to a preset, or 'custom'.
export function toneForTheme(theme) {
  return TONES.find(t => t.theme === theme)?.id || (theme ? 'custom' : 'generalist')
}

// A fixed sample per tone for the preview card. Not generated: the preview
// must not spend tokens or pretend to be the model.
export function sampleGreeting(tone, name) {
  const who = name || 'your assistant'
  switch (tone) {
    case 'concise': return `${who} here. What do you need?`
    case 'warm': return `Hi! I'm ${who}. How's your day going? Tell me what you're working on.`
    case 'custom': return `Hi, I'm ${who}.`
    default: return `Hi, I'm ${who}. Tell me what you're working on and I'll get stuck in.`
  }
}

export const DEFAULT_HOUSE_RULES = `Be genuinely helpful, not performatively helpful. Skip filler — just help.

Have opinions. You're allowed to disagree, prefer things, find stuff amusing or boring.

Be resourceful before asking. Try to figure it out first, then ask if stuck.

Earn trust through competence. Be careful with external actions. Be bold with internal ones.

Remember you're a guest. You have access to someone's life — treat it with respect.`

// houseRules splits the soul text into its paragraphs.
export function houseRules(text) {
  return text.split(/\n\s*\n/).map(s => s.trim()).filter(Boolean)
}

// Emoji the user can pick for the agent's identity (user data, not UI chrome).
export const EMOJI_SUGGESTIONS = ['🦊', '🦉', '🐙', '🌿', '🛰️', '🐉']

export const EXAMPLE_PROMPTS = [
  'What can you do for me?',
  'Every Monday at 9, remind me to plan my week.',
  'Read this page and give me the short version.',
]

export const TIERS = [
  { id: 'restricted', label: 'Restricted', caption: 'Chat and read-only tools' },
  { id: 'supervised', label: 'Supervised', caption: 'Risky tools get checked' },
  { id: 'autonomous', label: 'Autonomous', caption: 'Runs any tool' },
]

export const CHAT_APPS = [
  { id: 'telegram', label: 'Telegram', source: '@BotFather', sourceURL: 'https://t.me/BotFather', tokenPlaceholder: '123456789:AA…' },
  { id: 'discord', label: 'Discord', source: 'the Discord developer portal', sourceURL: 'https://discord.com/developers/applications', tokenPlaceholder: 'Bot token' },
]
