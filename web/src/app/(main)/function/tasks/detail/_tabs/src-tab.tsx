"use client";

import * as React from "react";

import { CheckCircle2Icon, RefreshCwIcon, ShieldAlertIcon } from "lucide-react";
import { toast } from "sonner";

import { Alert, AlertDescription } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Empty, EmptyDescription, EmptyHeader, EmptyTitle } from "@/components/ui/empty";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Select, SelectContent, SelectGroup, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Skeleton } from "@/components/ui/skeleton";
import { Spinner } from "@/components/ui/spinner";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { Textarea } from "@/components/ui/textarea";
import { api } from "@/lib/api";
import type { SrcCell, SrcMatrixAsset, SrcMatrixCell, SrcTaskState } from "@/lib/types";

// 风险面短名 → 中文。后端 cell.title / gap.area_title 有值时优先用后端的，
// 这里只兜底（矩阵里老数据可能没带 title）。
const AREA_TITLES: Record<string, string> = {
  unauth: "未授权",
  authz: "越权/IDOR",
  injection: "注入",
  ssrf: "SSRF",
  xss: "XSS",
  rce: "RCE",
  cred: "凭证泄露",
  paths: "敏感路径",
  session: "会话/登录",
  upload: "上传",
  logic: "业务逻辑",
  host: "主机暴露",
  takeover: "子域接管",
};

function areaTitle(area: string): string {
  return AREA_TITLES[area] ?? area;
}

const CELL_STATES: SrcCell["state"][] = ["tested_found", "tested_clear", "na", "budget_stop"];

const CELL_STATE_LABELS: Record<SrcCell["state"], string> = {
  tested_found: "有洞（tested_found）",
  tested_clear: "已测（tested_clear）",
  na: "不适用（na）",
  budget_stop: "预算停（budget_stop）",
};

// 矩阵里空格没有 state，单独一种虚线样式——"从来没测过"和"测了不适用"是两件事。
function cellTone(state?: string): string {
  switch (state) {
    case "tested_found":
      return "border-red-500/40 bg-red-500/15 text-red-600 dark:text-red-400";
    case "tested_clear":
      return "border-emerald-500/40 bg-emerald-500/15 text-emerald-600 dark:text-emerald-400";
    case "na":
      return "border-border bg-muted text-muted-foreground";
    case "budget_stop":
      return "border-amber-500/40 bg-amber-500/15 text-amber-600 dark:text-amber-400";
    default:
      return "border-dashed border-border text-muted-foreground";
  }
}

function cellLabel(state?: string): string {
  switch (state) {
    case "tested_found":
      return "有洞";
    case "tested_clear":
      return "已测";
    case "na":
      return "不适用";
    case "budget_stop":
      return "预算停";
    default:
      return "未测";
  }
}

const GAP_KIND_LABELS: Record<string, string> = {
  blank: "未测",
  missing_reason: "缺原因",
  missing_evidence: "缺证据",
  missing_finding: "缺 finding",
  budget_stop: "未测完",
  unknown_asset_type: "未配置必做面",
};

function gapKindLabel(kind: string): string {
  return GAP_KIND_LABELS[kind] ?? kind;
}

function Stat({ label, value }: { label: string; value: number }) {
  return (
    <Card size="sm">
      <CardContent className="flex flex-col gap-0.5">
        <span className="text-muted-foreground text-xs">{label}</span>
        <span className="text-xl font-semibold tabular-nums">{value}</span>
      </CardContent>
    </Card>
  );
}

type MarkTarget = { asset: SrcMatrixAsset; cell: SrcMatrixCell };

