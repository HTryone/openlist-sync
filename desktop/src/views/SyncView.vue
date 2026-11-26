<template>
  <div class="relative flex h-full w-full items-center justify-center">
    <div
      class="relative z-1 flex h-full w-full flex-col items-center justify-start gap-4 rounded-xl border-none p-4 overflow-hidden"
    >
      <!-- Header -->
      <div
        class="flex w-full items-center justify-between gap-4 rounded-2xl border border-border-secondary px-2 py-1 shadow-sm"
      >
        <div class="flex flex-1 flex-wrap items-center gap-4 p-2">
          <FolderSync class="text-accent" />
          <div class="flex flex-col gap-0.5">
            <h1 class="m-0 text-xl font-semibold tracking-tight text-main">{{ t('sync.title') }}</h1>
            <span class="text-sm text-secondary">
              {{ jobs.length }} {{ t('sync.stats.jobs') }} · {{ enabledCount }} {{ t('sync.stats.enabled') }}
            </span>
          </div>
        </div>
        <div class="flex gap-3">
          <CustomButton type="secondary" :icon="RefreshCw" :text="t('sync.actions.refresh')" @click="load" />
          <CustomButton type="secondary" :icon="Plus" :text="t('sync.actions.add')" @click="openAdd" />
          <CustomButton
            type="primary"
            :icon="Play"
            :text="running ? t('sync.run.running') : t('sync.actions.syncAll')"
            :disabled="running !== '' || jobs.length === 0"
            @click="run()"
          />
        </div>
      </div>

      <div class="flex w-full flex-1 flex-col gap-4 overflow-auto no-scrollbar">
        <!-- Engine missing -->
        <div
          v-if="engine && !engine.found"
          class="flex w-full items-center border border-border-secondary bg-warning/10 p-3 rounded-md gap-3"
        >
          <TriangleAlert class="text-warning" />
          <p class="flex-1 text-xs font-medium text-secondary select-text">{{ t('sync.engineMissing') }}</p>
        </div>

        <!-- Connection -->
        <SettingSection :icon="LinkIcon" :title="t('sync.connection.title')">
          <SettingCard>
            <CustomInput
              v-model="globals.webdavUrl"
              :title="t('sync.connection.url')"
              :placeholder="t('sync.connection.urlPlaceholder')"
            />
          </SettingCard>
          <SettingCard>
            <CustomInput v-model="globals.user" :title="t('sync.connection.user')" placeholder="admin" />
          </SettingCard>
          <SettingCard>
            <CustomInput
              v-model="globals.pass"
              :title="t('sync.connection.pass')"
              :is-password="true"
              :placeholder="t('sync.connection.passPlaceholder')"
            />
          </SettingCard>
          <SettingCard>
            <CustomInput
              v-model="globals.remoteBase"
              :title="t('sync.connection.remoteBase')"
              :tips="t('sync.connection.remoteBaseTip')"
              placeholder="/ty2"
            />
          </SettingCard>
          <div class="flex justify-end p-2">
            <CustomButton type="primary" :icon="Save" :text="t('sync.connection.save')" @click="saveConfig" />
          </div>
        </SettingSection>

        <!-- Auto sync -->
        <SettingSection :icon="Timer" :title="t('sync.auto.title')">
          <SettingCard>
            <div class="flex w-full items-center justify-between gap-3">
              <div class="flex flex-col gap-0.5">
                <span class="text-sm font-medium text-main">{{ t('sync.auto.enable') }}</span>
                <span class="text-xs text-secondary">{{ t('sync.auto.enableTip') }}</span>
              </div>
              <CustomSwitch v-model="autoEnabled" />
            </div>
          </SettingCard>
          <SettingCard>
            <div class="flex w-full items-end gap-3">
              <div class="flex-1">
                <CustomInput
                  v-model.number="intervalMin"
                  type="number"
                  :title="t('sync.auto.interval')"
                  :tips="t('sync.auto.intervalTip')"
                  :placeholder="String(globals.intervalMin ?? 30)"
                  :min="1"
                />
              </div>
              <CustomButton type="primary" :icon="Save" :text="t('sync.connection.save')" @click="saveConfig" />
            </div>
          </SettingCard>
        </SettingSection>

        <!-- Jobs -->
        <SettingSection :icon="FolderSync" :title="t('sync.jobs.title')">
          <div v-if="jobs.length === 0" class="py-8 text-center text-sm text-secondary">
            {{ t('sync.jobs.empty') }}
          </div>
          <div
            v-for="job in jobs"
            :key="job.id"
            class="flex w-full items-center gap-3 rounded-lg border border-border-secondary px-3 py-2"
          >
            <div class="flex min-w-0 flex-1 flex-col gap-0.5">
              <div class="flex items-center gap-2">
                <span class="truncate text-sm font-semibold text-main">{{ job.name }}</span>
                <span
                  class="rounded-full px-1.5 py-0.5 text-[11px]"
                  :class="job.enabled ? 'bg-success/15 text-success' : 'bg-border-secondary/60 text-secondary'"
                >
                  {{ job.enabled ? t('sync.jobs.enabled') : t('sync.jobs.disabled') }}
                </span>
              </div>
              <span class="truncate text-xs text-secondary">
                {{ job.localDir }} → {{ job.remoteDir || t('sync.jobs.remoteAuto') }}
              </span>
            </div>
            <CustomButton
              type="secondary"
              :icon="Play"
              :text="t('sync.actions.sync')"
              :disabled="running !== ''"
              @click="run(job.id)"
            />
            <CustomButton
              type="secondary"
              :icon="Trash2"
              :text="t('sync.actions.delete')"
              :disabled="running !== ''"
              @click="removeJob(job)"
            />
          </div>
        </SettingSection>

        <!-- Output -->
        <SettingSection v-if="output" :icon="TerminalIcon" :title="t('sync.run.output')">
          <div class="flex w-full items-center justify-end gap-2 px-2 pb-2">
            <span class="text-xs" :class="lastCode === 0 ? 'text-success' : 'text-danger'">
              {{ lastCode === 0 ? t('sync.run.done') : t('sync.run.failed', { code: lastCode }) }}
            </span>
          </div>
          <pre
            class="max-h-72 w-full overflow-auto rounded-lg bg-bg-tertiary p-3 text-[11px] leading-relaxed whitespace-pre-wrap text-secondary select-text"
            >{{ output }}</pre>
        </SettingSection>
      </div>
    </div>

    <!-- Add dialog -->
    <CustomModal v-show="showAdd" v-model:visible="showAdd" :title="t('sync.modal.addTitle')" @close="resetForm">
      <div class="flex w-full flex-col gap-4 p-4">
        <SettingCard>
          <CustomInput
            v-model="form.localDir"
            :title="t('sync.modal.localDir')"
            :tips="t('sync.modal.localDirTip')"
            :placeholder="t('sync.modal.localDirPlaceholder')"
          >
            <template #input-extra>
              <div class="mt-1 flex gap-2">
                <CustomButton
                  type="secondary"
                  text=""
                  :icon="FolderOpen"
                  :title="t('sync.modal.pick')"
                  @click="pickDir"
                />
              </div>
            </template>
          </CustomInput>
        </SettingCard>
        <SettingCard>
          <CustomInput
            v-model="form.name"
            :title="t('sync.modal.name')"
            :tips="t('sync.modal.nameTip')"
            :placeholder="t('sync.modal.namePlaceholder')"
          />
        </SettingCard>
        <SettingCard>
          <CustomInput
            v-model="form.remoteDir"
            :title="t('sync.modal.remoteDir')"
            :tips="t('sync.modal.remoteDirTip')"
            :placeholder="derivedRemote"
          />
        </SettingCard>
        <SettingCard>
          <div class="flex w-full items-center justify-between gap-3">
            <span class="text-sm font-medium text-main">{{ t('sync.modal.enabled') }}</span>
            <CustomSwitch v-model="form.enabled" />
          </div>
        </SettingCard>
      </div>

      <template #footer>
        <CustomButton :icon="X" :text="t('common.cancel')" type="secondary" @click="resetForm" />
        <CustomButton :icon="Plus" :text="t('common.add')" @click="addJob" />
      </template>
    </CustomModal>
  </div>
