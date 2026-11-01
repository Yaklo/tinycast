import { describe, expect, it, vi } from 'vitest'
import { mount } from '@vue/test-utils'
import RoomCard from './RoomCard.vue'
import type { RoomView } from '../types'

const baseRoom: RoomView = {
  username: 'alex',
  room: '深夜写代码',
  state: 'LIVE',
  viewerCount: 12,
  startedAt: Date.now() - 60_000,
  graceUntil: 0,
}

function mountCard(room: RoomView) {
  return mount(RoomCard, {
    props: { room },
    global: {
      stubs: { RouterLink: { props: ['to'], template: '<a :href="to"><slot /></a>' } },
    },
  })
}

describe('RoomCard', () => {
  it('展示房间名、用户名、状态与在线人数', () => {
    const wrapper = mountCard(baseRoom)
    expect(wrapper.text()).toContain('深夜写代码')
    expect(wrapper.text()).toContain('@alex')
    expect(wrapper.text()).toContain('直播中')
    expect(wrapper.text()).toContain('12')
    expect(wrapper.find('a').attributes('href')).toBe('/r/alex')
  })

  it('GRACE 状态显示保留倒计时而不是开播时间', () => {
    vi.useFakeTimers()
    vi.setSystemTime(new Date('2026-11-01T12:00:00Z'))
    const wrapper = mountCard({
      ...baseRoom,
      state: 'GRACE',
      graceUntil: Date.now() + 125_000,
    })
    expect(wrapper.text()).toContain('断流保留')
    expect(wrapper.text()).toContain('保留中 · 2:05')
    expect(wrapper.text()).not.toContain('开播于')
    vi.useRealTimers()
  })
})
