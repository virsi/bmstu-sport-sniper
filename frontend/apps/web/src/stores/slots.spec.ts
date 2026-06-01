import { describe, it, expect, beforeEach } from 'vitest'
import { setActivePinia, createPinia } from 'pinia'
import { useSlotsStore } from '@/stores/slots'
import type { Slot } from '@/types/api'

function makeSlot(id: string): Slot {
  return {
    id,
    week: 14,
    time: '18:00-19:30',
    section: 'Аэробика',
    place: 'СК «Дворец», зал 3',
    teacher_name: 'Иванова А.П.',
    teacher_uid: 'uid_42',
    teacher_rating: 4.5,
    vacancy: 3,
    semester_uuid: 'f1d2-1234',
    day_of_week: 'WEDNESDAY',
  }
}

describe('useSlotsStore.prepend', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
  })

  it('добавляет уникальный слот в начало', () => {
    const s = useSlotsStore()
    s.prepend(makeSlot('a'))
    s.prepend(makeSlot('b'))
    expect(s.slots.map((x) => x.id)).toEqual(['b', 'a'])
  })

  it('дедуплицирует по id', () => {
    const s = useSlotsStore()
    s.prepend(makeSlot('a'))
    s.prepend(makeSlot('a'))
    expect(s.slots).toHaveLength(1)
  })
})
