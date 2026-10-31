"use client";

import * as React from "react";

import Link from "next/link";

import { CheckCheckIcon, ListChecksIcon, RotateCcwIcon, ShieldAlertIcon, XIcon } from "lucide-react";
import { toast } from "sonner";

import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from "@/components/ui/dialog";
import { Label } from "@/components/ui/label";
import { Spinner } from "@/components/ui/spinner";
import { Textarea } from "@/components/ui/textarea";
import { api } from "@/lib/api";
import type { ReviewKind, ReviewQueue, ReviewQueueItem } from "@/lib/types";
import { cn } from "@/lib/utils";

const KIND_LABEL: Record<ReviewKind, string> = {
  check_unverified: "收尾待核验",
  report_missing: "报告缺段",
  retest_inconclusive: "复验存疑",
  dedupe_pending: "疑似重复",
  budget_stop: "预算停",
  suspicious_na: "四件套全 N/A",
  ready_stale: "待提交超 SLA",
  submitted_stale: "已提交超 SLA",
  accepted_unpaid: "已受理未到账",
};

const KIND_TONE: Record<ReviewKind, string> = {
  check_unverified: "border-amber-500/40 text-amber-600 dark:text-amber-400",
  report_missing: "border-sky-500/40 text-sky-600 dark:text-sky-400",
  retest_inconclusive: "border-amber-500/40 text-amber-600 dark:text-amber-400",
  dedupe_pending: "border-violet-500/40 text-violet-600 dark:text-violet-400",
  budget_stop: "border-orange-500/40 text-orange-600 dark:text-orange-400",
  suspicious_na: "border-rose-500/40 text-rose-600 dark:text-rose-400",
  ready_stale: "border-sky-500/40 text-sky-600 dark:text-sky-400",
  submitted_stale: "border-amber-500/40 text-amber-600 dark:text-amber-400",
  accepted_unpaid: "border-emerald-500/40 text-emerald-600 dark:text-emerald-400",
};

function itemLabel(item: ReviewQueueItem): string {
  if (item.title) return item.title;
  if (item.asset_label) {
    return item.area
      ? `${item.asset_label} · ${item.area}${item.item && item.item !== "*" ? `/${item.item}` : ""}`
      : item.asset_label;
  }
  return item.ref_key;
}

/**
 * 人工复核队列：聚合报告缺段/复验存疑/疑似重复/预算停/四件套全 N/A。
 * 批量动作复用既有状态机：接受=确认发现或留痕；打回=误报/解除重复/删格重测；
 * 退回重测=启动独立复测或把格删回空白。
 */