</template>

<script setup lang="ts">
import {
  FolderOpen,
  FolderSync,
  Link as LinkIcon,
  Play,
  Plus,
  RefreshCw,
  Save,
  Terminal as TerminalIcon,
  Timer,
  Trash2,
  TriangleAlert,
  X,
} from 'lucide-vue-next'
import { computed, onMounted, reactive, ref } from 'vue'

import { TauriAPI } from '@/api/tauri'
import CustomButton from '@/components/common/CustomButton.vue'
import CustomInput from '@/components/common/CustomInput.vue'
import CustomModal from '@/components/common/CustomModal.vue'
import CustomSwitch from '@/components/common/CustomSwitch.vue'
import SettingCard from '@/components/common/SettingCard.vue'
import SettingSection from '@/components/common/SettingSection.vue'
import useConfirm from '@/hooks/useConfirm'
import useMessage from '@/hooks/useMessage'

import { useTranslation } from '../composables/useI18n'

const { t } = useTranslation()
const { success, error } = useMessage()
const { confirm } = useConfirm()

const engine = ref<SyncEngineInfo | null>(null)
const globals = reactive<SyncGlobals>({
  webdavUrl: '',
  user: '',
  pass: '',
  remote: 'openlist',
  remoteBase: '',
  remoteFolderName: 'agents-sy',
  compare: 'size',
})
const jobs = ref<SyncJob[]>([])
const running = ref('')
const output = ref('')
const lastCode = ref<number | null>(null)
const showAdd = ref(false)
const form = reactive({ localDir: '', name: '', remoteDir: '', enabled: true })

