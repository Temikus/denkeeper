// @ts-check

/** Page object for the Chat page. */
export class ChatPage {
  /** @param {import('@playwright/test').Page} page */
  constructor(page) {
    this.page = page
  }

  async goto() {
    await this.page.goto('/#/chat')
    await this.page.locator('[data-testid="chat-input"]').waitFor({ state: 'visible', timeout: 10000 })
  }

  /** Get the agent selector element. */
  agentSelector() {
    return this.page.locator('[data-testid="agent-selector"]')
  }

  /** Get the session selector element. */
  sessionSelector() {
    return this.page.locator('[data-testid="session-selector"]')
  }

  /** Type and send a message. */
  async sendMessage(text) {
    const input = this.page.locator('[data-testid="chat-input"]')
    await input.fill(text)
    await this.page.locator('[data-testid="chat-send"]').click()
  }

  /** Wait for an assistant response bubble to appear (streaming finished — no typing dots). */
  async waitForResponse(timeout = 30000) {
    // Wait for at least one agent bubble that is not still streaming.
    await this.page.locator('.bubble.agent:not(.streaming)').first().waitFor({ state: 'visible', timeout })
  }

  /** Return all assistant message texts. */
  async assistantMessages() {
    const bubbles = this.page.locator('.bubble.agent .text')
    return bubbles.allTextContents()
  }

  /** Return the last assistant message text. */
  async lastAssistantMessage() {
    const msgs = await this.assistantMessages()
    return msgs.length > 0 ? msgs[msgs.length - 1] : null
  }

  /** Return the messages container. */
  messagesContainer() {
    return this.page.locator('[data-testid="chat-messages"]')
  }

  /** Check if the chat input is visible (page loaded correctly). */
  async isReady() {
    return this.page.locator('[data-testid="chat-input"]').isVisible()
  }
}
