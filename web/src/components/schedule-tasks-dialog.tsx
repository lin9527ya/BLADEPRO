"use client";

import * as React from "react";

import { CalendarClockIcon, PencilIcon, PlayIcon, PlusIcon, Trash2Icon } from "lucide-react";
import { toast } from "sonner";

import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { NativeSelect, NativeSelectOption } from "@/components/ui/native-select";
import { Switch } from "@/components/ui/switch";
import { Textarea } from "@/components/ui/textarea";
import { api } from "@/lib/api";
import type { ScheduleFrequency, SrcProgram, TaskCategory, TaskMode, TaskSchedule, TaskTemplate } from "@/lib/types";

/**
 * ScheduleTasksDialog 管理「计划任务」（P22）：按结构化频率到点自动建任务。
 *
 * 语义（与后端 server/schedules.go 一致）：
 *  · 一份计划任务 = 一份**确定的可重放规格**（描述/目标/分类/拦截规则…）+ 一个频率；
 *    到点按规格原样建一个普通任务，之后的推送/交付全走既有链路。
 *  · 间隔型首次运行 = 创建后一个间隔（不在创建瞬间就跑；急就跑「立即运行」）。
 *  · 停机期间错过的次数**不追补**（开机只跑一次）。
 *  · 「立即运行」是额外一次，不改排期。
 */

const emptyForm = () => ({
  name: "",
  description: "",
  goal: "",
  categoryID: undefined as number | undefined,
  mode: "" as TaskMode,
  srcProgramID: undefined as number | undefined,
  inheritCoverage: false,
  batch: false,
  shardBy: "host" as "host" | "root_domain",
  assetsPerTask: "20",
  maxShards: "50",
  campaignName: "",
  campaignTokenBudget: "0",
  batchFilter: "all" as "all" | "new" | "untested",
  enabled: true,
  kind: "every_hours" as ScheduleFrequency["kind"],
  everyMinutes: "30",
  everyHours: "6",
  at: "09:30",
  weekday: 1,
});

type FormState = ReturnType<typeof emptyForm>;

function fmtLocal(ts?: string): string {
  if (!ts) return "—";
  const d = new Date(ts);
  if (Number.isNaN(d.getTime())) return "—";
  return d.toLocaleString("zh-CN", {
    month: "2-digit",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
    hour12: false,
  });
}

