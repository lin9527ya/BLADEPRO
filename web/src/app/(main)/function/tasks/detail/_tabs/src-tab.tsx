"use client";

import * as React from "react";

import { CheckCircle2Icon, FilePlus2Icon, RefreshCwIcon, ShieldAlertIcon } from "lucide-react";
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
import type {
  SrcCell,
  SrcCheckAsset,
  SrcCheckItem,
  SrcMatrixAsset,
  SrcMatrixCell,
  SrcSubmission,
  SrcTaskState,
} from "@/lib/types";

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
  // P2 交付面：不是站点风险面，而是"这条发现还没准备好可提交件"。
  delivery: "交付",
  // F1 收尾闸：缺口里的伪风险面（JS 抽全/路径枚举/方法矩阵…）。
  closure: "收尾闸",
};

// 台账状态 → 中文 + 下一步合法流转（与服务端状态机一致；UI 只是少给错选项，
// 真正的判定在服务端——两边口径以服务端为准）。
const SUBMISSION_STATUS_LABELS: Record<string, string> = {
  draft: "草稿",
  ready: "可提交",
  submitted: "已提交",
  accepted: "已受理",
  duplicate: "重复",
  rejected: "已驳回",
  paid: "已到账",
  withdrawn: "已撤回",
};

const SUBMISSION_NEXT: Record<string, string[]> = {
  draft: ["ready", "withdrawn"],
  ready: ["submitted", "draft", "withdrawn"],
  submitted: ["accepted", "duplicate", "rejected", "withdrawn"],
  accepted: ["paid", "withdrawn"],
  duplicate: ["withdrawn"],
  rejected: ["withdrawn"],
  paid: [],
  withdrawn: [],
};

