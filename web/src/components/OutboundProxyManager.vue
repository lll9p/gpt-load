<script setup lang="ts">
import { outboundProxiesApi } from "@/api/outbound-proxies";
import type { OutboundProxy } from "@/types/models";
import {
  NAlert,
  NButton,
  NCard,
  NForm,
  NFormItem,
  NInput,
  NModal,
  NSpace,
  NSpin,
  NSwitch,
  NText,
  useDialog,
} from "naive-ui";
import { onMounted, reactive, ref } from "vue";
import { useI18n } from "vue-i18n";

const { t } = useI18n();
const dialog = useDialog();
const proxies = ref<OutboundProxy[]>([]);
const loading = ref(false);
const failed = ref(false);
const saving = ref(false);
const show = ref(false);
const editingId = ref<number | null>(null);
const form = reactive({ name: "", url: "", enabled: true });
const formRef = ref<InstanceType<typeof NForm> | null>(null);

// Never display raw credentials outside the explicitly revealable editor.
function maskedUrl(raw: string): string {
  try {
    const url = new URL(raw);
    return `${url.protocol}//${url.username || url.password ? "***:***@" : ""}${url.host}`;
  } catch {
    return "-";
  }
}

async function load() {
  loading.value = true;
  failed.value = false;
  try {
    proxies.value = await outboundProxiesApi.list();
  } catch {
    failed.value = true;
  } finally {
    loading.value = false;
  }
}

function edit(proxy?: OutboundProxy) {
  editingId.value = proxy?.id ?? null;
  Object.assign(form, {
    name: proxy?.name ?? "",
    url: proxy?.url ?? "",
    enabled: proxy?.enabled ?? true,
  });
  show.value = true;
}

function clearEditor() {
  Object.assign(form, { name: "", url: "", enabled: true });
  editingId.value = null;
}

async function save() {
  if (saving.value) {
    return;
  }
  saving.value = true;
  try {
    await formRef.value?.validate();
    if (editingId.value === null) {
      await outboundProxiesApi.create({ ...form });
    } else {
      await outboundProxiesApi.update(editingId.value, { ...form });
    }
    show.value = false;
    clearEditor();
    await load();
  } catch {
    // Shared HTTP handling reports the request error; keep the editor for retry.
  } finally {
    saving.value = false;
  }
}

function remove(proxy: OutboundProxy) {
  dialog.warning({
    title: t("common.delete"),
    content: t("outboundProxies.deleteConfirm", { name: proxy.name }),
    positiveText: t("common.delete"),
    negativeText: t("common.cancel"),
    onPositiveClick: async () => {
      try {
        await outboundProxiesApi.remove(proxy.id);
        await load();
      } catch {
        return false;
      }
    },
  });
}

onMounted(load);
</script>

<template>
  <n-card :title="t('outboundProxies.title')" size="small">
    <n-space vertical>
      <n-alert type="info" :show-icon="false">{{ t("outboundProxies.help") }}</n-alert>
      <n-space>
        <n-button @click="edit()">{{ t("outboundProxies.add") }}</n-button>
        <n-button :loading="loading" @click="load">{{ t("outboundProxies.refresh") }}</n-button>
      </n-space>
      <n-alert v-if="failed" type="error">{{ t("outboundProxies.loadFailed") }}</n-alert>
      <n-spin :show="loading">
        <n-space vertical>
          <n-text v-if="!failed && !proxies.length">{{ t("outboundProxies.empty") }}</n-text>
          <n-card v-for="proxy in proxies" :key="proxy.id" size="small">
            <n-space align="center" justify="space-between">
              <n-space vertical :size="4">
                <n-text>{{ proxy.name }}</n-text>
                <n-text depth="3">{{ maskedUrl(proxy.url) }}</n-text>
                <n-text>
                  {{ proxy.enabled ? t("outboundProxies.enabled") : t("outboundProxies.disabled") }}
                </n-text>
              </n-space>
              <n-space>
                <n-button :disabled="loading" @click="edit(proxy)">{{ t("common.edit") }}</n-button>
                <n-button :disabled="loading" type="error" @click="remove(proxy)">
                  {{ t("common.delete") }}
                </n-button>
              </n-space>
            </n-space>
          </n-card>
        </n-space>
      </n-spin>
    </n-space>
  </n-card>
  <n-modal
    v-model:show="show"
    preset="card"
    :title="t('outboundProxies.title')"
    style="width: 600px; max-width: 95vw"
    :mask-closable="!saving"
    :closable="!saving"
    :close-on-esc="!saving"
    @after-leave="clearEditor"
  >
    <n-form ref="formRef" :model="form" label-placement="top" :disabled="saving">
      <n-form-item
        :label="t('outboundProxies.name')"
        path="name"
        :rule="{
          required: true,
          whitespace: true,
          message: t('outboundProxies.required'),
          trigger: ['input', 'blur'],
        }"
      >
        <n-input v-model:value="form.name" />
      </n-form-item>
      <n-form-item
        :label="t('outboundProxies.url')"
        path="url"
        :rule="{
          required: true,
          whitespace: true,
          message: t('outboundProxies.required'),
          trigger: ['input', 'blur'],
        }"
      >
        <n-input
          v-model:value="form.url"
          type="password"
          show-password-on="click"
          autocomplete="off"
        />
      </n-form-item>
      <n-text depth="3">{{ t("outboundProxies.urlHelp") }}</n-text>
      <n-form-item :label="t('outboundProxies.enabled')">
        <n-switch v-model:value="form.enabled" />
      </n-form-item>
      <n-alert type="warning" :show-icon="false">{{ t("outboundProxies.changeWarning") }}</n-alert>
    </n-form>
    <template #footer>
      <n-space justify="end">
        <n-button :disabled="saving" @click="show = false">{{ t("common.cancel") }}</n-button>
        <n-button type="primary" :loading="saving" @click="save">{{ t("common.save") }}</n-button>
      </n-space>
    </template>
  </n-modal>
</template>
