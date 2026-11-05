import type { OutboundProxy } from "@/types/models";
import http from "@/utils/http";

export type OutboundProxyInput = Pick<OutboundProxy, "name" | "url" | "enabled">;

export const outboundProxiesApi = {
  async list(): Promise<OutboundProxy[]> {
    const res = await http.get("/outbound-proxies");
    return res.data || [];
  },
  async create(data: OutboundProxyInput): Promise<void> {
    await http.post("/outbound-proxies", data);
  },
  async update(id: number, data: Partial<OutboundProxyInput>): Promise<void> {
    await http.put(`/outbound-proxies/${id}`, data);
  },
  async remove(id: number): Promise<void> {
    await http.delete(`/outbound-proxies/${id}`);
  },
};