export function ScheduleTasksDialog({ categories, onCreated }: { categories: TaskCategory[]; onCreated?: () => void }) {
  const [open, setOpen] = React.useState(false);
  const [rows, setRows] = React.useState<TaskSchedule[]>([]);
  const [templates, setTemplates] = React.useState<TaskTemplate[]>([]);
  const [programs, setPrograms] = React.useState<SrcProgram[]>([]);
  const [editingID, setEditingID] = React.useState<number | null>(null); // null = 新建
  const [formOpen, setFormOpen] = React.useState(false);
  const [form, setForm] = React.useState<FormState>(emptyForm);
  const [busy, setBusy] = React.useState(false);

  const load = React.useCallback(async () => {
    try {
      setRows(await api.taskSchedules());
    } catch {
      // 瞬时错误忽略
    }
  }, []);

  React.useEffect(() => {
    if (!open) return;
    void load();
    void api
      .taskTemplates()
      .then(setTemplates)
      .catch(() => {});
    void api
      .srcPrograms()
      .then((r) => setPrograms(r.programs ?? []))
      .catch(() => setPrograms([]));
  }, [open, load]);

  function startCreate() {
    setEditingID(null);
    setForm(emptyForm());
    setFormOpen(true);
  }

  function startEdit(s: TaskSchedule) {
    setEditingID(s.id);
    setForm({
      name: s.name,
      description: s.task_spec.description,
      goal: s.task_spec.goal,
      categoryID: s.task_spec.category_id,
      mode: s.task_spec.mode === "src" ? "src" : "",
      srcProgramID: s.task_spec.src_program_id,
      inheritCoverage: !!s.task_spec.inherit_coverage,
      batch: !!s.task_spec.batch,
      shardBy: s.task_spec.batch?.shard_by === "root_domain" ? "root_domain" : "host",
      assetsPerTask: String(s.task_spec.batch?.assets_per_task ?? 20),
      maxShards: String(s.task_spec.batch?.max_shards ?? 50),
      campaignName: s.task_spec.batch?.campaign_name ?? "",
      campaignTokenBudget: String(s.task_spec.batch?.campaign_token_budget ?? 0),
      batchFilter: s.task_spec.batch?.only_new_assets
        ? "new"
        : s.task_spec.batch?.only_untested_assets
          ? "untested"
          : "all",
      enabled: s.enabled,
      kind: s.frequency.kind,
      everyMinutes: String(s.frequency.every_minutes ?? 30),
      everyHours: String(s.frequency.every_hours ?? 6),
      at: s.frequency.at ?? "09:30",
      weekday: s.frequency.weekday ?? 1,
    });
    setFormOpen(true);
  }

  function applyTemplate(id: number) {
    const t = templates.find((x) => x.id === id);
    if (!t) return;
    setForm((f) => ({
      ...f,
      name: f.name || t.name,
      description: t.description,
      goal: t.goal,
      categoryID: t.category_id ?? f.categoryID,
      mode: t.mode === "src" ? "src" : "",
      srcProgramID: t.mode === "src" ? (t.src_program_id ?? undefined) : undefined,
      inheritCoverage: t.mode === "src" && !!t.inherit_coverage,
    }));
  }

  function buildFrequency(): ScheduleFrequency {
    switch (form.kind) {
      case "every_minutes":
        return { kind: "every_minutes", every_minutes: Number(form.everyMinutes) || 0 };
      case "every_hours":
        return { kind: "every_hours", every_hours: Number(form.everyHours) || 0 };
      case "daily_at":
        return { kind: "daily_at", at: form.at };
      default:
        return { kind: "weekly_at", at: form.at, weekday: form.weekday };
    }
  }

  async function save() {
    if (!form.description.trim() || !form.goal.trim()) {
      toast.error("描述与目标必填");
      return;
    }
    setBusy(true);
    try {
      const payload = {
        name: form.name.trim() || form.description.trim().slice(0, 40),
        enabled: form.enabled,
        task_spec: {
          description: form.description.trim(),
          goal: form.goal.trim(),
          category_id: form.categoryID,
          mode: form.mode,
          src_program_id: form.mode === "src" ? form.srcProgramID : undefined,
          inherit_coverage: form.mode === "src" ? form.inheritCoverage : false,
          batch: form.batch
            ? {
                shard_by: form.shardBy,
                assets_per_task: Math.max(1, Number(form.assetsPerTask) || 20),
                max_shards: Math.max(1, Number(form.maxShards) || 50),
                campaign_name: form.campaignName.trim() || undefined,
                campaign_token_budget: Math.max(0, Number(form.campaignTokenBudget) || 0),
                only_new_assets: form.batchFilter === "new",
                only_untested_assets: form.batchFilter === "untested",
              }
            : undefined,
        },
        frequency: buildFrequency(),
      };
      if (editingID == null) {
        await api.createTaskSchedule(payload);
        toast.success(form.enabled ? "计划任务已创建（到点自动建任务）" : "计划任务已创建（当前停用）");
      } else {
        await api.updateTaskSchedule(editingID, payload);
        toast.success("计划任务已更新");
      }
      setFormOpen(false);
      await load();
    } catch (e) {
      toast.error("保存失败：" + (e as Error).message);
    } finally {
      setBusy(false);
    }
  }

  async function toggle(s: TaskSchedule, enabled: boolean) {
    setRows((prev) => prev.map((x) => (x.id === s.id ? { ...x, enabled } : x)));
    try {
      const updated = await api.updateTaskSchedule(s.id, { enabled });
      setRows((prev) => prev.map((x) => (x.id === s.id ? updated : x)));
      toast.success(
        enabled ? `已启用：${updated.frequency_text}（下次 ${fmtLocal(updated.next_run_at)}）` : "已停用（排期已清空）",
      );
    } catch (e) {
      toast.error("切换失败：" + (e as Error).message);
      await load();
    }
  }

  async function runNow(s: TaskSchedule) {
    setBusy(true);
    try {
      const r = await api.runTaskScheduleNow(s.id);
      toast.success(`已建任务 #${r.task_id}（不改排期）`);
      onCreated?.();
      await load();
    } catch (e) {
      toast.error("运行失败：" + (e as Error).message);
    } finally {
      setBusy(false);
    }
  }

  async function remove(s: TaskSchedule) {
    setBusy(true);
    try {
      await api.deleteTaskSchedule(s.id);
      toast.success("计划任务已删除（已建出的任务不受影响）");
      await load();
    } catch (e) {
      toast.error("删除失败：" + (e as Error).message);
    } finally {
      setBusy(false);
    }
  }

  return (
    <Dialog open={open} onOpenChange={setOpen}>
      <DialogTrigger asChild>
        <Button size="sm" variant="outline">
          <CalendarClockIcon data-icon="inline-start" />
          计划任务
        </Button>
      </DialogTrigger>
      <DialogContent className="flex max-h-[88vh] flex-col gap-0 overflow-hidden p-0 sm:max-w-2xl">
        <DialogHeader className="border-b p-6">
          <DialogTitle>计划任务</DialogTitle>
          <DialogDescription>
            按固定节奏自动建任务（定期复测 / 新资产巡检）。到点按这里的规格原样建一个普通任务，
            推送与交付走既有链路；停机期间错过的次数不追补，急就跑「立即运行」。
          </DialogDescription>
        </DialogHeader>

        <div className="min-h-0 flex-1 overflow-y-auto p-6">
          {formOpen ? (
            <div className="grid gap-4">
              <div className="grid gap-2">
                <Label className="text-sm font-normal text-muted-foreground">从模板预填（可选）</Label>
                <NativeSelect
                  size="sm"
                  value=""
                  onChange={(e) => {
                    const id = Number(e.target.value);
                    if (id) applyTemplate(id);
                  }}
                >
                  <NativeSelectOption value="">不预填</NativeSelectOption>
                  {templates.map((t) => (
                    <NativeSelectOption key={t.id} value={String(t.id)}>
                      {t.name}
                    </NativeSelectOption>
                  ))}
                </NativeSelect>
              </div>
              <div className="grid gap-2">
                <Label htmlFor="sched-name">名称</Label>
                <Input
                  id="sched-name"
                  value={form.name}
                  onChange={(e) => setForm({ ...form, name: e.target.value })}
                  placeholder="如：每周一 09:30 母公司资产巡检"
                  disabled={busy}
                />
              </div>
              <div className="grid gap-2">
                <Label htmlFor="sched-desc">任务描述（必填）</Label>
                <Textarea
                  id="sched-desc"
                  rows={2}
                  value={form.description}
                  onChange={(e) => setForm({ ...form, description: e.target.value })}
                  placeholder="如：example.com 主站与 API 只读复测"
                  disabled={busy}
                />
              </div>
              <div className="grid gap-2">
                <Label htmlFor="sched-goal">任务目标（必填）</Label>
                <Textarea
                  id="sched-goal"
                  rows={2}
                  value={form.goal}
                  onChange={(e) => setForm({ ...form, goal: e.target.value })}
                  placeholder="如：确认上周的越权读取已修复，并检查新上线的接口"
                  disabled={busy}
                />
              </div>
              <div className="grid gap-2">
                <Label htmlFor="sched-cat">分类（可选）</Label>
                <NativeSelect
                  size="sm"
                  id="sched-cat"
                  value={form.categoryID == null ? "" : String(form.categoryID)}
                  onChange={(e) =>
                    setForm({ ...form, categoryID: e.target.value ? Number(e.target.value) : undefined })
                  }
                >
                  <NativeSelectOption value="">无分类</NativeSelectOption>
                  {categories.map((c) => (
                    <NativeSelectOption key={c.id} value={String(c.id)}>
                      {c.name}
                    </NativeSelectOption>
                  ))}
                </NativeSelect>
              </div>
              <div className="grid gap-2">
                <Label htmlFor="sched-mode">任务模式</Label>
                <NativeSelect
                  size="sm"
                  id="sched-mode"
                  value={form.mode}
                  onChange={(e) => {
                    const mode = e.target.value as TaskMode;
                    setForm({
                      ...form,
                      mode,
                      ...(mode !== "src" ? { srcProgramID: undefined, inheritCoverage: false } : {}),
                    });
                  }}
                >
                  <NativeSelectOption value="">普通渗透</NativeSelectOption>
                  <NativeSelectOption value="src">SRC 挖掘</NativeSelectOption>
                </NativeSelect>
                <p className="text-muted-foreground text-xs">模式在每次触发时写入新任务，创建后不可切换。</p>
              </div>
              {form.mode === "src" ? (
                <>
                  <div className="grid gap-2">
                    <Label htmlFor="sched-program">SRC 平台项目（可选）</Label>
                    <NativeSelect
                      size="sm"
                      id="sched-program"
                      value={form.srcProgramID == null ? "" : String(form.srcProgramID)}
                      onChange={(e) =>
                        setForm({ ...form, srcProgramID: e.target.value ? Number(e.target.value) : undefined })
                      }
                    >
                      <NativeSelectOption value="">不绑定</NativeSelectOption>
                      {programs.map((p) => (
                        <NativeSelectOption key={p.id} value={String(p.id)}>
                          {p.platform ? `${p.name} · ${p.platform}` : p.name}
                        </NativeSelectOption>
                      ))}
                    </NativeSelect>
                    <p className="text-muted-foreground text-xs">每次触发时把项目规则编译进新任务。</p>
                  </div>
                  <label htmlFor="sched-inherit" className="flex items-center gap-2 text-sm">
                    <input
                      id="sched-inherit"
                      type="checkbox"
                      className="size-4 accent-primary"
                      checked={form.inheritCoverage}
                      onChange={(e) => setForm({ ...form, inheritCoverage: e.target.checked })}
                    />
                    继承近期覆盖（同项目 / 同企业的 tested_clear 与收尾数字）
                  </label>
                </>
              ) : null}
              <div className="grid gap-2 rounded-lg border p-3">
                <label htmlFor="sched-batch" className="flex items-center gap-2 text-sm">
                  <input
                    id="sched-batch"
                    type="checkbox"
                    className="size-4 accent-primary"
                    checked={form.batch}
                    onChange={(e) => setForm({ ...form, batch: e.target.checked })}
                  />
                  每次触发按资产分片建一组任务（批量扇出）
                </label>
                {form.batch ? (
                  <div className="grid gap-2 sm:grid-cols-2">
                    <div className="grid gap-1">
                      <Label className="text-xs">分片方式</Label>
                      <NativeSelect
                        size="sm"
                        value={form.shardBy}
                        onChange={(e) => setForm({ ...form, shardBy: e.target.value as FormState["shardBy"] })}
                      >
                        <NativeSelectOption value="host">按 host</NativeSelectOption>
                        <NativeSelectOption value="root_domain">按根域名</NativeSelectOption>
                      </NativeSelect>
                    </div>
                    <div className="grid gap-1">
                      <Label htmlFor="sched-assets-per-task" className="text-xs">
                        每片资产上限
                      </Label>
                      <Input
                        id="sched-assets-per-task"
                        inputMode="numeric"
                        value={form.assetsPerTask}
                        onChange={(e) => setForm({ ...form, assetsPerTask: e.target.value })}
                      />
                    </div>
                    <div className="grid gap-1">
                      <Label htmlFor="sched-max-shards" className="text-xs">
                        最大分片数
                      </Label>
                      <Input
                        id="sched-max-shards"
                        inputMode="numeric"
                        value={form.maxShards}
                        onChange={(e) => setForm({ ...form, maxShards: e.target.value })}
                      />
                    </div>
                    <div className="grid gap-1">
                      <Label className="text-xs">只跑</Label>
                      <NativeSelect
                        size="sm"
                        value={form.batchFilter}
                        onChange={(e) =>
                          setForm({ ...form, batchFilter: e.target.value as FormState["batchFilter"] })
                        }
                      >
                        <NativeSelectOption value="all">全部资产</NativeSelectOption>
                        <NativeSelectOption value="new">新增资产</NativeSelectOption>
                        <NativeSelectOption value="untested">未测资产</NativeSelectOption>
                      </NativeSelect>
                    </div>
                    <div className="grid gap-1 sm:col-span-2">
                      <Label htmlFor="sched-campaign" className="text-xs">
                        Campaign 名称（可选）
                      </Label>
                      <Input
                        id="sched-campaign"
                        value={form.campaignName}
                        placeholder="留空自动生成"
                        onChange={(e) => setForm({ ...form, campaignName: e.target.value })}
                      />
                    </div>
                    <div className="grid gap-1 sm:col-span-2">
                      <Label htmlFor="sched-campaign-budget" className="text-xs">
                        战役总 token 预算（0=不限）
                      </Label>
                      <Input
                        id="sched-campaign-budget"
                        inputMode="numeric"
                        value={form.campaignTokenBudget}
                        onChange={(e) => setForm({ ...form, campaignTokenBudget: e.target.value })}
                      />
                    </div>
                    <p className="text-muted-foreground text-xs sm:col-span-2">
                      资产范围来自本计划任务绑定的 SRC 项目/企业范围；未绑定项目时需通过 API 指定 company_ids 或 asset_ids。
                    </p>
                  </div>
                ) : null}
              </div>
              <div className="grid gap-2">
                <Label htmlFor="sched-kind">频率</Label>
                <div className="flex flex-wrap items-center gap-2">
                  <NativeSelect
                    size="sm"
                    id="sched-kind"
                    value={form.kind}
                    onChange={(e) => setForm({ ...form, kind: e.target.value as FormState["kind"] })}
                  >
                    <NativeSelectOption value="every_minutes">每 N 分钟</NativeSelectOption>
                    <NativeSelectOption value="every_hours">每 N 小时</NativeSelectOption>
                    <NativeSelectOption value="daily_at">每天 HH:MM</NativeSelectOption>
                    <NativeSelectOption value="weekly_at">每周某天 HH:MM</NativeSelectOption>
                  </NativeSelect>
                  {form.kind === "every_minutes" && (
                    <Input
                      className="h-8 w-20"
                      inputMode="numeric"
                      value={form.everyMinutes}
                      onChange={(e) => setForm({ ...form, everyMinutes: e.target.value.replace(/[^0-9]/g, "") })}
                      disabled={busy}
                    />
                  )}
                  {form.kind === "every_hours" && (
                    <Input
                      className="h-8 w-20"
                      inputMode="numeric"
                      value={form.everyHours}
                      onChange={(e) => setForm({ ...form, everyHours: e.target.value.replace(/[^0-9]/g, "") })}
                      disabled={busy}
                    />
                  )}
                  {form.kind === "weekly_at" && (
                    <NativeSelect
                      size="sm"
                      value={String(form.weekday)}
                      onChange={(e) => setForm({ ...form, weekday: Number(e.target.value) })}
                    >
                      {["周日", "周一", "周二", "周三", "周四", "周五", "周六"].map((w, i) => (
                        <NativeSelectOption key={w} value={String(i)}>
                          {w}
                        </NativeSelectOption>
                      ))}
                    </NativeSelect>
                  )}
                  {(form.kind === "daily_at" || form.kind === "weekly_at") && (
                    <Input
                      className="h-8 w-24"
                      value={form.at}
                      onChange={(e) => setForm({ ...form, at: e.target.value })}
                      placeholder="09:30"
                      disabled={busy}
                    />
                  )}
                </div>
                <p className="text-muted-foreground text-xs">
                  {form.kind === "every_minutes" || form.kind === "every_hours"
                    ? "间隔型从创建时刻起算（首次运行 = 创建后一个间隔）。"
                    : "按服务器本地时间（当前 " +
                      new Date().toLocaleTimeString("zh-CN", { hour12: false }) +
                      "）在到点时触发。"}
                </p>
              </div>
              <div className="flex items-center justify-between gap-4">
                <Label htmlFor="sched-enabled" className="text-sm font-normal text-muted-foreground">
                  创建后立即启用{form.enabled ? "" : "（保持停用，之后再开）"}
                </Label>
                <Switch
                  id="sched-enabled"
                  checked={form.enabled}
                  onCheckedChange={(v) => setForm({ ...form, enabled: v })}
                />
              </div>
              <div className="flex gap-2">
                <Button size="sm" onClick={() => void save()} disabled={busy}>
                  {editingID == null ? "创建" : "保存"}
                </Button>
                <Button size="sm" variant="outline" onClick={() => setFormOpen(false)} disabled={busy}>
                  取消
                </Button>
              </div>
            </div>
          ) : (
            <div className="flex flex-col gap-3">
              <Button size="sm" onClick={startCreate}>
                <PlusIcon className="size-3.5" /> 新建计划任务
              </Button>
              {rows.length === 0 ? (
                <p className="text-muted-foreground text-sm">
                  还没有计划任务。适合「每周复测一遍这批资产」「每天看一眼有没有新上线资产」这类固定节奏的活。
                </p>
              ) : (
                rows.map((s) => (
                  <div key={s.id} className="flex flex-col gap-1.5 rounded-md border px-3 py-2">
                    <div className="flex items-center gap-2">
                      <span className="min-w-0 flex-1 truncate text-sm font-medium" title={s.name}>
                        {s.name}
                      </span>
                      <Switch checked={s.enabled} onCheckedChange={(v) => void toggle(s, v)} disabled={busy} />
                    </div>
                    <div className="text-muted-foreground flex flex-wrap items-center gap-x-3 gap-y-1 text-xs">
                      <span>{s.frequency_text}</span>
                      <span>下次 {s.enabled ? fmtLocal(s.next_run_at) : "（停用）"}</span>
                      <span>上次 {fmtLocal(s.last_run_at)}</span>
                      {s.last_task_id && (
                        <a
                          className="hover:underline"
                          href={`/function/tasks/detail?id=${encodeURIComponent(s.last_task_id)}`}
                        >
                          任务 #{s.last_task_id}
                        </a>
                      )}
                    </div>
                    {s.last_error && <span className="text-xs text-red-500">上次失败：{s.last_error}</span>}
                    <div className="flex items-center gap-1.5">
                      <Button
                        size="sm"
                        variant="ghost"
                        className="h-6 px-1.5"
                        disabled={busy}
                        onClick={() => void runNow(s)}
                      >
                        <PlayIcon className="size-3.5" /> 立即运行
                      </Button>
                      <Button
                        size="sm"
                        variant="ghost"
                        className="h-6 px-1.5"
                        disabled={busy}
                        onClick={() => startEdit(s)}
                      >
                        <PencilIcon className="size-3.5" />
                      </Button>
                      <Button
                        size="sm"
                        variant="ghost"
                        className="h-6 px-1.5"
                        disabled={busy}
                        onClick={() => void remove(s)}
                      >
                        <Trash2Icon className="size-3.5 text-red-500" />
                      </Button>
                    </div>
                  </div>
                ))
              )}
            </div>
          )}
        </div>
      </DialogContent>
    </Dialog>
  );
}
