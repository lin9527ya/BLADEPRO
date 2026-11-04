"use client";

import * as React from "react";

import { BellRingIcon, CheckIcon, RefreshCwIcon, XIcon } from "lucide-react";
import { toast } from "sonner";

import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { api } from "@/lib/api";
import type { Approval } from "@/lib/types";

const KIND_LABEL: Record<Approval["kind"], string> = {
  knowledge: "知识提案",
  ammo_global: "全局弹药",
  vendor_profile: "厂商档案",
  rating_rule: "定级规则",
};

const STATUSES = [
  { value: "pending", label: "待审批" },
  { value: "approved", label: "已批准" },
  { value: "rejected", label: "已驳回" },
];

function fmtTime(s?: string) {
  if (!s) return "—";
  return new Date(s).toLocaleString("zh-CN", {
    year: "numeric",
    month: "2-digit",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
  });
}

function payloadSummary(p: Record<string, unknown>): string {
  const pick = (k: string) => (typeof p[k] === "string" ? (p[k] as string) : "");
  return pick("title") || pick("summary") || pick("vulnclass") || pick("key") || JSON.stringify(p).slice(0, 80);
}

/** 统一审批页：知识 / 全局弹药 / 厂商档案 / 定级规则（命令审批仍在 /system/intercept）。 */
export default function ApprovalsPage() {
  const [status, setStatus] = React.useState("pending");
  const [rows, setRows] = React.useState<Approval[]>([]);
  const [loading, setLoading] = React.useState(false);
  const [expanded, setExpanded] = React.useState<number | null>(null);

  const load = React.useCallback(() => {
    setLoading(true);
    api
      .approvals({ status, limit: 100 })
      .then((r) => setRows(r.approvals))
      .catch((e: Error) => toast.error(`加载审批失败：${e.message}`))
      .finally(() => setLoading(false));
  }, [status]);

  React.useEffect(() => {
    load();
  }, [load]);

  async function decide(a: Approval, approve: boolean) {
    try {
      await api.decideApproval(a.id, approve, approve ? "" : "人工驳回");
      toast.success(approve ? "已批准并生效" : "已驳回");
      load();
    } catch (e) {
      toast.error(`操作失败：${(e as Error).message}`);
    }
  }

  return (
    <div className="flex flex-col gap-4">
      <div className="flex flex-wrap items-end justify-between gap-3">
        <div>
          <h1 className="flex items-center gap-2 font-semibold text-lg">
            <BellRingIcon className="size-5" />
            审批
          </h1>
          <p className="mt-1 text-muted-foreground text-sm">
            知识、全局弹药、厂商档案与定级规则的提案都走这里：批准后才写入知识库并影响后续任务（防「记忆污染」）。
            命令执行审批仍在「系统 → 拦截」。
          </p>
        </div>
        <div className="flex items-center gap-2">
          <Select value={status} onValueChange={setStatus}>
            <SelectTrigger size="sm" className="w-32">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              {STATUSES.map((s) => (
                <SelectItem key={s.value} value={s.value}>
                  {s.label}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
          <Button variant="outline" size="sm" onClick={load} disabled={loading}>
            <RefreshCwIcon className={loading ? "size-4 animate-spin" : "size-4"} />
            刷新
          </Button>
        </div>
      </div>

      <Card className="overflow-hidden">
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead className="w-12">#</TableHead>
              <TableHead className="w-28">类型</TableHead>
              <TableHead>内容</TableHead>
              <TableHead className="w-24">状态</TableHead>
              <TableHead className="w-36">提交时间</TableHead>
              <TableHead className="w-48">操作</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {rows.length === 0 ? (
              <TableRow>
                <TableCell colSpan={6} className="text-muted-foreground py-12 text-center text-sm">
                  {loading ? "加载中…" : "没有待处理提案"}
                </TableCell>
              </TableRow>
            ) : (
              rows.map((a) => (
                <React.Fragment key={a.id}>
                  <TableRow className="cursor-pointer" onClick={() => setExpanded(expanded === a.id ? null : a.id)}>
                    <TableCell className="text-muted-foreground text-xs">{a.id}</TableCell>
                    <TableCell>
                      <Badge variant="secondary">{KIND_LABEL[a.kind] ?? a.kind}</Badge>
                    </TableCell>
                    <TableCell>
                      <div className="line-clamp-1 text-sm">{payloadSummary(a.payload)}</div>
                      {a.reason && <div className="line-clamp-1 text-muted-foreground text-xs">{a.reason}</div>}
                    </TableCell>
                    <TableCell>
                      <Badge variant={a.status === "pending" ? "default" : "outline"}>{a.status}</Badge>
                    </TableCell>
                    <TableCell className="text-muted-foreground text-xs">{fmtTime(a.created_at)}</TableCell>
                    <TableCell>
                      {a.status === "pending" ? (
                        <div className="flex gap-2">
                          <Button size="sm" onClick={() => decide(a, true)}>
                            <CheckIcon className="size-4" />
                            批准
                          </Button>
                          <Button size="sm" variant="outline" onClick={() => decide(a, false)}>
                            <XIcon className="size-4" />
                            驳回
                          </Button>
                        </div>
                      ) : (
                        <span className="text-muted-foreground text-xs">
                          {a.decided_by || "—"} · {fmtTime(a.decided_at)}
                          {a.entry_id ? ` · 条目 #${a.entry_id}` : ""}
                        </span>
                      )}
                    </TableCell>
                  </TableRow>
                  {expanded === a.id && (
                    <TableRow>
                      <TableCell colSpan={6} className="p-0">
                        <div className="border-t bg-muted/40 p-3">
                          <pre className="max-h-72 overflow-auto font-mono text-xs leading-relaxed whitespace-pre-wrap">
                            {JSON.stringify(a.payload, null, 2)}
                          </pre>
                        </div>
                      </TableCell>
                    </TableRow>
                  )}
                </React.Fragment>
              ))
            )}
          </TableBody>
        </Table>
      </Card>
    </div>
  );
}
