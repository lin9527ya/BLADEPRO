"use client";

import * as React from "react";

import { FilterIcon, TrendingDownIcon } from "lucide-react";

import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Skeleton } from "@/components/ui/skeleton";
import { api } from "@/lib/api";
import type { SrcFunnel } from "@/lib/types";
import { cn } from "@/lib/utils";

// 全局 SRC 漏斗：资产 → 在范围 → 有终态 → 中危+发现 → 报告齐备 → 已提交 → 受理/到账。
// 数字直接来自 task_scope / src_cells / findings / src_submissions / llm_usage。

const STAGE_TONE = [
  "bg-muted-foreground/25",
  "bg-sky-500/50",
  "bg-cyan-500/55",
  "bg-emerald-500/55",
  "bg-teal-500/55",
  "bg-indigo-500/55",
  "bg-violet-500/55",
  "bg-amber-500/60",
  "bg-orange-500/60",
];

function fmtN(n: number): string {
  if (n >= 1_000_000) return (n / 1_000_000).toFixed(1) + "M";
  if (n >= 10_000) return (n / 1000).toFixed(1) + "k";
  return String(n);
}

function fmtCost(usd: number): string {
  if (!usd || !Number.isFinite(usd)) return "—";
  if (usd < 0.01) return "$" + usd.toFixed(4);
  if (usd < 1) return "$" + usd.toFixed(3);
  return "$" + usd.toFixed(2);
}

const SEVERITY_ORDER = ["critical", "high", "medium", "low", "unknown"];

function fmtMinutes(minutes: number): string {
  if (!minutes || !Number.isFinite(minutes)) return "—";
  if (minutes < 60) return `${Math.round(minutes)}m`;
  return `${(minutes / 60).toFixed(1)}h`;
}

function fmtHours(hours: number): string {
  if (!hours || !Number.isFinite(hours)) return "—";
  if (hours < 48) return `${hours.toFixed(1)}h`;
  return `${(hours / 24).toFixed(1)}d`;
}

