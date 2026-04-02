+++
name = "browser-orchestrator"
description = "Multi-step browser workflow patterns for web tasks"
version = "1.0.0"
+++

# Browser Orchestrator

You have access to browser automation tools for interacting with web pages. Follow these patterns for reliable multi-step workflows.

## Core Workflow Pattern

1. **Navigate** to the target URL with `browser_navigate`.
2. **Assess** the page state — use `browser_screenshot` (vision LLM) or `browser_extract_text` (non-vision LLM).
3. **Act** on the page — click, fill, select as needed.
4. **Verify** the result — take another screenshot or extract text to confirm the action succeeded.
5. **Repeat** steps 2-4 for each interaction step.
6. **Close** the browser session when done with `browser_close`.

## Vision vs Non-Vision

**If you support vision (you can interpret images):**
- Use `browser_screenshot` to understand page layout and element positions.
- Describe what you see before acting — this catches unexpected states early.
- After filling a form, screenshot to verify the values are correct before submitting.

**If you do NOT support vision:**
- Use `browser_extract_text` to read page content.
- Use `browser_extract_html` with a CSS selector when you need structural information.
- Target elements by CSS selector or text content, not visual position.
- After each action, use `browser_extract_text` to verify the page changed as expected.

## Form Filling

1. Extract the page to identify form fields and their selectors.
2. Fill each field with `browser_fill`, one at a time.
3. Verify all fields are filled correctly before submitting.
4. Click the submit button.
5. Check for validation errors — if present, correct and retry.

## Authentication Flows

1. Navigate to the login page.
2. Fill username and password fields (retrieve credentials from KV store if available).
3. Submit the form.
4. Verify you reached the authenticated state (check for profile name, dashboard, etc.).
5. The browser profile retains session cookies — subsequent tasks may skip login.

## Error Recovery

- **Element not found**: Wait briefly with `browser_wait`, then retry. The page may still be loading.
- **Navigation timeout**: Check if the URL is correct and the site is reachable. Try once more.
- **Unexpected page**: Extract text to understand where you are. Navigate back or to the correct URL.
- **CAPTCHA**: Inform the user — you cannot solve CAPTCHAs. Ask them to complete it manually if possible.

## Multi-Page Workflows

When a task spans multiple pages (e.g., search → click result → extract data):
- Keep track of your position in the workflow.
- After each navigation, verify you're on the expected page before proceeding.
- If pagination is involved, process one page at a time and track progress.
- Use `browser_list_pages` if you have multiple tabs open.

## Best Practices

- **Be explicit with selectors**: Prefer `#id` or `[data-testid="..."]` over fragile class-based selectors.
- **Wait before acting**: Use `browser_wait` for dynamic content rather than assuming the page is ready.
- **Minimize screenshots**: Each screenshot is a large payload. Only screenshot when you need visual context.
- **Close when done**: Always close the browser session to free resources.
- **Respect rate limits**: Add reasonable pauses between rapid page loads to avoid being blocked.
- **Report progress**: For long workflows, tell the user what step you're on.
