// 通用 API 响应结构
export interface ApiResponse<T> {
  code: number;
  message: string;
  data: T;
}

// 密钥状态
export type KeyStatus = "active" | "invalid" | undefined;

export type KeySelectionStrategy = "round_robin" | "fill_first";

// 渠道类型
export type ChannelType = "openai" | "openai-response" | "gemini" | "anthropic" | "tavily";

// 数据模型定义
export interface APIKey {
  id: number;
  group_id: number;
  key_value: string;
  notes?: string;
  outbound_proxy_id?: number;
  outbound_proxy_name?: string;
  status: KeyStatus;
  request_count: number;
  failure_count: number;
  last_used_at?: string;
  created_at: string;
  updated_at: string;
}

export interface UpstreamInfo {
  url: string;
  weight: number;
}

export interface HeaderRule {
  key: string;
  value: string;
  action: "set" | "remove";
}

export interface Group {
  id?: number;
  name: string;
  display_name: string;
  description: string;
  sort: number;
  test_model: string;
  channel_type: ChannelType;
  key_selection_strategy?: KeySelectionStrategy;
  sticky_proxy_enabled: boolean;
  upstreams: UpstreamInfo[];
  validation_endpoint: string;
  config: Record<string, unknown>;
  api_keys?: APIKey[];
  endpoint?: string;
  param_overrides: Record<string, unknown>;
  model_redirect_rules: Record<string, string>;
  model_redirect_strict: boolean;
  header_rules?: HeaderRule[];
  proxy_keys: string;
  created_at?: string;
  updated_at?: string;
}

export interface GroupConfigOption {
  key: string;
  name: string;
  description: string;
  default_value: string | number;
}

// GroupStatsResponse defines the complete statistics for a group.
export interface GroupStatsResponse {
  key_stats: KeyStats;
  stats_24_hour: RequestStats;
  stats_7_day: RequestStats;
  stats_30_day: RequestStats;
}

// KeyStats defines the statistics for API keys in a group.
export interface KeyStats {
  total_keys: number;
  active_keys: number;
  invalid_keys: number;
}

// RequestStats defines the statistics for requests over a period.
export interface RequestStats {
  total_requests: number;
  failed_requests: number;
  failure_rate: number;
}

export type TaskType = "KEY_VALIDATION" | "KEY_IMPORT" | "KEY_DELETE";

export interface KeyValidationResult {
  invalid_keys: number;
  total_keys: number;
  valid_keys: number;
}

export interface KeyImportResult {
  added_count: number;
  ignored_count: number;
}

export interface KeyDeleteResult {
  deleted_count: number;
  ignored_count: number;
}

export interface TaskInfo {
  task_type: TaskType;
  is_running: boolean;
  group_name?: string;
  processed?: number;
  total?: number;
  started_at?: string;
  finished_at?: string;
  result?: KeyValidationResult | KeyImportResult | KeyDeleteResult;
  error?: string;
}

// Based on backend response
export interface RequestLog {
  id: string;
  timestamp: string;
  group_id: number;
  key_id: number;
  is_success: boolean;
  source_ip: string;
  status_code: number;
  request_path: string;
  duration_ms: number;
  error_message: string;
  user_agent: string;
  request_type: "retry" | "final";
  group_name?: string;
  key_value?: string;
  model: string;
  upstream_addr: string;
  is_stream: boolean;
  request_body?: string;
}

export interface Pagination {
  page: number;
  page_size: number;
  total_items: number;
  total_pages: number;
}

export interface LogsResponse {
  items: RequestLog[];
  pagination: Pagination;
}

export interface LogFilter {
  page?: number;
  page_size?: number;
  group_name?: string;
  key_value?: string;
  model?: string;
  is_success?: boolean | null;
  status_code?: number | null;
  source_ip?: string;
  error_contains?: string;
  start_time?: string | null;
  end_time?: string | null;
  request_type?: "retry" | "final";
}

export interface DashboardStats {
  total_requests: number;
  success_requests: number;
  success_rate: number;
  group_stats: GroupRequestStat[];
}

export interface GroupRequestStat {
  display_name: string;
  request_count: number;
}

// 仪表盘统计卡片数据
export interface StatCard {
  value: number;
  sub_value?: number;
  sub_value_tip?: string;
  trend: number;
  trend_is_growth: boolean;
}

// 安全警告信息
export interface SecurityWarning {
  type: string; // 警告类型：auth_key, encryption_key 等
  message: string; // 警告信息
  severity: string; // 严重程度：low, medium, high
  suggestion: string; // 建议解决方案
}

// 仪表盘基础统计响应
export interface DashboardStatsResponse {
  key_count: StatCard;
  rpm: StatCard;
  request_count: StatCard;
  error_rate: StatCard;
  security_warnings: SecurityWarning[];
}

// 图表数据集
export interface ChartDataset {
  label: string;
  data: number[];
  color: string;
}

// 图表数据
export interface ChartData {
  labels: string[];
  datasets: ChartDataset[];
}

export interface OutboundProxy {
  id: number;
  name: string;
  url: string;
  enabled: boolean;
  created_at: string;
  updated_at: string;
}
