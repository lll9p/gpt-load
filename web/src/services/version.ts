import axios from "axios";

export interface GitHubRelease {
  tag_name: string;
  html_url: string;
  published_at: string;
  name: string;
}

export interface VersionInfo {
  currentVersion: string;
  latestVersion: string | null;
  isLatest: boolean;
  hasUpdate: boolean;
  releaseUrl: string | null;
  lastCheckTime: number;
  status: "checking" | "latest" | "update-available" | "unreleased" | "error";
}

const CACHE_KEY = "lll9p-gpt-load-lite-version-info";
const CACHE_DURATION = 30 * 60 * 1000;

class VersionService {
  private currentVersion: string;

  constructor() {
    this.currentVersion = import.meta.env.VITE_VERSION || "1.0.0";
  }

  /**
   * 获取缓存的版本信息
   */
  private getCachedVersionInfo(): VersionInfo | null {
    try {
      const cached = localStorage.getItem(CACHE_KEY);
      if (!cached) {
        return null;
      }

      const versionInfo: VersionInfo = JSON.parse(cached);
      const now = Date.now();

      // 检查缓存是否过期
      if (now - versionInfo.lastCheckTime > CACHE_DURATION) {
        return null;
      }

      // 检查缓存中的版本号是否与当前应用版本号一致
      if (versionInfo.currentVersion !== this.currentVersion) {
        this.clearCache();
        return null;
      }

      return versionInfo;
    } catch (error) {
      console.warn("Failed to parse cached version info:", error);
      localStorage.removeItem(CACHE_KEY);
      return null;
    }
  }

  /**
   * 缓存版本信息
   */
  private setCachedVersionInfo(versionInfo: VersionInfo): void {
    try {
      localStorage.setItem(CACHE_KEY, JSON.stringify(versionInfo));
    } catch (error) {
      console.warn("Failed to cache version info:", error);
    }
  }

  /**
   * 比较版本号 (简单的语义化版本比较)
   */
  private compareVersions(current: string, latest: string): number {
    const currentParts = current.replace(/^v/, "").split(".").map(Number);
    const latestParts = latest.replace(/^v/, "").split(".").map(Number);

    for (let i = 0; i < Math.max(currentParts.length, latestParts.length); i++) {
      const currentPart = currentParts[i] || 0;
      const latestPart = latestParts[i] || 0;

      if (currentPart < latestPart) {
        return -1;
      }
      if (currentPart > latestPart) {
        return 1;
      }
    }

    return 0;
  }

  /**
   * 从 GitHub API 获取最新版本
   */
  private async fetchLatestVersion(): Promise<GitHubRelease | null> {
    try {
      const response = await axios.get(
        "https://api.github.com/repos/lll9p/gpt-load-lite/releases/latest",
        {
          timeout: 10000,
          headers: {
            Accept: "application/vnd.github.v3+json",
          },
        }
      );

      if (
        response.status !== 200 ||
        typeof response.data?.tag_name !== "string" ||
        !response.data.tag_name.trim()
      ) {
        throw new Error("Invalid GitHub release response");
      }

      return response.data;
    } catch (error) {
      if (axios.isAxiosError(error) && error.response?.status === 404) {
        return null;
      }
      throw error;
    }
  }

  /**
   * 检查版本更新
   */
  async checkForUpdates(): Promise<VersionInfo> {
    // 先检查缓存
    const cached = this.getCachedVersionInfo();
    if (cached) {
      return cached;
    }

    // 创建初始状态
    const versionInfo: VersionInfo = {
      currentVersion: this.currentVersion,
      latestVersion: null,
      isLatest: false,
      hasUpdate: false,
      releaseUrl: null,
      lastCheckTime: Date.now(),
      status: "checking",
    };

    try {
      const release = await this.fetchLatestVersion();

      if (release) {
        const comparison = this.compareVersions(this.currentVersion, release.tag_name);

        versionInfo.latestVersion = release.tag_name;
        versionInfo.releaseUrl = release.html_url;
        versionInfo.isLatest = comparison >= 0;
        versionInfo.hasUpdate = comparison < 0;
        versionInfo.status = comparison < 0 ? "update-available" : "latest";
      } else {
        versionInfo.status = "unreleased";
      }

      // 只缓存已确认的发布状态，不缓存请求失败
      this.setCachedVersionInfo(versionInfo);
    } catch (error) {
      console.warn("Version check failed:", error);
      versionInfo.status = "error";
    }

    return versionInfo;
  }

  /**
   * 获取当前版本号
   */
  getCurrentVersion(): string {
    return this.currentVersion;
  }

  /**
   * 清除缓存
   */
  clearCache(): void {
    localStorage.removeItem(CACHE_KEY);
  }
}

export const versionService = new VersionService();
