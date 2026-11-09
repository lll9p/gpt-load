<script setup lang="ts">
import { outboundProxiesApi } from "@/api/outbound-proxies";
import type { OutboundProxy } from "@/types/models";
import {
  NAlert,
  NButton,
  NCard,
  NEmpty,
  NForm,
  NFormItem,
  NInput,
  NModal,
  NSpace,
  NSpin,
  NSwitch,
  NTable,
  NTag,
  NText,
  NTooltip,
  useDialog,
  useMessage,
} from "naive-ui";
import { computed, onMounted, reactive, ref } from "vue";
import { useI18n } from "vue-i18n";

const { t } = useI18n();
const dialog = useDialog();
const message = useMessage();
const expanded = ref(false);
const search = ref("");
const draft = ref("");
const batchError = ref("");
const proxies = ref<OutboundProxy[]>([]);
const loading = ref(false);
const loaded = ref(false);
const failed = ref(false);
const saving = ref(false);
const mutatingId = ref<number | null>(null);
const confirming = ref(false);
const show = ref(false);
const mask = ref(true);
const notice = ref<"savedRefreshFailed" | "deletedRefreshFailed" | null>(null);
const editingId = ref<number | null>(null);
const form = reactive({ name: "", url: "", enabled: true });
const formRef = ref<InstanceType<typeof NForm> | null>(null);
const enabledCount = computed(() => proxies.value.filter(proxy => proxy.enabled).length);
const busy = computed(
  () => loading.value || saving.value || mutatingId.value !== null || confirming.value || show.value
);
const unavailable = computed(() => busy.value || failed.value || !loaded.value);

const filteredProxies = computed(() => {
  const query = search.value.trim().toLowerCase();
  return proxies.value.filter(proxy => {
    const url = parseProxyUrl(proxy.url);
    return `${proxy.name} ${url?.protocol ?? ""} ${url?.host ?? ""}`.toLowerCase().includes(query);
  });
});

// Match Go net/url userinfo byte escaping, including non-UTF8 percent bytes.
function canonicalUserinfoPart(raw: string): string {
  return raw.replace(/%[a-f\d]{2}|./gi, token => {
    const byte = token.startsWith("%") ? parseInt(token.slice(1), 16) : token.charCodeAt(0);
    const char = String.fromCharCode(byte);
    return /^[a-z\d._~$&+,;=-]$/i.test(char)
      ? char
      : `%${byte.toString(16).toUpperCase().padStart(2, "0")}`;
  });
}

