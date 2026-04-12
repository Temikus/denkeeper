// @ts-check
import { test, expect } from '@playwright/test'

const API_KEY = 'e2e-test-token-12345678'

test.describe('Skills CRUD via API', () => {
  test('create, list, and delete a skill', async ({ request }) => {
    const name = `e2e-skill-${Date.now()}`

    // Create
    const createRes = await request.post('/api/v1/skills/default', {
      headers: { Authorization: `Bearer ${API_KEY}` },
      data: { name, description: 'A test skill', body: '# Test skill body' },
    })
    expect(createRes.ok()).toBeTruthy()

    // List
    const listRes = await request.get('/api/v1/skills/default', {
      headers: { Authorization: `Bearer ${API_KEY}` },
    })
    expect(listRes.ok()).toBeTruthy()
    const skills = await listRes.json()
    expect(skills.some(s => s.name === name)).toBeTruthy()

    // Delete
    const delRes = await request.delete(`/api/v1/skills/default/${encodeURIComponent(name)}`, {
      headers: { Authorization: `Bearer ${API_KEY}` },
    })
    expect(delRes.ok()).toBeTruthy()
  })
})

test.describe('Costs API', () => {
  test('returns cost data', async ({ request }) => {
    const res = await request.get('/api/v1/costs', {
      headers: { Authorization: `Bearer ${API_KEY}` },
    })
    expect(res.ok()).toBeTruthy()
    const data = await res.json()
    expect(data).toHaveProperty('global_cost')
    expect(data).toHaveProperty('cost_limits')
    expect(data.cost_limits).toHaveProperty('soft')
    expect(data.cost_limits).toHaveProperty('hard')
  })
})

test.describe('Auth status API', () => {
  test('returns auth configuration', async ({ request }) => {
    const res = await request.get('/api/v1/auth/status', {
      headers: { Authorization: `Bearer ${API_KEY}` },
    })
    expect(res.ok()).toBeTruthy()
    const data = await res.json()
    expect(data).toHaveProperty('password_enabled')
    expect(data).toHaveProperty('sessions_trackable')
  })
})