export function ReviewQueueDialog({ onChanged }: { onChanged?: () => void }) {
  const checkboxPrefix = React.useId();
  const [open, setOpen] = React.useState(false);
  const [data, setData] = React.useState<ReviewQueue | null>(null);
  const [loading, setLoading] = React.useState(false);
  const [busy, setBusy] = React.useState(false);
  const [note, setNote] = React.useState("");
  const [selected, setSelected] = React.useState<Set<string>>(new Set());

  const load = React.useCallback(async () => {
    setLoading(true);
    try {
      const q = await api.reviewQueue({ limit: 200 });
      setData(q);
      setSelected((prev) => {
        const live = new Set(q.items.map((i) => i.ref_key));
        return new Set([...prev].filter((k) => live.has(k)));
      });
    } catch (e) {
      toast.error("加载复核队列失败：" + (e as Error).message);
    } finally {
      setLoading(false);
    }
  }, []);

  React.useEffect(() => {
    if (open) void load();
  }, [open, load]);

  const items = data?.items ?? [];
  const selectedItems = items.filter((i) => selected.has(i.ref_key));

  function toggle(ref: string, checked: boolean) {
    setSelected((prev) => {
      const next = new Set(prev);
      if (checked) next.add(ref);
      else next.delete(ref);
      return next;
    });
  }

  function toggleAll(checked: boolean) {
    setSelected(checked ? new Set(items.map((i) => i.ref_key)) : new Set());
  }

  async function run(action: "accept" | "reject" | "retest") {
    if (selectedItems.length === 0) {
      toast.error("先选择要处理的复核项");
      return;
    }
    if (action === "accept" && selectedItems.some((i) => i.kind === "check_unverified") && !note.trim()) {
      toast.error("人工核验收尾项必须填写实际核对依据");
      return;
    }
    setBusy(true);
    try {
      const r = await api.reviewActions(action, selectedItems, note.trim());
      if (r.failed > 0) {
        toast.warning(`已处理 ${r.ok} 项，${r.failed} 项失败`, {
          description: r.results
            .filter((x) => !x.ok)
            .map((x) => x.error)
            .filter(Boolean)
            .slice(0, 3)
            .join("；"),
        });
      } else {
        toast.success(
          `已处理 ${r.ok} 项（${action === "accept" ? "接受" : action === "reject" ? "打回" : "退回重测"}）`,
        );
      }
      setNote("");
      setSelected(new Set());
      await load();
      onChanged?.();
    } catch (e) {
      toast.error("处理失败：" + (e as Error).message);
    } finally {
      setBusy(false);
    }
  }

  return (
    <Dialog open={open} onOpenChange={setOpen}>
      <DialogTrigger asChild>
        <Button size="sm" variant="outline">
          <ListChecksIcon data-icon="inline-start" />
          复核队列
          {data && data.total > 0 ? (
            <Badge variant="outline" className="ml-1 border-amber-500/50 text-amber-600 dark:text-amber-400">
              {data.total}
            </Badge>
          ) : null}
        </Button>
      </DialogTrigger>
      <DialogContent className="flex max-h-[88vh] flex-col gap-0 overflow-hidden p-0 sm:max-w-3xl">
        <DialogHeader className="border-b px-5 py-4">
          <DialogTitle className="flex items-center gap-2">
            <ShieldAlertIcon className="size-4 text-amber-500" /> 人工复核队列
          </DialogTitle>
          <DialogDescription>
            按严重度 × 不确定性 × 临界程度排序：报告缺段、复验存疑、疑似重复、预算停、 四件套全 N/A，以及台账 SLA（ready
            未提交 / submitted 无结论 / accepted 未到账）。 可批量接受/打回/退回重测；已处理项不再重复出现。
          </DialogDescription>
        </DialogHeader>

        <div className="flex min-h-0 flex-1 flex-col gap-3 overflow-y-auto px-5 py-4">
          <div className="flex flex-wrap items-center gap-3 text-sm">
            <label htmlFor={`${checkboxPrefix}-all`} className="flex items-center gap-2">
              <Checkbox
                id={`${checkboxPrefix}-all`}
                checked={items.length > 0 && selectedItems.length === items.length}
                onCheckedChange={(v) => toggleAll(v === true)}
                aria-label="全选"
              />
              全选
            </label>
            <span className="text-muted-foreground text-xs">
              共 {items.length} 项
              {Object.entries(data?.counts ?? {}).map(([k, n]) => (
                <span key={k} className="ml-2">
                  {KIND_LABEL[k as ReviewKind] ?? k} {n}
                </span>
              ))}
            </span>
            <Button
              size="sm"
              variant="ghost"
              className="ml-auto"
              onClick={() => void load()}
              disabled={loading || busy}
            >
              {loading ? <Spinner /> : <RotateCcwIcon />} 刷新
            </Button>
          </div>

          {items.length === 0 ? (
            <p className="text-muted-foreground py-10 text-center text-sm">
              {loading ? "加载中…" : "队列为空：当前没有需要人工裁决的项。"}
            </p>
          ) : (
            <div className="grid gap-1.5">
              {items.map((item) => (
                <label
                  key={item.ref_key}
                  htmlFor={`${checkboxPrefix}-${item.ref_key}`}
                  className={cn(
                    "flex cursor-pointer items-start gap-3 rounded-lg border p-3 transition-colors hover:bg-muted/40",
                    selected.has(item.ref_key) && "border-primary/40 bg-primary/5",
                  )}
                >
                  <Checkbox
                    id={`${checkboxPrefix}-${item.ref_key}`}
                    className="mt-0.5"
                    checked={selected.has(item.ref_key)}
                    onCheckedChange={(v) => toggle(item.ref_key, v === true)}
                    aria-label={`选择 ${item.ref_key}`}
                  />
                  <div className="min-w-0 flex-1">
                    <div className="flex flex-wrap items-center gap-2">
                      <Badge variant="outline" className={KIND_TONE[item.kind]}>
                        {KIND_LABEL[item.kind]}
                      </Badge>
                      {item.severity ? <Badge variant="outline">{item.severity}</Badge> : null}
                      <span className="truncate text-sm font-medium">{itemLabel(item)}</span>
                    </div>
                    <p className="text-muted-foreground mt-1 line-clamp-2 text-xs">{item.reason || "—"}</p>
                    <div className="text-muted-foreground mt-1 flex flex-wrap gap-x-3 gap-y-1 text-[11px]">
                      {item.task_id ? (
                        <Link
                          className="hover:text-foreground hover:underline"
                          href={`/function/tasks/detail?id=${item.task_id}`}
                          onClick={(e) => e.stopPropagation()}
                        >
                          任务 #{item.task_id} {item.task_label ? `· ${item.task_label}` : ""}
                        </Link>
                      ) : null}
                      {item.finding_id ? (
                        <Link
                          className="hover:text-foreground hover:underline"
                          href={`/function/findings/detail?id=${item.finding_id}`}
                          onClick={(e) => e.stopPropagation()}
                        >
                          发现 #{item.finding_id}
                        </Link>
                      ) : null}
                      <span>{new Date(item.updated_at).toLocaleString("zh-CN", { hour12: false })}</span>
                    </div>
                  </div>
                </label>
              ))}
            </div>
          )}
        </div>

        <DialogFooter className="border-t px-5 py-4">
          <div className="grid w-full gap-2">
            <Label htmlFor="review-note" className="text-xs text-muted-foreground">
              备注（可选，会写进裁决留痕；复测会作为补充说明）
            </Label>
            <Textarea
              id="review-note"
              rows={2}
              value={note}
              onChange={(e) => setNote(e.target.value)}
              placeholder="例如：已人工确认该接口无越权 / 复测签名已过期，重新取包"
              disabled={busy}
            />
            <div className="flex flex-wrap items-center gap-2">
              <span className="text-muted-foreground text-xs">已选 {selectedItems.length} 项</span>
              <div className="ml-auto flex flex-wrap gap-2">
                <Button size="sm" variant="outline" disabled={busy} onClick={() => void run("retest")}>
                  <RotateCcwIcon /> 退回重测
                </Button>
                <Button size="sm" variant="destructive" disabled={busy} onClick={() => void run("reject")}>
                  <XIcon /> 打回
                </Button>
                <Button size="sm" disabled={busy} onClick={() => void run("accept")}>
                  {busy ? <Spinner /> : <CheckCheckIcon />} 接受
                </Button>
              </div>
            </div>
          </div>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