function parseProxyUrl(raw: string): {
  protocol: string;
  host: string;
  canonical: string;
  hasCredentials: boolean;
} | null {
  // Validate raw components before WHATWG can rewrite IDNs, IPv4 or dot paths.
  const parts = /^(https?|socks5h?):\/\/([^/?#]*)(\/?)$/i.exec(raw);
  if (!parts || /\s/.test(raw)) {
    return null;
  }
  const protocol = `${parts[1].toLowerCase()}:`;
  const authority = parts[2];
  const at = authority.lastIndexOf("@");
  let credentials = "";
  if (at !== -1) {
    const userinfo = authority.slice(0, at);
    if (/[^a-z\d._~!$&'()*+,;=:%@-]/i.test(userinfo) || /%(?![a-f\d]{2})/i.test(userinfo)) {
      return null;
    }
    const colon = userinfo.indexOf(":");
    credentials =
      colon === -1
        ? canonicalUserinfoPart(userinfo)
        : `${canonicalUserinfoPart(userinfo.slice(0, colon))}:${canonicalUserinfoPart(userinfo.slice(colon + 1))}`;
    credentials += "@";
  }
  const hostParts = /^(?:\[([^\]]+)\]|([^:]+))(?::(\d+))?$/.exec(authority.slice(at + 1));
  if (!hostParts) {
    return null;
  }
  const rawHostname = hostParts[1] ?? hostParts[2];
  if (/[^\x21-\x7e]/.test(rawHostname)) {
    return null;
  }
  const hostname = rawHostname.toLowerCase();
  if (hostname.includes(":")) {
    // Use the browser only to validate IPv6, never its compressed hostname.
    if (!/^[a-f\d:.]+$/i.test(hostname)) {
      return null;
    }
    try {
      new URL(`http://[${hostname}]`);
    } catch {
      return null;
    }
  } else if (
    hostname.length > 253 ||
    !hostname
      .replace(/\.$/, "")
      .split(".")
      .every(label => /^[a-z\d](?:[a-z\d-]{0,61}[a-z\d])?$/i.test(label))
  ) {
    return null;
  }
  let port = hostParts[3] === undefined ? "" : String(Number(hostParts[3]));
  if (port && (Number(port) < 1 || Number(port) > 65535)) {
    return null;
  }
  if (
    (protocol === "http:" && port === "80") ||
    (protocol === "https:" && port === "443") ||
    ((protocol === "socks5:" || protocol === "socks5h:") && port === "1080")
  ) {
    port = "";
  }
  const host = `${hostname.includes(":") ? `[${hostname}]` : hostname}${port ? `:${port}` : ""}`;
  return host.length <= 255
    ? { protocol, host, canonical: `${protocol}//${credentials}${host}`, hasCredentials: at !== -1 }
    : null;
}

const preview = computed(() => {
  const canonical = (raw: string) => parseProxyUrl(raw)?.canonical ?? raw.trim();
  const existing = new Set(proxies.value.map(proxy => canonical(proxy.url)));
  const seen = new Set<string>();
  return draft.value
    .split(/\r?\n/)
    .map((line, index) => ({ url: line.trim(), line: index + 1 }))
    .filter(row => row.url)
    .map(row => {
      const parsed = parseProxyUrl(row.url);
      const key = canonical(row.url);
      const status = !parsed
        ? "invalid"
        : existing.has(key)
          ? "existing"
          : seen.has(key)
            ? "duplicate"
            : "new";
      seen.add(key);
      return {
        ...row,
        name: parsed?.host ?? "-",
        address: parsed
          ? `${parsed.protocol}//${parsed.hasCredentials ? "***:***@" : ""}${parsed.host}`
          : "-",
        status,
      };
    });
});
const previewCounts = computed(() => ({
  added: preview.value.filter(row => row.status === "new").length,
  skipped: preview.value.filter(row => row.status === "existing" || row.status === "duplicate")
    .length,
  invalid: preview.value.filter(row => row.status === "invalid").length,
}));
const canBatchSave = computed(
  () =>
    preview.value.length <= 500 &&
    preview.value.some(row => row.status === "new") &&
    !preview.value.some(row => row.status === "invalid")
);

function toggleExpanded() {
  expanded.value = !expanded.value;
  if (!expanded.value) {
    mask.value = true;
  }
}

async function closeEditor() {
  if (saving.value || confirming.value) {
    return;
  }
  if (
    editingId.value === null &&
    draft.value.trim() &&
    !(await confirmRisk(t("outboundProxies.discardDraft")))
  ) {
    return;
  }
  show.value = false;
  mask.value = true;
}

async function saveBatch() {
  if (saving.value || !canBatchSave.value || failed.value || loading.value) {
    return;
  }
  // Send every nonempty line so server skip counts include local duplicates.
  const rows = preview.value;
  saving.value = true;
  batchError.value = "";
  try {
    const result = await outboundProxiesApi.batch(
      rows.map(row => ({ url: row.url, enabled: form.enabled }))
    );
    message.success(t("outboundProxies.batchResult", result));
    expanded.value = true;
    search.value = "";
    show.value = false;
    clearEditor();
    if (!(await load())) {
      notice.value = "savedRefreshFailed";
    }
  } catch (error) {
    const response = (
      error as { response?: { status?: number; data?: { data?: { index?: number } } } }
    ).response;
    const index = response?.data?.data?.index;
    batchError.value =
      response?.status === 400 && typeof index === "number" && rows[index]
        ? t("outboundProxies.invalidLine", { line: rows[index].line })
        : t("outboundProxies.batchUnknown");
  } finally {
    saving.value = false;
  }
}

function displayUrl(raw: string): string {
  if (!mask.value) {
    return raw;
  }
  try {
    const url = new URL(raw);
    return `${url.protocol}//${url.username || url.password ? "***:***@" : ""}${url.host}`;
  } catch {
    return "-";
  }
}

function protocolLabel(raw: string): string {
  try {
    return new URL(raw).protocol.slice(0, -1).toUpperCase();
  } catch {
    return "-";
  }
}

async function load() {
  loading.value = true;
  try {
    proxies.value = await outboundProxiesApi.list();
    loaded.value = true;
    failed.value = false;
    notice.value = null;
    return true;
  } catch {
    failed.value = true;
    return false;
  } finally {
    loading.value = false;
  }
}

function edit(proxy?: OutboundProxy) {
  if (unavailable.value) {
    return;
  }
  draft.value = "";
  batchError.value = "";
  editingId.value = proxy?.id ?? null;
  Object.assign(form, {
    name: proxy?.name ?? "",
    url: proxy?.url ?? "",
    enabled: proxy?.enabled ?? true,
  });
  show.value = true;
}

function clearEditor() {
  draft.value = "";
  batchError.value = "";
  mask.value = true;
  Object.assign(form, { name: "", url: "", enabled: true });
  editingId.value = null;
}

function confirmRisk(content: string, deleting = false): Promise<boolean> {
  confirming.value = true;
  return new Promise(resolve => {
    const finish = (accepted: boolean) => {
      confirming.value = false;
      resolve(accepted);
    };
    dialog.warning({
      title: t(deleting ? "outboundProxies.deleteTitle" : "outboundProxies.confirmTitle"),
      content,
      positiveText: t(deleting ? "common.delete" : "common.confirm"),
      negativeText: t("common.cancel"),
      closable: false,
      maskClosable: false,
      closeOnEsc: false,
      onPositiveClick: () => finish(true),
      onNegativeClick: () => finish(false),
    });
  });
}

function disableWarning() {
  return t(
    enabledCount.value === 1
      ? "outboundProxies.disableLastConfirm"
      : "outboundProxies.disableConfirm"
  );
}

async function toggle(proxy: OutboundProxy, enabled: boolean) {
  if (unavailable.value) {
    return;
  }
  mutatingId.value = proxy.id;
  try {
    if (!enabled && !(await confirmRisk(disableWarning()))) {
      return;
    }
    await outboundProxiesApi.update(proxy.id, { enabled });
    if (!(await load())) {
      notice.value = "savedRefreshFailed";
    }
  } catch {
    // Shared HTTP handling reports errors; retain the last confirmed row state.
  } finally {
    mutatingId.value = null;
  }
}

async function save() {
  if (saving.value || failed.value || loading.value) {
    return;
  }
  saving.value = true;
  try {
    await formRef.value?.validate();
    const previous = proxies.value.find(proxy => proxy.id === editingId.value);
    if (editingId.value === null || !previous) {
      return;
    }
    const changes: Partial<Pick<OutboundProxy, "name" | "url" | "enabled">> = {};
    if (form.name.trim() !== previous.name) {
      changes.name = form.name.trim();
    }
    if (parseProxyUrl(form.url.trim())?.canonical !== parseProxyUrl(previous.url)?.canonical) {
      changes.url = form.url.trim();
    }
    if (form.enabled !== previous.enabled) {
      changes.enabled = form.enabled;
    }
    if (!Object.keys(changes).length) {
      show.value = false;
      clearEditor();
      return;
    }
    if (changes.enabled === false && !(await confirmRisk(disableWarning()))) {
      return;
    }
    await outboundProxiesApi.update(editingId.value, changes);
    show.value = false;
    clearEditor();
    if (!(await load())) {
      notice.value = "savedRefreshFailed";
    }
  } catch {
    // Validation stays inline; request errors are reported by shared HTTP handling.
  } finally {
    saving.value = false;
  }
}

async function remove(proxy: OutboundProxy) {
  if (unavailable.value) {
    return;
  }
  mutatingId.value = proxy.id;
  try {
    const key = !proxy.enabled
      ? "deleteDisabledConfirm"
      : enabledCount.value === 1
        ? "deleteLastConfirm"
        : "deleteEnabledConfirm";
    if (!(await confirmRisk(t(`outboundProxies.${key}`, { name: proxy.name }), true))) {
      return;
    }
    await outboundProxiesApi.remove(proxy.id);
    if (!(await load())) {
      notice.value = "deletedRefreshFailed";
    }
  } catch {
    // Keep the row on failure; shared HTTP handling reports the error.
  } finally {
    mutatingId.value = null;
  }
}

function validUrl(_rule: unknown, value: string) {
  return Boolean(parseProxyUrl(value.trim())) || new Error(t("outboundProxies.invalidUrl"));
}

onMounted(load);
</script>

<template>
  <n-card size="small" :content-style="!expanded && !notice ? { padding: '0' } : undefined">
    <template #header>
      <div class="proxy-header">
        <span>{{ t("outboundProxies.title") }}</span>
        <n-text v-if="loaded && !failed" depth="3">
          {{ t("outboundProxies.summary", { enabled: enabledCount, total: proxies.length }) }}
        </n-text>
        <n-text v-else depth="3">
          {{ t(failed ? "outboundProxies.stateUnknown" : "outboundProxies.notLoaded") }}
        </n-text>
        <n-button :disabled="unavailable" @click="edit()">{{ t("outboundProxies.add") }}</n-button>
        <n-button :aria-expanded="expanded" :disabled="saving" @click="toggleExpanded">
          {{ t(expanded ? "outboundProxies.collapse" : "outboundProxies.expand") }}
        </n-button>
        <n-button v-if="failed" :loading="loading" :disabled="busy" @click="load">
          {{ t("outboundProxies.refresh") }}
        </n-button>
      </div>
    </template>
    <n-alert v-if="!expanded && notice" type="warning">
      {{ t(`outboundProxies.${notice}`) }}
    </n-alert>
    <n-space v-if="expanded" vertical>
      <n-space>
        <n-button :loading="loading" :disabled="busy" @click="load">
          {{ t("outboundProxies.refresh") }}
        </n-button>
        <n-tooltip>
          <template #trigger>
            <n-button
              :type="mask ? 'primary' : 'default'"
              :aria-pressed="mask"
              @click="mask = !mask"
            >
              {{ t("outboundProxies.mask") }}
            </n-button>
          </template>
          {{ t(mask ? "outboundProxies.revealHint" : "outboundProxies.maskHint") }}
        </n-tooltip>
      </n-space>
      <n-text v-if="!mask" type="warning">{{ t("outboundProxies.revealedWarning") }}</n-text>
      <n-input
        v-model:value="search"
        :placeholder="t('outboundProxies.searchPlaceholder')"
        clearable
      />
      <n-alert v-if="notice" type="warning">{{ t(`outboundProxies.${notice}`) }}</n-alert>
      <n-alert v-else-if="failed" type="error">
        {{ t(loaded ? "outboundProxies.staleWarning" : "outboundProxies.loadFailed") }}
      </n-alert>
      <n-spin :show="loading">
        <div class="proxy-scroll">
          <n-text v-if="!loaded && loading">{{ t("outboundProxies.loading") }}</n-text>
          <n-empty
            v-else-if="loaded && !failed && !proxies.length"
            :description="t('outboundProxies.empty')"
          >
            <template #extra>
              <n-space vertical align="center">
                <n-text type="warning">{{ t("outboundProxies.emptyRisk") }}</n-text>
              </n-space>
            </template>
          </n-empty>
          <n-alert
            v-if="loaded && !failed && proxies.length && !enabledCount"
            type="warning"
            :show-icon="false"
          >
            {{ t("outboundProxies.allDisabled") }}
          </n-alert>
          <n-empty
            v-if="proxies.length && !filteredProxies.length"
            :description="t('outboundProxies.noMatch')"
          />
          <n-table
            v-if="filteredProxies.length"
            class="proxy-table"
            size="small"
            :single-line="false"
          >
            <thead>
              <tr>
                <th>{{ t("outboundProxies.id") }}</th>
                <th>{{ t("outboundProxies.name") }}</th>
                <th>{{ t("outboundProxies.url") }}</th>
                <th>{{ t("outboundProxies.enabled") }}</th>
                <th>{{ t("outboundProxies.actions") }}</th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="proxy in filteredProxies" :key="proxy.id">
                <td>{{ proxy.id }}</td>
                <td>
                  <n-tooltip>
                    <template #trigger>
                      <span class="proxy-value proxy-truncate">{{ proxy.name }}</span>
                    </template>
                    <span class="proxy-tooltip">{{ proxy.name }}</span>
                  </n-tooltip>
                </td>
                <td>
                  <n-tag size="small" :bordered="false">{{ protocolLabel(proxy.url) }}</n-tag>
                  <n-tooltip>
                    <template #trigger>
                      <span class="proxy-value proxy-truncate">{{ displayUrl(proxy.url) }}</span>
                    </template>
                    <span class="proxy-tooltip">{{ displayUrl(proxy.url) }}</span>
                  </n-tooltip>
                </td>
                <td>
                  <n-switch
                    :value="proxy.enabled"
                    :disabled="unavailable"
                    :loading="mutatingId === proxy.id"
                    :aria-label="t('outboundProxies.toggleLabel', { name: proxy.name })"
                    @update:value="toggle(proxy, $event)"
                  />
                </td>
                <td>
                  <n-space :size="4">
                    <n-button size="small" :disabled="unavailable" @click="edit(proxy)">
                      {{ t("common.edit") }}
                    </n-button>
                    <n-button
                      size="small"
                      :disabled="unavailable"
                      type="error"
                      @click="remove(proxy)"
                    >
                      {{ t("common.delete") }}
                    </n-button>
                  </n-space>
                </td>
              </tr>
            </tbody>
          </n-table>
          <n-space class="proxy-cards" vertical>
            <n-card v-for="proxy in filteredProxies" :key="proxy.id" size="small">
              <n-space vertical :size="8">
                <n-tooltip>
                  <template #trigger>
                    <span class="proxy-value proxy-truncate">
                      #{{ proxy.id }} · {{ proxy.name }}
                    </span>
                  </template>
                  <span class="proxy-tooltip">{{ proxy.name }}</span>
                </n-tooltip>
                <n-tag size="small" :bordered="false">{{ protocolLabel(proxy.url) }}</n-tag>
                <n-tooltip>
                  <template #trigger>
                    <span class="proxy-value proxy-truncate">{{ displayUrl(proxy.url) }}</span>
                  </template>
                  <span class="proxy-tooltip">{{ displayUrl(proxy.url) }}</span>
                </n-tooltip>
                <n-space align="center">
                  <n-switch
                    :value="proxy.enabled"
                    :disabled="unavailable"
                    :loading="mutatingId === proxy.id"
                    :aria-label="t('outboundProxies.toggleLabel', { name: proxy.name })"
                    @update:value="toggle(proxy, $event)"
                  />
                  <n-text>
                    {{ t(proxy.enabled ? "outboundProxies.enabled" : "outboundProxies.disabled") }}
                  </n-text>
                </n-space>
                <n-space>
                  <n-button size="small" :disabled="unavailable" @click="edit(proxy)">
                    {{ t("common.edit") }}
                  </n-button>
                  <n-button
                    size="small"
                    :disabled="unavailable"
                    type="error"
                    @click="remove(proxy)"
                  >
                    {{ t("common.delete") }}
                  </n-button>
                </n-space>
              </n-space>
            </n-card>
          </n-space>
        </div>
      </n-spin>
    </n-space>
  </n-card>
  <n-modal
    :show="show"
    @update:show="!$event && closeEditor()"
    preset="card"
    :title="t(editingId === null ? 'outboundProxies.addTitle' : 'outboundProxies.editTitle')"
    style="width: 600px; max-width: 95vw"
    :mask-closable="false"
    :closable="!saving && !confirming"
    :close-on-esc="!saving && !confirming"
    @after-leave="!show && clearEditor()"
  >
    <n-space v-if="editingId === null" vertical>
      <n-text depth="3">{{ t("outboundProxies.batchHelp", { count: preview.length }) }}</n-text>
      <n-input
        v-model:value="draft"
        type="textarea"
        :aria-label="t('outboundProxies.url')"
        placeholder="http://proxy.example.com:8080&#10;socks5://user:password@proxy.example.com:1080"
        :disabled="saving"
        :autosize="{ minRows: 4, maxRows: 8 }"
        autocomplete="off"
        @update:value="batchError = ''"
      />
      <n-text type="warning">{{ t("outboundProxies.sensitiveInput") }}</n-text>
      <n-space align="center">
        <n-text>{{ t("outboundProxies.enableAfterAdd") }}</n-text>
        <n-switch
          v-model:value="form.enabled"
          :disabled="saving"
          :aria-label="t('outboundProxies.enableAfterAdd')"
        />
      </n-space>
      <n-text :type="previewCounts.invalid || preview.length > 500 ? 'error' : 'default'">
        {{ t("outboundProxies.previewSummary", previewCounts) }}
      </n-text>
      <n-alert v-if="batchError" type="warning">
        {{ batchError }}
        <n-button :loading="loading" :disabled="saving || loading" @click="load">
          {{ t("outboundProxies.refresh") }}
        </n-button>
      </n-alert>
      <div class="proxy-scroll">
        <n-table v-if="preview.length" size="small" :single-line="false">
          <thead>
            <tr>
              <th>{{ t("outboundProxies.line") }}</th>
              <th>{{ t("outboundProxies.name") }}</th>
              <th>{{ t("outboundProxies.url") }}</th>
              <th>{{ t("common.status") }}</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="row in preview" :key="row.line">
              <td>{{ row.line }}</td>
              <td class="proxy-value">{{ row.name }}</td>
              <td class="proxy-value">{{ row.address }}</td>
              <td>{{ t(`outboundProxies.batchStatus_${row.status}`) }}</td>
            </tr>
          </tbody>
        </n-table>
      </div>
    </n-space>
    <n-form v-else ref="formRef" :model="form" label-placement="top" :disabled="saving">
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
        :rule="[
          {
            required: true,
            whitespace: true,
            message: t('outboundProxies.required'),
            trigger: ['input', 'blur'],
          },
          { validator: validUrl, trigger: ['blur'] },
        ]"
      >
        <n-input
          v-model:value="form.url"
          type="password"
          show-password-on="click"
          autocomplete="off"
        />
      </n-form-item>
      <n-form-item :label="t('outboundProxies.enabled')">
        <n-switch v-model:value="form.enabled" />
      </n-form-item>
    </n-form>
    <template #footer>
      <n-space justify="end">
        <n-button :disabled="saving" @click="closeEditor">{{ t("common.cancel") }}</n-button>
        <n-button
          type="primary"
          :loading="saving"
          :disabled="saving || (editingId === null && !canBatchSave)"
          @click="editingId === null ? saveBatch() : save()"
        >
          {{
            editingId === null
              ? t("outboundProxies.addCount", { count: previewCounts.added })
              : t("common.save")
          }}
        </n-button>
      </n-space>
    </template>
  </n-modal>
</template>

<style scoped>
.proxy-scroll {
  max-height: 420px;
  overflow: auto;
}
.proxy-header {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 8px;
}
.proxy-table {
  table-layout: fixed;
  margin-top: 8px;
}
.proxy-table th:first-child {
  width: 56px;
}
.proxy-table th:nth-child(2) {
  width: 18%;
}
.proxy-table th:nth-child(4) {
  width: 80px;
}
.proxy-table th:last-child {
  width: 160px;
}
.proxy-value {
  overflow-wrap: anywhere;
  white-space: normal;
  min-width: 0;
}
.proxy-truncate {
  display: -webkit-box;
  -webkit-box-orient: vertical;
  -webkit-line-clamp: 2;
  overflow: hidden;
}
.proxy-tooltip {
  display: block;
  max-width: min(480px, 80vw);
  overflow-wrap: anywhere;
  white-space: normal;
}
.proxy-cards {
  display: none !important;
}
@media (max-width: 767px) {
  .proxy-table {
    display: none;
  }
  .proxy-cards {
    display: flex !important;
  }
}
</style>