export function RowSrcFunnel() {
  const [days, setDays] = React.useState(30);
  const [data, setData] = React.useState<SrcFunnel | null>(null);
  const [loading, setLoading] = React.useState(true);
  const [error, setError] = React.useState("");

  React.useEffect(() => {
    let alive = true;
    setLoading(true);
    setError("");
    api
      .srcFunnel({ days })
      .then((r) => {
        if (alive) setData(r);
      })
      .catch((e: Error) => {
        if (alive) setError(e.message);
      })
      .finally(() => {
        if (alive) setLoading(false);
      });
    return () => {
      alive = false;
    };
  }, [days]);

  const stages = data?.stages ?? [];
  const max = Math.max(1, ...stages.map((s) => s.value));
  const severityRows = [...(data?.by_severity ?? [])].sort(
    (a, b) => SEVERITY_ORDER.indexOf(a.severity) - SEVERITY_ORDER.indexOf(b.severity),
  );

  return (
    <Card>
      <CardHeader className="flex-row flex-wrap items-start justify-between gap-3">
        <div className="grid gap-1">
          <CardTitle className="flex items-center gap-2 text-base">
            <FilterIcon className="size-4 text-emerald-500" /> SRC 转化漏斗
            <span className="text-muted-foreground text-xs font-normal">近 {data?.days ?? days} 天</span>
          </CardTitle>
          <CardDescription>
            资产 → 在范围 → 有终态格 → 中危+发现 → 报告齐备 → 已提交 → 受理/到账；成本按调用时单价归因。
          </CardDescription>
        </div>
        <div className="flex items-center gap-1 rounded-md border p-0.5">
          {[7, 30, 90].map((d) => (
            <Button
              key={d}
              size="sm"
              variant={days === d ? "secondary" : "ghost"}
              className="h-7 px-2 text-xs"
              onClick={() => setDays(d)}
            >
              {d}天
            </Button>
          ))}
        </div>
      </CardHeader>
      <CardContent className="grid gap-4">
        {loading && !data ? (
          <div className="grid gap-2">
            {[0, 1, 2, 3, 4].map((i) => (
              <Skeleton key={i} className="h-8 w-full" />
            ))}
          </div>
        ) : error ? (
          <p className="text-destructive text-sm">加载失败：{error}</p>
        ) : stages.length === 0 ? (
          <p className="text-muted-foreground text-sm">暂无 SRC 任务数据。</p>
        ) : (
          <>
            <div className="grid gap-1.5">
              {stages.map((s, i) => {
                const prev = i > 0 ? stages[i - 1].value : 0;
                const rate = prev > 0 ? Math.round((s.value / prev) * 100) : null;
                return (
                  <div key={s.key} className="grid grid-cols-[9rem_minmax(0,1fr)_5rem_3.5rem] items-center gap-2 text-sm">
                    <span className="text-muted-foreground truncate text-xs" title={s.label}>
                      {s.label}
                    </span>
                    <div className="bg-muted h-6 overflow-hidden rounded">
                      <div
                        className={cn("h-full rounded transition-all", STAGE_TONE[i % STAGE_TONE.length])}
                        style={{ width: `${Math.max(2, (s.value / max) * 100)}%` }}
                      />
                    </div>
                    <span className="text-right font-medium tabular-nums">{fmtN(s.value)}</span>
                    <span
                      className={cn(
                        "text-right text-[11px] tabular-nums",
                        rate !== null && rate < 50 ? "text-amber-600 dark:text-amber-400" : "text-muted-foreground",
                      )}
                      title={rate !== null ? "相对上一阶段" : undefined}
                    >
                      {rate !== null ? `${rate}%` : "—"}
                    </span>
                  </div>
                );
              })}
            </div>

            <div className="grid grid-cols-2 gap-2 border-t pt-3 text-xs sm:grid-cols-5">
              <div>
                <div className="text-muted-foreground">SRC 任务</div>
                <div className="font-semibold tabular-nums">{fmtN(data?.tasks ?? 0)}</div>
              </div>
              <div>
                <div className="text-muted-foreground">终态格</div>
                <div className="font-semibold tabular-nums">{fmtN(data?.cells_terminal ?? 0)}</div>
              </div>
              <div>
                <div className="text-muted-foreground">Token</div>
                <div className="font-semibold tabular-nums">{fmtN(data?.tokens ?? 0)}</div>
              </div>
              <div>
                <div className="text-muted-foreground">成本</div>
                <div className="font-semibold tabular-nums">{fmtCost(data?.cost_usd ?? 0)}</div>
              </div>
              <div>
                <div className="text-muted-foreground">每条齐备发现成本</div>
                <div className="font-semibold tabular-nums">{fmtCost(data?.cost_per_effective ?? 0)}</div>
              </div>
            </div>

            {data?.slo ? (
              <div className="grid grid-cols-2 gap-2 border-t pt-3 text-xs sm:grid-cols-4">
                <div>
                  <div className="text-muted-foreground">排队 P50 / P95</div>
                  <div className="font-semibold tabular-nums">
                    {fmtMinutes(data.slo.queue_wait_p50_minutes)} / {fmtMinutes(data.slo.queue_wait_p95_minutes)}
                  </div>
                </div>
                <div>
                  <div className="text-muted-foreground">任务时长 P50 / P95</div>
                  <div className="font-semibold tabular-nums">
                    {fmtMinutes(data.slo.task_duration_p50_minutes)} / {fmtMinutes(data.slo.task_duration_p95_minutes)}
                  </div>
                </div>
                <div>
                  <div className="text-muted-foreground">提交前停留 P50</div>
                  <div className="font-semibold tabular-nums">{fmtHours(data.slo.pre_submit_dwell_p50_hours)}</div>
                </div>
                <div>
                  <div className="text-muted-foreground">中危+证据绑定率</div>
                  <div className="font-semibold tabular-nums">{Math.round(data.slo.evidence_coverage_pct)}%</div>
                </div>
              </div>
            ) : null}

            {severityRows.length > 0 ? (
              <div className="flex flex-wrap gap-1.5">
                {severityRows.map((s) => (
                  <span
                    key={s.severity}
                    className="text-muted-foreground rounded border px-1.5 py-0.5 text-[11px] tabular-nums"
                    title="发现数 / 报告齐备 / 已受理"
                  >
                    {s.severity} {fmtN(s.findings)} · 齐 {fmtN(s.reports_ready)} · 收 {fmtN(s.accepted)}
                  </span>
                ))}
              </div>
            ) : null}

            {(data?.by_campaign?.length ?? 0) > 0 ? (
              <div className="overflow-x-auto border-t pt-3">
                <div className="text-muted-foreground mb-1 flex items-center gap-2 text-xs">
                  <TrendingDownIcon className="size-3.5" /> 按战役
                </div>
                <table className="w-full text-xs">
                  <thead>
                    <tr className="text-muted-foreground border-b text-left">
                      <th className="py-1 pr-3 font-medium">战役</th>
                      <th className="py-1 pr-3 text-right font-medium">任务</th>
                      <th className="py-1 pr-3 text-right font-medium">中危+</th>
                      <th className="py-1 pr-3 text-right font-medium">齐备</th>
                      <th className="py-1 pr-3 text-right font-medium">已提交</th>
                      <th className="py-1 pr-3 text-right font-medium">受理</th>
                      <th className="py-1 text-right font-medium">成本</th>
                    </tr>
                  </thead>
                  <tbody>
                    {(data?.by_campaign ?? []).slice(0, 6).map((c) => (
                      <tr key={c.campaign_id} className="border-b last:border-0">
                        <td className="max-w-56 truncate py-1 pr-3" title={c.name}>
                          {c.name}
                        </td>
                        <td className="py-1 pr-3 text-right tabular-nums">{c.tasks}</td>
                        <td className="py-1 pr-3 text-right tabular-nums">{c.findings_medium_plus}</td>
                        <td className="py-1 pr-3 text-right tabular-nums">{c.reports_ready}</td>
                        <td className="py-1 pr-3 text-right tabular-nums">{c.submitted}</td>
                        <td className="py-1 pr-3 text-right tabular-nums">{c.accepted}</td>
                        <td className="py-1 text-right tabular-nums">{fmtCost(c.cost_usd)}</td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            ) : null}

            {(data?.by_program?.length ?? 0) > 0 ? (
              <div className="overflow-x-auto border-t pt-3">
                <div className="text-muted-foreground mb-1 flex items-center gap-2 text-xs">
                  <TrendingDownIcon className="size-3.5" /> 按项目
                </div>
                <table className="w-full text-xs">
                  <thead>
                    <tr className="text-muted-foreground border-b text-left">
                      <th className="py-1 pr-3 font-medium">项目</th>
                      <th className="py-1 pr-3 text-right font-medium">任务</th>
                      <th className="py-1 pr-3 text-right font-medium">中危+</th>
                      <th className="py-1 pr-3 text-right font-medium">齐备</th>
                      <th className="py-1 pr-3 text-right font-medium">已提交</th>
                      <th className="py-1 pr-3 text-right font-medium">受理</th>
                      <th className="py-1 text-right font-medium">成本</th>
                    </tr>
                  </thead>
                  <tbody>
                    {(data?.by_program ?? []).slice(0, 6).map((p) => (
                      <tr key={p.program_id} className="border-b last:border-0">
                        <td className="max-w-56 truncate py-1 pr-3" title={p.name}>
                          {p.name}
                        </td>
                        <td className="py-1 pr-3 text-right tabular-nums">{p.tasks}</td>
                        <td className="py-1 pr-3 text-right tabular-nums">{p.findings_medium_plus}</td>
                        <td className="py-1 pr-3 text-right tabular-nums">{p.reports_ready}</td>
                        <td className="py-1 pr-3 text-right tabular-nums">{p.submitted}</td>
                        <td className="py-1 pr-3 text-right tabular-nums">{p.accepted}</td>
                        <td className="py-1 text-right tabular-nums">{fmtCost(p.cost_usd)}</td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            ) : null}
          </>
        )}
      </CardContent>
    </Card>
  );
}