function submissionTone(status: string): string {
  switch (status) {
    case "ready":
      return "border-sky-500/40 bg-sky-500/15 text-sky-600 dark:text-sky-400";
    case "submitted":
      return "border-amber-500/40 bg-amber-500/15 text-amber-600 dark:text-amber-400";
    case "accepted":
      return "border-emerald-500/40 bg-emerald-500/15 text-emerald-600 dark:text-emerald-400";
    case "paid":
      return "border-emerald-600/50 bg-emerald-600/20 text-emerald-700 dark:text-emerald-300";
    case "duplicate":
    case "rejected":
      return "border-border bg-muted text-muted-foreground";
    case "withdrawn":
      return "border-dashed border-border text-muted-foreground";
    default:
      return "border-border text-muted-foreground";
  }
}

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
  report_missing: "缺可提交件",
  duplicate: "疑似重复",
  missing_check: "收尾项未做",
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
  const [cellNaCode, setCellNaCode] = React.useState("");
  const [evidenceNodeID, setEvidenceNodeID] = React.useState("");
  const [findingID, setFindingID] = React.useState("");
  const [markError, setMarkError] = React.useState("");
  const [saving, setSaving] = React.useState(false);

  const [forceOpen, setForceOpen] = React.useState(false);
  const [forceReason, setForceReason] = React.useState("");
  const [forceError, setForceError] = React.useState("");
  const [forcing, setForcing] = React.useState(false);

  // P2 交付台账：改状态 + 准备交付件。
  const [ledgerTarget, setLedgerTarget] = React.useState<SrcSubmission | null>(null);
  const [ledgerStatus, setLedgerStatus] = React.useState("");
  const [ledgerExternalID, setLedgerExternalID] = React.useState("");
  const [ledgerReward, setLedgerReward] = React.useState("");
  const [ledgerError, setLedgerError] = React.useState("");
  const [ledgerSaving, setLedgerSaving] = React.useState(false);

  const [prepareOpen, setPrepareOpen] = React.useState(false);
  const [prepFindingID, setPrepFindingID] = React.useState("");
  const [prepDescription, setPrepDescription] = React.useState("");
  const [prepImpact, setPrepImpact] = React.useState("");
  const [prepFix, setPrepFix] = React.useState("");
  const [prepIssues, setPrepIssues] = React.useState<string[]>([]);
  const [prepNote, setPrepNote] = React.useState("");
  const [prepError, setPrepError] = React.useState("");
  const [prepSaving, setPrepSaving] = React.useState(false);

  // 收尾闸（F1）：一个资产 × 一项收尾。done 要数字+证据，na 要理由码。
  const [checkTarget, setCheckTarget] = React.useState<{ asset: SrcCheckAsset; item: SrcCheckItem } | null>(null);
  const [checkState, setCheckState] = React.useState<"done" | "na" | "budget_stop" | "">("");
  const [checkNACode, setCheckNACode] = React.useState("");
  const [checkReason, setCheckReason] = React.useState("");
  const [checkEvidence, setCheckEvidence] = React.useState("");
  const [checkMetrics, setCheckMetrics] = React.useState<Record<string, string>>({});
  const [checkError, setCheckError] = React.useState("");
  const [checkSaving, setCheckSaving] = React.useState(false);

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
  const checks = data?.checks ?? [];
  const gapsByArea = summary?.gaps_by_area ?? data?.gaps_by_area ?? {};
  const finishable = data?.finishable ?? summary?.finishable ?? false;

  const openMark = (asset: SrcMatrixAsset, cell: SrcMatrixCell) => {
    setTarget({ asset, cell });
    setCellState(
      CELL_STATES.includes(cell.state as SrcCell["state"]) ? (cell.state as SrcCell["state"]) : "",
    );
    setCellReason(cell.reason ?? "");
    setCellNaCode(cell.na_code ?? "");
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
        na_code: cellState === "na" ? cellNaCode.trim() || undefined : undefined,
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

  const openCheck = (asset: SrcCheckAsset, item: SrcCheckItem) => {
    setCheckTarget({ asset, item });
    setCheckState(item.state ?? "");
    setCheckNACode(item.na_code ?? "");
    setCheckReason(item.reason ?? "");
    setCheckEvidence(item.evidence_node_id ? String(item.evidence_node_id) : "");
    const metrics: Record<string, string> = {};
    for (const f of item.metrics_fields ?? []) {
      const v = item.metrics?.[f.key];
      metrics[f.key] = v === undefined || v === null ? "" : String(v);
    }
    setCheckMetrics(metrics);
    setCheckError("");
  };

  const submitCheck = async () => {
    if (!checkTarget || !checkState) return;
    setCheckSaving(true);
    setCheckError("");
    try {
      const metrics: Record<string, number> = {};
      for (const [k, v] of Object.entries(checkMetrics)) {
        const n = Number(v);
        if (Number.isFinite(n) && n > 0) metrics[k] = Math.floor(n);
      }
      await api.markSrcCheck(taskId, {
        asset_id: checkTarget.asset.asset_id,
        check_key: checkTarget.item.key,
        state: checkState,
        na_code: checkState === "na" ? checkNACode.trim() || undefined : undefined,
        reason: checkReason.trim() || undefined,
        metrics: Object.keys(metrics).length > 0 ? metrics : undefined,
        evidence_node_id: Number(checkEvidence) > 0 ? Number(checkEvidence) : undefined,
      });
      setCheckTarget(null);
      toast.success("收尾项已更新");
      await load();
    } catch (e) {
      setCheckError(String((e as Error).message || e));
    } finally {
      setCheckSaving(false);
    }
  };

  const submitForce = async () => {    const reason = forceReason.trim();
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

  // ---- P2 交付台账 ----
  const openLedger = (sub: SrcSubmission) => {
    setLedgerTarget(sub);
    setLedgerStatus(sub.status);
    setLedgerExternalID(sub.external_id ?? "");
    setLedgerReward(sub.reward ?? "");
    setLedgerError("");
  };

  const submitLedger = async () => {
    if (!ledgerTarget) return;
    setLedgerSaving(true);
    setLedgerError("");
    try {
      await api.patchSrcSubmission(ledgerTarget.id, {
        status: ledgerStatus || undefined,
        external_id: ledgerExternalID.trim(),
        reward: ledgerReward.trim(),
      });
      setLedgerTarget(null);
      toast.success("台账已更新");
      await load();
    } catch (e) {
      setLedgerError(String((e as Error).message || e));
    } finally {
      setLedgerSaving(false);
    }
  };

  const submitPrepare = async () => {
    const findingID = Number(prepFindingID);
    if (!Number.isFinite(findingID) || findingID <= 0) {
      setPrepError("请填正确的发现 id");
      return;
    }
    setPrepSaving(true);
    setPrepError("");
    setPrepIssues([]);
    setPrepNote("");
    try {
      const result = await api.prepareSrcSubmission(findingID, {
        description: prepDescription.trim() || undefined,
        impact: prepImpact.trim() || undefined,
        fix: prepFix.trim() || undefined,
      });
      setPrepIssues(result.issues ?? []);
      setPrepNote(result.note ?? "");
      if (result.report_ready) {
        toast.success("可提交件已齐备（status=ready）");
      }
      await load();
    } catch (e) {
      setPrepError(String((e as Error).message || e));
    } finally {
      setPrepSaving(false);
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
          {enabled ? (
            <Button
              size="sm"
              variant="outline"
              onClick={() => {
                setPrepareOpen(true);
                setPrepIssues([]);
                setPrepNote("");
                setPrepError("");
              }}
            >
              <FilePlus2Icon /> 准备交付件
            </Button>
          ) : null}
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

          {/* 交付面：格清零 ≠ 能交，中危以上每条都要有齐备的可提交件。 */}
          <div className="grid grid-cols-2 gap-2 sm:grid-cols-4">
            <Stat label="中危+ 发现" value={summary?.findings_medium_plus ?? 0} />
            <Stat label="报告齐备" value={summary?.reports_ready ?? 0} />
            <Stat label="台账" value={(data.submissions ?? []).length} />
            <Stat
              label="待交付"
              value={Math.max(
                0,
                (summary?.findings_medium_plus ?? 0) - (summary?.reports_ready ?? 0),
              )}
            />
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

          <Card className="gap-0 py-0">
            <CardHeader className="border-b py-3">
              <CardTitle className="flex flex-wrap items-center gap-2 text-sm">
                收尾闸
                <span className="text-muted-foreground text-xs font-normal">
                  面之外的第二层：JS 抽全 / 路径枚举 / 非 404 复测 / 方法矩阵 / 参数变体 / 防护对照 / 判死三条件 / 截图
                </span>
              </CardTitle>
            </CardHeader>
            <CardContent className="p-0">
              {checks.length === 0 ? (
                <Empty className="py-8">
                  <EmptyHeader>
                    <EmptyTitle>没有收尾项</EmptyTitle>
                    <EmptyDescription>
                      该任务没有站点面资产，或模式快照关掉了收尾闸（check_tier=off / skip_check_gate）。
                    </EmptyDescription>
                  </EmptyHeader>
                </Empty>
              ) : (
                checks.map((row) => (
                  <div key={row.asset_id} className="border-b p-3 last:border-b-0">
                    <div className="mb-2 flex flex-wrap items-center gap-2">
                      <code
                        className="bg-muted max-w-[24rem] truncate rounded px-1.5 py-0.5 font-mono text-xs"
                        title={row.asset}
                      >
                        {row.asset}
                      </code>
                      <Badge variant="outline">{row.asset_type}</Badge>
                      <span className="text-muted-foreground text-xs">
                        已终态 {(row.items ?? []).filter((it) => it.state).length}/{row.items.length}
                      </span>
                    </div>
                    <div className="flex flex-wrap gap-1.5">
                      {[...(row.items ?? [])]
                        .sort((a, b) => Number(Boolean(a.state)) - Number(Boolean(b.state)))
                        .map((item) => (
                        <button
                          key={item.key}
                          type="button"
                          title={`${item.hint}`}
                          onClick={() => openCheck(row, item)}
                          className={`inline-flex items-center gap-1 rounded-full border px-2 py-0.5 text-xs transition-colors hover:opacity-80 ${cellTone(
                            item.state === "done" ? "tested_clear" : item.state === "na" ? "na" : undefined,
                          )}`}
                        >
                          <span>{item.title}</span>
                          <span className="font-medium">
                            {item.state === "done" ? "已做" : item.state === "na" ? "N/A" : item.state === "budget_stop" ? "预算停" : "未做"}
                          </span>
                        </button>
                      ))}
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
                    <TableRow key={`${gap.asset_id}-${gap.finding_id ?? 0}-${gap.area}-${gap.item}-${gap.kind}`}>
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

          {/* P2 提交台账：交付物不只是报告，还有"交到哪一步"的账。 */}
          <Card className="gap-0 overflow-hidden py-0">
            <CardHeader className="border-b py-3">
              <CardTitle className="flex flex-wrap items-center gap-2 text-sm">
                提交台账
                <span className="text-muted-foreground text-xs font-normal">
                  一条发现 × 一个平台；标记「已提交」必须带平台编号
                </span>
              </CardTitle>
            </CardHeader>
            <CardContent className="px-0">
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead>发现</TableHead>
                    <TableHead>等级</TableHead>
                    <TableHead>平台</TableHead>
                    <TableHead>状态</TableHead>
                    <TableHead>平台编号</TableHead>
                    <TableHead>回报</TableHead>
                    <TableHead className="text-right">操作</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {(data.submissions ?? []).map((sub) => (
                    <TableRow key={sub.id}>
                      <TableCell className="max-w-72">
                        <div className="flex min-w-0 flex-col gap-0.5">
                          <span className="truncate text-xs" title={sub.title}>
                            #{sub.finding_id} {sub.title}
                          </span>
                          {sub.fingerprint ? (
                            <code className="text-muted-foreground font-mono text-[10px]">
                              {sub.fingerprint}
                            </code>
                          ) : null}
                        </div>
                      </TableCell>
                      <TableCell className="text-xs">{sub.severity || "—"}</TableCell>
                      <TableCell className="text-xs">{sub.platform || "本任务"}</TableCell>
                      <TableCell>
                        <span
                          className={`inline-flex rounded-full border px-2 py-0.5 text-xs ${submissionTone(
                            sub.status,
                          )}`}
                        >
                          {SUBMISSION_STATUS_LABELS[sub.status] ?? sub.status}
                        </span>
                      </TableCell>
                      <TableCell className="font-mono text-xs">{sub.external_id || "—"}</TableCell>
                      <TableCell className="text-xs">{sub.reward || "—"}</TableCell>
                      <TableCell className="text-right">
                        <Button size="sm" variant="ghost" onClick={() => openLedger(sub)}>
                          改状态
                        </Button>
                      </TableCell>
                    </TableRow>
                  ))}
                  {(data.submissions ?? []).length === 0 ? (
                    <TableRow>
                      <TableCell className="text-muted-foreground py-8 text-center text-sm" colSpan={7}>
                        还没有台账。先「准备交付件」，把中危以上发现的描述/危害/修复写全并过证据合规。
                      </TableCell>
                    </TableRow>
                  ) : null}
                </TableBody>
              </Table>
            </CardContent>
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
            {cellState === "na" ? (
              <div className="grid gap-2">
                <Label htmlFor="src-cell-na">N/A 理由码（na 必填）</Label>
                <Select value={cellNaCode} onValueChange={setCellNaCode}>
                  <SelectTrigger id="src-cell-na" className="w-full">
                    <SelectValue placeholder="选一个：没这个面 ≠ 没测" />
                  </SelectTrigger>
                  <SelectContent>
                    <SelectGroup>
                      {NA_CODE_LABELS.map(([code, label]) => (
                        <SelectItem key={code} value={code}>
                          {label}（{code}）
                        </SelectItem>
                      ))}
                    </SelectGroup>
                  </SelectContent>
                </Select>
              </div>
            ) : null}
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
      <Dialog
        open={ledgerTarget !== null}
        onOpenChange={(open) => {
          if (!open) setLedgerTarget(null);
        }}
      >
        <DialogContent className="sm:max-w-lg">
          <DialogHeader>
            <DialogTitle>更新提交台账</DialogTitle>
            <DialogDescription>
              {ledgerTarget ? `#${ledgerTarget.finding_id} ${ledgerTarget.title}` : ""}
            </DialogDescription>
          </DialogHeader>
          <div className="grid gap-4">
            <div className="grid gap-2">
              <Label htmlFor="src-ledger-status">状态</Label>
              <Select value={ledgerStatus} onValueChange={setLedgerStatus}>
                <SelectTrigger id="src-ledger-status" className="w-full">
                  <SelectValue placeholder="选择状态" />
                </SelectTrigger>
                <SelectContent>
                  <SelectGroup>
                    {ledgerTarget ? (
                      <SelectItem value={ledgerTarget.status}>
                        {SUBMISSION_STATUS_LABELS[ledgerTarget.status] ?? ledgerTarget.status}（不变）
                      </SelectItem>
                    ) : null}
                    {(SUBMISSION_NEXT[ledgerTarget?.status ?? ""] ?? []).map((next) => (
                      <SelectItem key={next} value={next}>
                        {SUBMISSION_STATUS_LABELS[next] ?? next}
                      </SelectItem>
                    ))}
                  </SelectGroup>
                </SelectContent>
              </Select>
            </div>
            <div className="grid gap-2">
              <Label htmlFor="src-ledger-external">平台编号 / 提交凭证</Label>
              <Input
                id="src-ledger-external"
                value={ledgerExternalID}
                placeholder="标记为已提交时必填，例如 SRC-2026-0001"
                onChange={(e) => setLedgerExternalID(e.target.value)}
              />
            </div>
            <div className="grid gap-2">
              <Label htmlFor="src-ledger-reward">回报</Label>
              <Input
                id="src-ledger-reward"
                value={ledgerReward}
                placeholder="例如 200 积分 / 500 元"
                onChange={(e) => setLedgerReward(e.target.value)}
              />
            </div>
            {ledgerError ? (
              <p className="text-destructive text-xs [overflow-wrap:anywhere]">{ledgerError}</p>
            ) : null}
          </div>
          <DialogFooter>
            <Button variant="outline" onClick={() => setLedgerTarget(null)}>
              取消
            </Button>
            <Button disabled={ledgerSaving} onClick={() => void submitLedger()}>
              {ledgerSaving && <Spinner data-icon="inline-start" />}
              保存
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      <Dialog open={checkTarget !== null} onOpenChange={(open) => (open ? null : setCheckTarget(null))}>
        <DialogContent className="sm:max-w-lg">
          <DialogHeader>
            <DialogTitle>{checkTarget?.item.title ?? "收尾项"}</DialogTitle>
            <DialogDescription>
              {checkTarget?.item.hint}
              {checkTarget?.item.na_codes && Object.keys(checkTarget.item.na_codes).length > 0
                ? " · 可标 N/A（需理由码）"
                : " · 本项不接受 N/A，必须实际做完"}
            </DialogDescription>
          </DialogHeader>
          <div className="grid gap-4">
            <div className="grid gap-2">
              <Label htmlFor="src-check-state">终态</Label>
              <Select
                value={checkState}
                onValueChange={(v) => setCheckState(v as "done" | "na" | "budget_stop")}
              >
                <SelectTrigger id="src-check-state" className="w-full">
                  <SelectValue placeholder="选择终态" />
                </SelectTrigger>
                <SelectContent>
                  <SelectGroup>
                    <SelectItem value="done">done（做完，需数字与证据节点）</SelectItem>
                    <SelectItem value="na">na（不适用，需理由码）</SelectItem>
                    <SelectItem value="budget_stop">budget_stop（没测完）</SelectItem>
                  </SelectGroup>
                </SelectContent>
              </Select>
            </div>
            {checkState === "done" && (checkTarget?.item.metrics_fields ?? []).length > 0 ? (
              <div className="grid gap-2 sm:grid-cols-2">
                {(checkTarget?.item.metrics_fields ?? []).map((f) => (
                  <div key={f.key} className="grid gap-1">
                    <Label htmlFor={`src-metric-${f.key}`} className="text-xs">
                      {f.label}
                      {f.min ? <span className="text-muted-foreground">（门槛 ≥{f.min}）</span> : null}
                    </Label>
                    <Input
                      id={`src-metric-${f.key}`}
                      type="number"
                      min={0}
                      value={checkMetrics[f.key] ?? ""}
                      onChange={(e) => setCheckMetrics((prev) => ({ ...prev, [f.key]: e.target.value }))}
                    />
                  </div>
                ))}
              </div>
            ) : null}
            {checkState === "na" ? (
              <div className="grid gap-2">
                <Label htmlFor="src-check-na">N/A 理由码</Label>
                <Select value={checkNACode} onValueChange={setCheckNACode}>
                  <SelectTrigger id="src-check-na" className="w-full">
                    <SelectValue placeholder="选择理由码" />
                  </SelectTrigger>
                  <SelectContent>
                    <SelectGroup>
                      {Object.entries(checkTarget?.item.na_codes ?? {}).map(([code, label]) => (
                        <SelectItem key={code} value={code}>
                          {label}（{code}）
                        </SelectItem>
                      ))}
                    </SelectGroup>
                  </SelectContent>
                </Select>
              </div>
            ) : null}
            <div className="grid gap-2">
              <Label htmlFor="src-check-reason">说明</Label>
              <Input
                id="src-check-reason"
                value={checkReason}
                placeholder="例如：路径枚举 214 条；非 404 复测 6 条"
                onChange={(e) => setCheckReason(e.target.value)}
              />
            </div>
            <div className="grid gap-2">
              <Label htmlFor="src-check-evidence">证据节点 id（done 必填）</Label>
              <Input
                id="src-check-evidence"
                type="number"
                min={1}
                value={checkEvidence}
                placeholder="record_fact 返回的节点 id"
                onChange={(e) => setCheckEvidence(e.target.value)}
              />
            </div>
            {checkError ? (
              <p className="text-destructive text-xs [overflow-wrap:anywhere]">{checkError}</p>
            ) : null}
          </div>
          <DialogFooter>
            <Button variant="outline" onClick={() => setCheckTarget(null)}>
              取消
            </Button>
            <Button disabled={checkSaving || !checkState} onClick={() => void submitCheck()}>
              {checkSaving && <Spinner data-icon="inline-start" />}
              保存
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      <Dialog open={prepareOpen} onOpenChange={setPrepareOpen}>
        <DialogContent className="sm:max-w-2xl">
          <DialogHeader>
            <DialogTitle>准备交付件</DialogTitle>
            <DialogDescription>
              服务端会当场校验：正文齐备（不许占位符）+ 证据合规（proof 绑定、包长、请求 id、授权范围）
              + 跨任务指纹查重。issues 非空时台账落 draft。
            </DialogDescription>
          </DialogHeader>
          <div className="grid gap-4">
            <div className="grid gap-2">
              <Label htmlFor="src-prep-finding">发现 ID</Label>
              <Input
                id="src-prep-finding"
                type="number"
                min={1}
                value={prepFindingID}
                placeholder="report_finding / list_findings 返回的 id"
                onChange={(e) => setPrepFindingID(e.target.value)}
              />
            </div>
            <div className="grid gap-2">
              <Label htmlFor="src-prep-desc">漏洞描述</Label>
              <Textarea
                id="src-prep-desc"
                className="min-h-20"
                value={prepDescription}
                placeholder="第一人称讲做成了什么（留空则用该发现的报告/摘要）"
                onChange={(e) => setPrepDescription(e.target.value)}
              />
            </div>
            <div className="grid gap-4 sm:grid-cols-2">
              <div className="grid gap-2">
                <Label htmlFor="src-prep-impact">漏洞危害</Label>
                <Textarea
                  id="src-prep-impact"
                  className="min-h-20"
                  value={prepImpact}
                  placeholder="每种伤害单独一行，写最终伤害"
                  onChange={(e) => setPrepImpact(e.target.value)}
                />
              </div>
              <div className="grid gap-2">
                <Label htmlFor="src-prep-fix">修复建议</Label>
                <Textarea
                  id="src-prep-fix"
                  className="min-h-20"
                  value={prepFix}
                  placeholder="贴合本漏洞、可落地"
                  onChange={(e) => setPrepFix(e.target.value)}
                />
              </div>
            </div>
            {prepIssues.length > 0 ? (
              <div className="rounded-md border border-amber-500/40 bg-amber-500/10 p-3">
                <p className="mb-1 text-xs font-medium">还不能交（{prepIssues.length} 项）：</p>
                <ul className="text-muted-foreground list-disc space-y-0.5 pl-4 text-xs [overflow-wrap:anywhere]">
                  {prepIssues.map((issue) => (
                    <li key={issue}>{issue}</li>
                  ))}
                </ul>
              </div>
            ) : null}
            {prepNote ? <p className="text-muted-foreground text-xs [overflow-wrap:anywhere]">{prepNote}</p> : null}
            {prepError ? <p className="text-destructive text-xs [overflow-wrap:anywhere]">{prepError}</p> : null}
          </div>
          <DialogFooter>
            <Button variant="outline" onClick={() => setPrepareOpen(false)}>
              关闭
            </Button>
            <Button disabled={prepSaving || !prepFindingID.trim()} onClick={() => void submitPrepare()}>
              {prepSaving && <Spinner data-icon="inline-start" />}
              准备 / 重新校验
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  );
}

// N/A 理由码的中文说明（后端 srcmode 的白名单同名；这里只做 UI 展示）。
const NA_CODE_LABELS: Array<[string, string]> = [
  ["no_input", "无业务参数"],
  ["no_url_param", "无 URL 拉取/代理参数"],
  ["no_reflect", "无回显点"],
  ["no_writer", "无用户可控写入点"],
  ["no_exec_sink", "无执行 sink"],
  ["no_upload_entry", "无上传入口"],
  ["no_object", "抽不出业务对象"],
  ["no_login_entry", "无发会话/重置/改绑口"],
  ["static_page", "纯静态页"],
  ["platform_exempt", "平台档案明确不测"],
  ["out_of_scope", "越界资产"],
];