export function SrcTab({ taskId }: { taskId: string }) {
  const [data, setData] = React.useState<SrcTaskState | null>(null);
  const [loading, setLoading] = React.useState(true);
  const [error, setError] = React.useState("");
  const [notice, setNotice] = React.useState("");

  const [target, setTarget] = React.useState<MarkTarget | null>(null);
  const [cellState, setCellState] = React.useState<SrcCell["state"] | "">("");
  const [cellReason, setCellReason] = React.useState("");
  const [evidenceNodeID, setEvidenceNodeID] = React.useState("");
  const [findingID, setFindingID] = React.useState("");
  const [markError, setMarkError] = React.useState("");
  const [saving, setSaving] = React.useState(false);

  const [forceOpen, setForceOpen] = React.useState(false);
  const [forceReason, setForceReason] = React.useState("");
  const [forceError, setForceError] = React.useState("");
  const [forcing, setForcing] = React.useState(false);

  const load = React.useCallback(async () => {
    setLoading(true);
    try {
      const next = await api.taskSrc(taskId);
      setData(next);
      setError("");
    } catch (e) {
      setError(String((e as Error).message || e));
    } finally {
      setLoading(false);
    }
  }, [taskId]);

  React.useEffect(() => {
    void load();
  }, [load]);

  const summary = data?.summary;
  const enabled = data?.enabled === true;
  const gapsTotal = summary?.gaps_total ?? data?.gaps_total ?? 0;
  const gaps = data?.gaps ?? [];
  const assets = data?.assets ?? [];
  const gapsByArea = summary?.gaps_by_area ?? data?.gaps_by_area ?? {};
  const finishable = data?.finishable ?? summary?.finishable ?? false;

  const openMark = (asset: SrcMatrixAsset, cell: SrcMatrixCell) => {
    setTarget({ asset, cell });
    setCellState(
      CELL_STATES.includes(cell.state as SrcCell["state"]) ? (cell.state as SrcCell["state"]) : "",
    );
    setCellReason(cell.reason ?? "");
    setEvidenceNodeID(cell.evidence_node_id ? String(cell.evidence_node_id) : "");
    setFindingID(cell.finding_id ? String(cell.finding_id) : "");
    setMarkError("");
  };

  const submitMark = async () => {
    if (!target || !cellState) return;
    setSaving(true);
    setMarkError("");
    try {
      await api.markSrcCell(taskId, {
        asset_id: target.asset.asset_id,
        area: target.cell.area,
        item: target.cell.item,
        state: cellState,
        reason: cellReason.trim() || undefined,
        evidence_node_id: Number(evidenceNodeID) > 0 ? Number(evidenceNodeID) : undefined,
        finding_id: Number(findingID) > 0 ? Number(findingID) : undefined,
      });
      setTarget(null);
      setNotice("");
      toast.success("覆盖格已更新");
      await load();
    } catch (e) {
      setMarkError(String((e as Error).message || e));
    } finally {
      setSaving(false);
    }
  };

  const submitForce = async () => {
    const reason = forceReason.trim();
    if (!reason) return;
    setForcing(true);
    setForceError("");
    try {
      const result = await api.forceFinishSrc(taskId, reason);
      setForceOpen(false);
      setForceReason("");
      setNotice(`已人工放行收尾（缺口 ${result.gaps_total ?? 0} 个）`);
      await load();
    } catch (e) {
      setForceError(String((e as Error).message || e));
    } finally {
      setForcing(false);
    }
  };

  return (
    <div className="flex flex-col gap-3">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <div className="flex min-w-0 flex-wrap items-center gap-2">
          <h2 className="text-sm font-semibold">SRC 覆盖矩阵</h2>
          {enabled ? (
            <Badge
              variant="outline"
              className={
                finishable
                  ? "border-emerald-500/40 bg-emerald-500/15 text-emerald-700 dark:text-emerald-400"
                  : "border-amber-500/40 bg-amber-500/15 text-amber-700 dark:text-amber-400"
              }
            >
              {finishable ? "可收尾" : "缺口未清零"}
            </Badge>
          ) : null}
          {enabled && !finishable && summary?.blocked_reason ? (
            <span className="text-muted-foreground min-w-0 text-xs">{summary.blocked_reason}</span>
          ) : null}
        </div>
        <div className="flex items-center gap-2">
          {enabled && !finishable ? (
            <Button size="sm" variant="outline" disabled={loading} onClick={() => setForceOpen(true)}>
              <ShieldAlertIcon /> 人工放行
            </Button>
          ) : null}
          <Button size="sm" variant="outline" disabled={loading} onClick={() => void load()}>
            <RefreshCwIcon className={loading ? "animate-spin" : ""} /> 刷新
          </Button>
        </div>
      </div>

      {error ? (
        <Alert variant="destructive">
          <AlertDescription>
            读取 SRC 覆盖状态失败：{error}
            <Button className="ml-2" size="sm" variant="outline" onClick={() => void load()}>
              重试
            </Button>
          </AlertDescription>
        </Alert>
      ) : null}

      {notice ? (
        <Alert>
          <CheckCircle2Icon />
          <AlertDescription>{notice}</AlertDescription>
        </Alert>
      ) : null}

      {data === null && loading ? (
        <div className="grid grid-cols-2 gap-2 sm:grid-cols-3 xl:grid-cols-5">
          {["gaps", "cells", "terminal", "measured", "assets"].map((key) => (
            <Skeleton key={key} className="h-16 w-full" />
          ))}
        </div>
      ) : null}

      {data && !enabled ? (
        <Card>
          <CardContent className="text-muted-foreground py-10 text-center text-sm">
            该任务不是 SRC 挖掘模式（创建时 mode≠src），没有覆盖矩阵。
          </CardContent>
        </Card>
      ) : null}

      {data && enabled ? (
        <>
          <div className="grid grid-cols-2 gap-2 sm:grid-cols-3 xl:grid-cols-5">
            <Stat label="缺口" value={gapsTotal} />
            <Stat label="应测格" value={summary?.cells_total ?? 0} />
            <Stat label="已终态" value={summary?.cells_terminal ?? 0} />
            <Stat label="已实测" value={summary?.cells_measured ?? 0} />
            <Stat label="资产" value={summary?.assets ?? 0} />
          </div>

          {Object.keys(gapsByArea).length > 0 ? (
            <div className="flex flex-wrap items-center gap-1.5">
              <span className="text-muted-foreground text-xs">缺口分布</span>
              {Object.entries(gapsByArea)
                .sort(([areaA, countA], [areaB, countB]) => countB - countA || areaA.localeCompare(areaB))
                .map(([area, count]) => (
                  <Badge key={area} variant="outline" title={areaTitle(area)}>
                    {areaTitle(area)} ×{count}
                  </Badge>
                ))}
            </div>
          ) : null}

          <Card className="gap-0 py-0">
            <CardHeader className="border-b py-3">
              <CardTitle className="flex flex-wrap items-center gap-2 text-sm">
                覆盖矩阵
                <span className="text-muted-foreground text-xs font-normal">点任意格可人工改判</span>
              </CardTitle>
            </CardHeader>
            <CardContent className="p-0">
              {assets.length === 0 ? (
                <Empty className="py-8">
                  <EmptyHeader>
                    <EmptyTitle>暂无参与判定的资产</EmptyTitle>
                    <EmptyDescription>任务范围内还没有站点类资产，所以没有可点的覆盖格。</EmptyDescription>
                  </EmptyHeader>
                </Empty>
              ) : (
                assets.map((asset) => (
                  <div key={asset.asset_id} className="border-b p-3 last:border-b-0">
                    <div className="mb-2 flex flex-wrap items-center gap-2">
                      <code
                        className="bg-muted max-w-[24rem] truncate rounded px-1.5 py-0.5 font-mono text-xs"
                        title={asset.asset}
                      >
                        {asset.asset}
                      </code>
                      <Badge variant="outline">{asset.asset_type}</Badge>
                      <span className="text-muted-foreground text-xs">
                        必做 {(asset.required_areas ?? []).length} 面
                      </span>
                    </div>
                    <div className="flex flex-wrap gap-1.5">
                      {(asset.cells ?? []).map((cell) => (
                        <button
                          key={`${cell.area}/${cell.item}`}
                          type="button"
                          title={`${cell.title ?? areaTitle(cell.area)} · ${cellLabel(cell.state)}`}
                          onClick={() => openMark(asset, cell)}
                          className={`inline-flex items-center gap-1 rounded-full border px-2 py-0.5 text-xs transition-colors hover:opacity-80 ${cellTone(
                            cell.state,
                          )}`}
                        >
                          <span>{cell.area}</span>
                          {cell.item !== "*" ? <span className="opacity-70">/{cell.item}</span> : null}
                          <span className="font-medium">{cellLabel(cell.state)}</span>
                        </button>
                      ))}
                      {(asset.cells ?? []).length === 0 ? (
                        <span className="text-muted-foreground text-xs">该资产类型没有配置必做面</span>
                      ) : null}
                    </div>
                  </div>
                ))
              )}
            </CardContent>
          </Card>

          <Card className="gap-0 overflow-hidden py-0">
            <CardHeader className="border-b py-3">
              <CardTitle className="flex flex-wrap items-center gap-2 text-sm">
                缺口
                <span className="text-muted-foreground text-xs font-normal">
                  按优先级排序，补格清单以矩阵为准
                </span>
              </CardTitle>
            </CardHeader>
            <CardContent className="px-0">
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead>资产</TableHead>
                    <TableHead>风险面</TableHead>
                    <TableHead>细分项</TableHead>
                    <TableHead>缺口</TableHead>
                    <TableHead>说明</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {gaps.map((gap) => (
                    <TableRow key={`${gap.asset_id}-${gap.area}-${gap.item}-${gap.kind}`}>
                      <TableCell className="max-w-64">
                        <div className="flex min-w-0 flex-col gap-0.5">
                          <code className="truncate font-mono text-xs" title={gap.asset}>
                            {gap.asset}
                          </code>
                          <span className="text-muted-foreground text-xs">{gap.asset_type}</span>
                        </div>
                      </TableCell>
                      <TableCell className="text-xs">
                        <span title={gap.area}>{gap.area_title ?? areaTitle(gap.area)}</span>
                      </TableCell>
                      <TableCell className="font-mono text-xs">{gap.item === "*" ? "整组" : gap.item}</TableCell>
                      <TableCell>
                        <Badge variant="outline">{gapKindLabel(gap.kind)}</Badge>
                      </TableCell>
                      <TableCell className="text-muted-foreground max-w-md text-xs [overflow-wrap:anywhere]">
                        {gap.reason ?? "—"}
                      </TableCell>
                    </TableRow>
                  ))}
                  {gaps.length === 0 ? (
                    <TableRow>
                      <TableCell className="text-muted-foreground py-8 text-center text-sm" colSpan={5}>
                        收尾门已清零，没有缺口。
                      </TableCell>
                    </TableRow>
                  ) : null}
                </TableBody>
              </Table>
            </CardContent>
            {gapsTotal > gaps.length ? (
              <div className="text-muted-foreground border-t px-3 py-2 text-xs">
                仅列出优先级最高的 {gaps.length} 条，共 {gapsTotal} 条。
              </div>
            ) : null}
          </Card>
        </>
      ) : null}

      <Dialog
        open={target !== null}
        onOpenChange={(open) => {
          if (!open) setTarget(null);
        }}
      >
        <DialogContent className="sm:max-w-lg">
          <DialogHeader>
            <DialogTitle>人工点格</DialogTitle>
            <DialogDescription>
              {target
                ? `${target.asset.asset} · ${areaTitle(target.cell.area)}（${target.cell.area}${
                    target.cell.item !== "*" ? `/${target.cell.item}` : ""
                  }）`
                : ""}
            </DialogDescription>
          </DialogHeader>
          <div className="grid gap-4">
            <div className="grid gap-2">
              <Label htmlFor="src-cell-state">状态</Label>
              <Select
                value={cellState}
                onValueChange={(next) => setCellState(next as SrcCell["state"])}
              >
                <SelectTrigger id="src-cell-state" className="w-full">
                  <SelectValue placeholder="选择终态" />
                </SelectTrigger>
                <SelectContent>
                  <SelectGroup>
                    {CELL_STATES.map((state) => (
                      <SelectItem key={state} value={state}>
                        {CELL_STATE_LABELS[state]}
                      </SelectItem>
                    ))}
                  </SelectGroup>
                </SelectContent>
              </Select>
            </div>
            <div className="grid gap-2">
              <Label htmlFor="src-cell-reason">原因</Label>
              <Textarea
                id="src-cell-reason"
                className="min-h-24"
                value={cellReason}
                placeholder="na / budget_stop 必填；tested_clear 没有证据节点时也要写清为什么算测过"
                onChange={(e) => setCellReason(e.target.value)}
              />
            </div>
            <div className="grid gap-4 sm:grid-cols-2">
              <div className="grid gap-2">
                <Label htmlFor="src-cell-evidence">证据节点 ID（可选）</Label>
                <Input
                  id="src-cell-evidence"
                  type="number"
                  min={0}
                  placeholder="evidence_node_id"
                  value={evidenceNodeID}
                  onChange={(e) => setEvidenceNodeID(e.target.value)}
                />
              </div>
              <div className="grid gap-2">
                <Label htmlFor="src-cell-finding">漏洞 ID（可选）</Label>
                <Input
                  id="src-cell-finding"
                  type="number"
                  min={0}
                  placeholder="finding_id"
                  value={findingID}
                  onChange={(e) => setFindingID(e.target.value)}
                />
              </div>
            </div>
            {markError ? <p className="text-destructive text-xs [overflow-wrap:anywhere]">{markError}</p> : null}
          </div>
          <DialogFooter>
            <Button variant="outline" onClick={() => setTarget(null)}>
              取消
            </Button>
            <Button disabled={saving || !cellState} onClick={() => void submitMark()}>
              {saving && <Spinner data-icon="inline-start" />}
              保存
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      <Dialog open={forceOpen} onOpenChange={setForceOpen}>
        <DialogContent className="sm:max-w-md">
          <DialogHeader>
            <DialogTitle>人工放行收尾</DialogTitle>
            <DialogDescription>
              缺口未清零时强行把任务置为 done。理由会写进活动审计；缺口明细不会因此消失。
            </DialogDescription>
          </DialogHeader>
          <div className="grid gap-2">
            <Label htmlFor="src-force-reason">理由</Label>
            <Textarea
              id="src-force-reason"
              className="min-h-24"
              value={forceReason}
              placeholder="例如：剩余缺口属客户明确不测的范围，评审已确认"
              onChange={(e) => setForceReason(e.target.value)}
            />
            {forceError ? <p className="text-destructive text-xs [overflow-wrap:anywhere]">{forceError}</p> : null}
          </div>
          <DialogFooter>
            <Button variant="outline" onClick={() => setForceOpen(false)}>
              取消
            </Button>
            <Button disabled={forcing || !forceReason.trim()} onClick={() => void submitForce()}>
              {forcing && <Spinner data-icon="inline-start" />}
              确认放行
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  );
}