const enabledCount = computed(() => jobs.value.filter(j => j.enabled).length)

const autoEnabled = computed({
  get: () => globals.autoEnabled ?? false,
  set: (v: boolean) => {
    globals.autoEnabled = v
    void saveConfig()
  },
})
const intervalMin = computed({
  get: () => globals.intervalMin ?? 30,
  set: (v: number) => {
    globals.intervalMin = v
  },
})

const trimSlash = (s: string) => (s || '').trim().replace(/\/+$/, '')

// 与引擎一致：网盘目标 = 根目录/固定子目录/本地文件夹名
const derivedRemote = computed(() => {
  const base = trimSlash(globals.remoteBase)
  const folder = (globals.remoteFolderName || 'agents-sy').trim()
  const name = (form.name || '').trim() || baseName(form.localDir)
  if (!name) return ''
  return `${base}/${folder}/${name}`
})

function baseName(p: string): string {
  const parts = (p || '').split(/[\\/]/).filter(Boolean)
  return parts.length ? parts[parts.length - 1] : ''
}

async function load() {
  try {
    engine.value = await TauriAPI.sync.engineInfo()
    const cfg = await TauriAPI.sync.loadConfig()
    Object.assign(globals, cfg.globals)
    jobs.value = cfg.jobs ?? []
  } catch (e) {
    error(String(e))
  }
}

async function saveConfig() {
  try {
    await TauriAPI.sync.saveConfig({ globals: { ...globals }, jobs: jobs.value })
    success(t('sync.connection.saved'))
  } catch (e) {
    error(String(e))
  }
}

async function run(jobId?: string) {
  if (running.value) return
  running.value = jobId && jobId.length > 0 ? jobId : 'all'
  output.value = ''
  lastCode.value = null
  try {
    const result = await TauriAPI.sync.run(jobId)
    output.value = result.output || ''
    lastCode.value = result.code
    if (result.code === 0) success(t('sync.run.done'))
    else error(t('sync.run.failed', { code: result.code }))
  } catch (e) {
    lastCode.value = -1
    output.value = String(e)
    error(String(e))
  } finally {
    running.value = ''
    await load()
  }
}

function openAdd() {
  resetForm()
  showAdd.value = true
}

function resetForm() {
  form.localDir = ''
  form.name = ''
  form.remoteDir = ''
  form.enabled = true
  showAdd.value = false
}

async function pickDir() {
  try {
    const picked = await TauriAPI.util.selectDirectory(t('sync.modal.localDir'))
    if (picked) {
      form.localDir = picked
      if (!form.name) form.name = baseName(picked)
      if (!form.remoteDir) form.remoteDir = derivedRemote.value
    }
  } catch (e) {
    error(String(e))
  }
}

async function addJob() {
  const localDir = form.localDir.trim()
  if (!localDir) {
    error(t('sync.modal.localDirRequired'))
    return
  }
  const name = form.name.trim() || baseName(localDir)
  const job: SyncJob = {
    id: `job-${Date.now().toString(36)}`,
    name,
    localDir,
    remoteDir: form.remoteDir.trim() || derivedRemote.value,
    remoteTrashDir: '',
    autoRemote: true,
    enabled: form.enabled,
  }
  jobs.value = [...jobs.value, job]
  await saveConfig()
  resetForm()
}

async function removeJob(job: SyncJob) {
  const ok = await confirm({
    title: t('sync.actions.delete'),
    message: t('sync.confirmRemove', { name: job.name }),
  })
  if (!ok) return
  jobs.value = jobs.value.filter(j => j.id !== job.id)
  await saveConfig()
}

onMounted(load)
</script>
