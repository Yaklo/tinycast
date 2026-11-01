import { ref } from 'vue'

export type PublishDialogMode = 'form' | 'saved'

/** 创建推流配置弹窗的显隐状态，首页与页头共享。 */
export const publishDialogOpen = ref(false)
export const publishDialogMode = ref<PublishDialogMode>('form')

export function openPublishDialog(mode: PublishDialogMode = 'form'): void {
  publishDialogMode.value = mode
  publishDialogOpen.value = true
}

export function closePublishDialog(): void {
  publishDialogOpen.value = false
}
