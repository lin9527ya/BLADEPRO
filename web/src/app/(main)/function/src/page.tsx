"use client";

import * as React from "react";

import Link from "next/link";

import { BugIcon, PencilIcon, PlusIcon, RefreshCwIcon, Trash2Icon } from "lucide-react";
import { toast } from "sonner";

import { Alert, AlertDescription } from "@/components/ui/alert";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Checkbox } from "@/components/ui/checkbox";
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
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { Textarea } from "@/components/ui/textarea";
import { api } from "@/lib/api";
import type {
  ProgramScopeInput,
  SrcProgram,
  SrcProgramInput,
  SrcProgramRule,
  SrcProgramRules,
  SrcStats,
  SrcSubmission,
} from "@/lib/types";

// 台账状态 → 中文 + 下一步合法流转。与服务端状态机一致（口径以服务端为准），
// 与任务详情 SRC 页签里的那份保持同源，避免两处对不上。
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

const SUBMISSION_STATUS_ORDER = [
  "draft",
  "ready",
  "submitted",
  "accepted",
  "duplicate",
  "rejected",
  "paid",
  "withdrawn",
];

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

/** 台账时间统一成 YYYY-MM-DD HH:mm（本地时区），空值给 —。 */
function fmtTime(s?: string): string {
  if (!s) return "—";
  const d = new Date(s);
  if (Number.isNaN(d.getTime())) return "—";
  const pad = (n: number) => String(n).padStart(2, "0");
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())} ${pad(d.getHours())}:${pad(d.getMinutes())}`;
}

function errText(e: unknown): string {
  return String((e as Error).message || e);
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

// ---- 平台项目：选项表 ----

const TECHNIQUE_OPTIONS: { id: string; label: string }[] = [
  { id: "bruteforce", label: "口令爆破" },
  { id: "dos", label: "拒绝服务" },
  { id: "social_engineering", label: "社工钓鱼" },
  { id: "webshell", label: "WebShell 落地" },
  { id: "reverse_shell", label: "反弹Shell" },
  { id: "persistence", label: "持久化" },
];

const WRITE_OPTIONS: { id: string; label: string }[] = [
  { id: "http_delete", label: "删除请求" },
  { id: "http_write_method", label: "写方法(PUT/PATCH)" },
  { id: "mutation_endpoint", label: "变更接口" },
  { id: "db_write", label: "数据库写" },
  { id: "db_dump", label: "脱库" },
];

const SEVERITY_ROWS = ["critical", "high", "medium", "low"];
const SEVERITY_ROW_LABELS: Record<string, string> = {
  critical: "严重 critical",
  high: "高危 high",
  medium: "中危 medium",
  low: "低危 low",
};
const PLATFORM_LEVELS = ["info", "low", "medium", "high", "critical"];
const NO_MAPPING = "none"; // Radix Select 不允许空字符串值，"不映射" 用哨兵值。

const TIME_WINDOW_RE = /^\d{2}:\d{2}-\d{2}:\d{2}(,\d{2}:\d{2}-\d{2}:\d{2})*$/;

function toCount(v: string): number {
  const n = Math.floor(Number(v) || 0);
  return n > 0 ? n : 0;
}

function countText(v?: number): string {
  return v && v > 0 ? String(v) : "";
}

/** 命令级规则：一行一条，`label|pattern` 或只给 pattern（按非正则关键字处理）。 */
function parseCommandRules(text: string): SrcProgramRule[] {
  const out: SrcProgramRule[] = [];
  for (const raw of text.split("\n")) {
    const line = raw.trim();
    if (!line) continue;
    const sep = line.indexOf("|");
    if (sep >= 0) {
      const label = line.slice(0, sep).trim();
      const pattern = line.slice(sep + 1).trim();
      if (!pattern) continue;
      out.push({ label: label || pattern, pattern, regex: false });
    } else {
      out.push({ label: line, pattern: line, regex: false });
    }
  }
  return out;
}

function commandRulesText(rules?: SrcProgramRule[]): string {
  return (rules ?? [])
    .map((r) => (r.label && r.label !== r.pattern ? `${r.label}|${r.pattern}` : r.pattern))
    .join("\n");
}

function emptySeverityMap(): Record<string, string> {
  return { critical: NO_MAPPING, high: NO_MAPPING, medium: NO_MAPPING, low: NO_MAPPING };
}

function severityMapText(map?: Record<string, string>): string {
  const entries = Object.entries(map ?? {});
  if (entries.length === 0) return "—";
  return entries.map(([k, v]) => `${k}→${v}`).join(" · ");
}

interface ProgramDraft {
  name: string;
  platform: string;
  platformTemplate: string;
  companyID: string;
  scopeNote: string;
  scopeRows: ProgramScopeInput[];
  rateLimit: string;
  timeWindow: string;
  retentionDays: string;
  forbiddenTechniques: string[];
  forbiddenWrites: string[];
  severityMap: Record<string, string>;
  maxIntentsTotal: string;
  maxIntentsPerRound: string;
  maxAttemptsPerCell: string;
  maxIntentsPerAsset: string;
  allowBudgetStop: boolean;
  skipReportGate: boolean;
  commandRules: string;
  // F2：收尾闸档位与平台红线；F3：提交块字段。
  checkTier: "" | "full" | "platform" | "off";
  pathsMin: string;
  manualPathsMin: string;
  noAutomation: boolean;
  redlines: string;
  maxRecordsRead: string;
  requireScreenshots: boolean;
  strictLayout: boolean;
  egressIP: string;
  testAccounts: string;
}

function emptyDraft(): ProgramDraft {
  return {
    name: "",
    platform: "",
    platformTemplate: "",
    companyID: "",
    scopeNote: "",
    scopeRows: [],
    rateLimit: "",
    timeWindow: "",
    retentionDays: "",
    forbiddenTechniques: [],
    forbiddenWrites: [],
    severityMap: emptySeverityMap(),
    maxIntentsTotal: "",
    maxIntentsPerRound: "",
    maxAttemptsPerCell: "",
    maxIntentsPerAsset: "",
    allowBudgetStop: false,
    skipReportGate: false,
    commandRules: "",
    checkTier: "",
    pathsMin: "",
    manualPathsMin: "",
    noAutomation: false,
    redlines: "",
    maxRecordsRead: "",
    requireScreenshots: false,
    strictLayout: false,
    egressIP: "",
    testAccounts: "",
  };
}

function draftFromProgram(p: SrcProgram): ProgramDraft {
  const rules = p.rules ?? {};
  const severityMap = emptySeverityMap();
  for (const key of SEVERITY_ROWS) {
    const mapped = p.severity_map?.[key];
    if (mapped) severityMap[key] = mapped;
  }
  return {
    name: p.name ?? "",
    platform: p.platform ?? "",
    platformTemplate: rules.platform_template ?? "",
    companyID: p.company_id ? String(p.company_id) : "",
    scopeNote: p.scope_note ?? "",
    scopeRows: [],
    rateLimit: countText(p.rate_limit),
    timeWindow: p.time_window ?? "",
    retentionDays: countText(p.retention_days),
    forbiddenTechniques: [...(rules.forbidden_techniques ?? [])],
    forbiddenWrites: [...(rules.forbidden_writes ?? [])],
    severityMap,
    maxIntentsTotal: countText(rules.max_intents_total),
    maxIntentsPerRound: countText(rules.max_intents_per_round),
    maxAttemptsPerCell: countText(rules.max_attempts_per_cell),
    maxIntentsPerAsset: countText(rules.max_intents_per_asset),
    allowBudgetStop: rules.allow_budget_stop === true,
    skipReportGate: rules.skip_report_gate === true,
    commandRules: commandRulesText(rules.command_rules),
    checkTier: (rules.check_tier ?? "") as "" | "full" | "platform" | "off",
    pathsMin: countText(rules.check_thresholds?.paths),
    manualPathsMin: countText(rules.check_thresholds?.manual_paths),
    noAutomation: rules.no_automation === true,
    redlines: (rules.redlines ?? []).join("\n"),
    maxRecordsRead: countText(rules.max_records_read),
    requireScreenshots: rules.require_screenshots === true,
    strictLayout: rules.strict_layout === true,
    egressIP: rules.egress_ip ?? "",
    testAccounts: rules.test_accounts ?? "",
  };
}

/** 点选切换的小圆角按钮（与建任务里的策略词表同款样式）。 */
function ToggleChip({ on, label, title, onClick }: { on: boolean; label: string; title?: string; onClick: () => void }) {
  return (
    <button
      type="button"
      title={title}
      aria-pressed={on}
      onClick={onClick}
      className={`rounded-full border px-2 py-0.5 text-xs transition-colors ${
        on
          ? "border-red-500/40 bg-red-500/15 text-red-600 dark:text-red-400"
          : "border-border text-muted-foreground hover:bg-muted"
      }`}
    >
      {on ? "禁止 · " : ""}
      {label}
    </button>
  );
}

// ---- Tab A：提交台账（全局）----

function LedgerTab() {
  const [stats, setStats] = React.useState<SrcStats | null>(null);
  const [subs, setSubs] = React.useState<SrcSubmission[]>([]);
  const [status, setStatus] = React.useState("all");
  const [loading, setLoading] = React.useState(true);
  const [error, setError] = React.useState("");

  const [target, setTarget] = React.useState<SrcSubmission | null>(null);
  const [nextStatus, setNextStatus] = React.useState("");
  const [externalID, setExternalID] = React.useState("");
  const [reward, setReward] = React.useState("");
  const [note, setNote] = React.useState("");
  const [saveError, setSaveError] = React.useState("");
  const [saving, setSaving] = React.useState(false);

  const load = React.useCallback(async () => {
    setLoading(true);
    try {
      const [nextStats, list] = await Promise.all([
        api.srcStats(),
        api.srcSubmissions(undefined, status === "all" ? undefined : status),
      ]);
      setStats(nextStats);
      setSubs(list.submissions ?? []);
      setError("");
    } catch (e) {
      setError(errText(e));
    } finally {
      setLoading(false);
    }
  }, [status]);

  React.useEffect(() => {
    void load();
  }, [load]);

  const openLedger = (sub: SrcSubmission) => {
    setTarget(sub);
    setNextStatus(sub.status);
    setExternalID(sub.external_id ?? "");
    setReward(sub.reward ?? "");
    setNote(sub.note ?? "");
    setSaveError("");
  };

  const submitLedger = async () => {
    if (!target) return;
    setSaving(true);
    setSaveError("");
    try {
      await api.patchSrcSubmission(target.id, {
        status: nextStatus || undefined,
        external_id: externalID.trim(),
        reward: reward.trim(),
        note: note.trim(),
      });
      setTarget(null);
      toast.success("台账已更新");
      await load();
    } catch (e) {
      setSaveError(errText(e));
    } finally {
      setSaving(false);
    }
  };

  const statusCounts = stats?.submissions ?? {};

  return (
    <div className="flex flex-col gap-3">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <div className="grid flex-1 grid-cols-2 gap-2 sm:grid-cols-4 xl:grid-cols-8">
          <Stat label="提交总数" value={stats?.submissions_total ?? 0} />
          <Stat label="可提交" value={statusCounts.ready ?? 0} />
          <Stat label="已提交" value={statusCounts.submitted ?? 0} />
          <Stat label="已受理" value={statusCounts.accepted ?? 0} />
          <Stat label="已到账" value={statusCounts.paid ?? 0} />
          <Stat label="SRC 任务" value={stats?.tasks ?? 0} />
          <Stat label="中危+发现" value={stats?.findings_medium_plus ?? 0} />
          <Stat label="报告齐备" value={stats?.findings_report_ready ?? 0} />
        </div>
      </div>

      <div className="flex flex-wrap items-center justify-between gap-2">
        <div className="flex items-center gap-2">
          <span className="text-muted-foreground text-xs">状态</span>
          <Select value={status} onValueChange={setStatus}>
            <SelectTrigger className="w-32" aria-label="台账状态筛选">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectGroup>
                <SelectItem value="all">全部</SelectItem>
                {SUBMISSION_STATUS_ORDER.map((s) => (
                  <SelectItem key={s} value={s}>
                    {SUBMISSION_STATUS_LABELS[s] ?? s}
                  </SelectItem>
                ))}
              </SelectGroup>
            </SelectContent>
          </Select>
        </div>
        <Button size="sm" variant="outline" disabled={loading} onClick={() => void load()}>
          <RefreshCwIcon className={loading ? "animate-spin" : ""} /> 刷新
        </Button>
      </div>

      {error ? (
        <Alert variant="destructive">
          <AlertDescription>
            读取 SRC 台账失败：{error}
            <Button className="ml-2" size="sm" variant="outline" onClick={() => void load()}>
              重试
            </Button>
          </AlertDescription>
        </Alert>
      ) : null}

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
                <TableHead>平台等级</TableHead>
                <TableHead>状态</TableHead>
                <TableHead>编号</TableHead>
                <TableHead>回报</TableHead>
                <TableHead>更新时间</TableHead>
                <TableHead className="text-right">操作</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {loading && subs.length === 0 ? (
                <TableRow>
                  <TableCell colSpan={9} className="py-8">
                    <div className="flex flex-col gap-2">
                      <Skeleton className="h-6 w-full" />
                      <Skeleton className="h-6 w-full" />
                      <Skeleton className="h-6 w-full" />
                    </div>
                  </TableCell>
                </TableRow>
              ) : (
                subs.map((sub) => (
                  <TableRow key={sub.id}>
                    <TableCell className="max-w-72">
                      <span className="block truncate text-xs" title={sub.title}>
                        #{sub.finding_id} {sub.title}
                      </span>
                    </TableCell>
                    <TableCell className="text-xs">{sub.severity || "—"}</TableCell>
                    <TableCell className="text-xs">{sub.platform || "本任务"}</TableCell>
                    <TableCell className="text-xs">{sub.platform_level || "—"}</TableCell>
                    <TableCell>
                      <span
                        className={`inline-flex rounded-full border px-2 py-0.5 text-xs ${submissionTone(sub.status)}`}
                      >
                        {SUBMISSION_STATUS_LABELS[sub.status] ?? sub.status}
                      </span>
                    </TableCell>
                    <TableCell className="font-mono text-xs">{sub.external_id || "—"}</TableCell>
                    <TableCell className="text-xs">{sub.reward || "—"}</TableCell>
                    <TableCell className="text-muted-foreground text-xs">{fmtTime(sub.updated_at)}</TableCell>
                    <TableCell className="text-right whitespace-nowrap">
                      <Button size="sm" variant="ghost" onClick={() => openLedger(sub)}>
                        改状态
                      </Button>
                      <Button asChild size="sm" variant="ghost">
                        <Link href={`/function/tasks/detail?id=${sub.task_id}`}>打开任务</Link>
                      </Button>
                    </TableCell>
                  </TableRow>
                ))
              )}
              {!loading && subs.length === 0 ? (
                <TableRow>
                  <TableCell colSpan={9} className="py-8">
                    <Empty className="py-4">
                      <EmptyHeader>
                        <EmptyTitle>还没有提交台账</EmptyTitle>
                        <EmptyDescription>
                          在任务详情的 SRC 页签里「准备交付件」后，这里就能看到。
                        </EmptyDescription>
                      </EmptyHeader>
                    </Empty>
                  </TableCell>
                </TableRow>
              ) : null}
            </TableBody>
          </Table>
        </CardContent>
      </Card>

      <Dialog
        open={target !== null}
        onOpenChange={(open) => {
          if (!open) setTarget(null);
        }}
      >
        <DialogContent className="sm:max-w-lg">
          <DialogHeader>
            <DialogTitle>更新提交台账</DialogTitle>
            <DialogDescription>{target ? `#${target.finding_id} ${target.title}` : ""}</DialogDescription>
          </DialogHeader>
          <div className="grid gap-4">
            <div className="grid gap-2">
              <Label htmlFor="src-ledger-status">状态</Label>
              <Select value={nextStatus} onValueChange={setNextStatus}>
                <SelectTrigger id="src-ledger-status" className="w-full">
                  <SelectValue placeholder="选择状态" />
                </SelectTrigger>
                <SelectContent>
                  <SelectGroup>
                    {target ? (
                      <SelectItem value={target.status}>
                        {SUBMISSION_STATUS_LABELS[target.status] ?? target.status}（不变）
                      </SelectItem>
                    ) : null}
                    {(SUBMISSION_NEXT[target?.status ?? ""] ?? []).map((next) => (
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
                value={externalID}
                placeholder="标记为已提交时必填，例如 SRC-2026-0001"
                onChange={(e) => setExternalID(e.target.value)}
              />
            </div>
            <div className="grid gap-2">
              <Label htmlFor="src-ledger-reward">回报</Label>
              <Input
                id="src-ledger-reward"
                value={reward}
                placeholder="例如 200 积分 / 500 元"
                onChange={(e) => setReward(e.target.value)}
              />
            </div>
            <div className="grid gap-2">
              <Label htmlFor="src-ledger-note">备注（可选）</Label>
              <Input
                id="src-ledger-note"
                value={note}
                placeholder="例如 审核中，等平台回复"
                onChange={(e) => setNote(e.target.value)}
              />
            </div>
            {saveError ? <p className="text-destructive text-xs [overflow-wrap:anywhere]">{saveError}</p> : null}
          </div>
          <DialogFooter>
            <Button variant="outline" onClick={() => setTarget(null)}>
              取消
            </Button>
            <Button disabled={saving} onClick={() => void submitLedger()}>
              {saving && <Spinner data-icon="inline-start" />}
              保存
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  );
}

// ---- Tab B：平台项目（全局规则集）----

function ProgramsTab() {
  const [programs, setPrograms] = React.useState<SrcProgram[]>([]);
  const [loading, setLoading] = React.useState(true);
  const [error, setError] = React.useState("");

  const [open, setOpen] = React.useState(false);
  const [editing, setEditing] = React.useState<SrcProgram | null>(null);
  const [draft, setDraft] = React.useState<ProgramDraft>(emptyDraft);
  // 编辑时保留本页没有编辑入口的规则字段（max_open_intents / required_areas），
  // 否则 PATCH 整份 rules 会把它们抹掉。
  const [preservedRules, setPreservedRules] = React.useState<SrcProgramRules>({});
  const [formError, setFormError] = React.useState("");
  const [saving, setSaving] = React.useState(false);

  const patch = (p: Partial<ProgramDraft>) => setDraft((d) => ({ ...d, ...p }));

  const load = React.useCallback(async () => {
    setLoading(true);
    try {
      const r = await api.srcPrograms();
      setPrograms(r.programs ?? []);
      setError("");
    } catch (e) {
      setError(errText(e));
    } finally {
      setLoading(false);
    }
  }, []);

  React.useEffect(() => {
    void load();
  }, [load]);

  const openCreate = () => {
    setEditing(null);
    setDraft(emptyDraft());
    setPreservedRules({});
    setFormError("");
    setOpen(true);
  };

  const openEdit = async (p: SrcProgram) => {
    setEditing(p);
    setDraft(draftFromProgram(p));
    setPreservedRules(p.rules ?? {});
    setFormError("");
    setOpen(true);
    try {
      const r = await api.srcProgramScope(p.id);
      setDraft((current) => ({
        ...current,
        scopeRows: r.rows.map((row) => ({
          direction: row.direction,
          kind: row.kind,
          value: row.domain || row.net || "",
          note: row.note || "",
        })),
      }));
    } catch (e) {
      toast.error("读取项目范围失败：" + (e as Error).message);
    }
  };

  const toggleIn = (list: string[], id: string): string[] =>
    list.includes(id) ? list.filter((x) => x !== id) : [...list, id];

  const submit = async () => {
    const name = draft.name.trim();
    if (!name) {
      setFormError("项目名不能为空");
      return;
    }
    const timeWindow = draft.timeWindow.trim();
    if (timeWindow && !TIME_WINDOW_RE.test(timeWindow)) {
      setFormError("作业时间窗格式不对：示例 09:00-18:00 或 09:00-12:00,13:00-18:00");
      return;
    }
    const severityMap: Record<string, string> = {};
    for (const key of SEVERITY_ROWS) {
      const mapped = draft.severityMap[key];
      if (mapped && mapped !== NO_MAPPING) severityMap[key] = mapped;
    }
    const input: SrcProgramInput = {
      company_id: draft.companyID.trim() ? Math.floor(Number(draft.companyID)) || undefined : undefined,
      name,
      platform: draft.platform.trim(),
      scope_note: draft.scopeNote.trim(),
      rules: {
        ...preservedRules,
        forbidden_techniques: draft.forbiddenTechniques,
        forbidden_writes: draft.forbiddenWrites,
        max_intents_total: toCount(draft.maxIntentsTotal),
        max_intents_per_round: toCount(draft.maxIntentsPerRound),
        max_attempts_per_cell: toCount(draft.maxAttemptsPerCell),
        max_intents_per_asset: toCount(draft.maxIntentsPerAsset),
        allow_budget_stop: draft.allowBudgetStop,
        skip_report_gate: draft.skipReportGate,
        command_rules: parseCommandRules(draft.commandRules),
        check_tier: draft.checkTier || undefined,
        check_thresholds: buildThresholds(draft.pathsMin, draft.manualPathsMin),
        no_automation: draft.noAutomation,
        redlines: draft.redlines
          .split("\n")
          .map((s) => s.trim())
          .filter(Boolean),
        max_records_read: toCount(draft.maxRecordsRead),
        require_screenshots: draft.requireScreenshots,
        strict_layout: draft.strictLayout,
        egress_ip: draft.egressIP.trim(),
        test_accounts: draft.testAccounts.trim(),
        platform_template: draft.platformTemplate || undefined,
      },
      rate_limit: toCount(draft.rateLimit),
      time_window: timeWindow,
      retention_days: toCount(draft.retentionDays),
      severity_map: severityMap,
    };
    setSaving(true);
    setFormError("");
    try {
      const saved = editing ? await api.updateSrcProgram(editing.id, input) : await api.createSrcProgram(input);
      try {
        await api.saveSrcProgramScope(saved.id, draft.scopeRows);
      } catch (e) {
        setEditing(saved);
        await load();
        setFormError("项目已保存，但显式范围保存失败：" + (e as Error).message);
        return;
      }
      toast.success(editing ? "项目已更新" : "项目已创建");
      setOpen(false);
      setEditing(null);
      await load();
    } catch (e) {
      setFormError(errText(e));
    } finally {
      setSaving(false);
    }
  };

  const remove = async (p: SrcProgram) => {
    if (!window.confirm(`删除项目「${p.name}」？已建任务不受影响，之后建任务不能再绑定它。`)) return;
    try {
      await api.deleteSrcProgram(p.id);
      toast.success("项目已删除");
      await load();
    } catch (e) {
      toast.error(`删除失败：${errText(e)}`);
    }
  };

  return (
    <div className="flex flex-col gap-3">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <p className="text-muted-foreground text-xs">
          项目 = 一个平台的规则集：禁用手段/写操作、限速、时间窗、等级映射与必做面，建 SRC 任务时绑定。
        </p>
        <div className="flex items-center gap-2">
          <Button size="sm" variant="outline" disabled={loading} onClick={() => void load()}>
            <RefreshCwIcon className={loading ? "animate-spin" : ""} /> 刷新
          </Button>
          <Button size="sm" onClick={openCreate}>
            <PlusIcon /> 新建项目
          </Button>
        </div>
      </div>

      {error ? (
        <Alert variant="destructive">
          <AlertDescription>
            读取平台项目失败：{error}
            <Button className="ml-2" size="sm" variant="outline" onClick={() => void load()}>
              重试
            </Button>
          </AlertDescription>
        </Alert>
      ) : null}

      <Card className="gap-0 overflow-hidden py-0">
        <CardContent className="px-0">
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead className="min-w-[180px]">项目名</TableHead>
                <TableHead>平台</TableHead>
                <TableHead>限速</TableHead>
                <TableHead>时间窗</TableHead>
                <TableHead>保留</TableHead>
                <TableHead>禁用手段</TableHead>
                <TableHead className="min-w-[200px]">等级映射</TableHead>
                <TableHead className="w-32 text-right">操作</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {loading && programs.length === 0 ? (
                <TableRow>
                  <TableCell colSpan={8} className="py-12 text-center text-muted-foreground">
                    加载中…
                  </TableCell>
                </TableRow>
              ) : null}
              {programs.map((p) => {
                const techniques = p.rules?.forbidden_techniques ?? [];
                const writes = p.rules?.forbidden_writes ?? [];
                return (
                  <TableRow key={p.id}>
                    <TableCell>
                      <div className="flex min-w-0 flex-col gap-0.5">
                        <span className="truncate font-medium text-sm" title={p.name}>
                          {p.name}
                        </span>
                        {p.company_id ? (
                          <span className="text-muted-foreground text-xs">企业 #{p.company_id}</span>
                        ) : null}
                        {p.feedback && p.feedback.submitted > 0 ? (
                          <span className="text-muted-foreground text-xs">
                            已交 {p.feedback.submitted} · 受理 {p.feedback.accepted} · 驳回 {p.feedback.rejected} · 重复{" "}
                            {p.feedback.duplicate}
                          </span>
                        ) : null}
                      </div>
                    </TableCell>
                    <TableCell className="text-xs">{p.platform || "—"}</TableCell>
                    <TableCell className="text-xs">{p.rate_limit ? `${p.rate_limit} qps` : "不限"}</TableCell>
                    <TableCell className="text-xs">{p.time_window || "不限"}</TableCell>
                    <TableCell className="text-xs">
                      {p.retention_days ? `${p.retention_days} 天` : "跟随全局"}
                    </TableCell>
                    <TableCell>
                      <div className="flex flex-wrap items-center gap-1">
                        <Badge variant="outline" title={techniques.join(", ") || "未禁用任何手段"}>
                          {techniques.length} 手段
                        </Badge>
                        {writes.length > 0 ? (
                          <Badge variant="secondary" title={writes.join(", ")}>
                            {writes.length} 写操作
                          </Badge>
                        ) : null}
                      </div>
                    </TableCell>
                    <TableCell className="text-xs [overflow-wrap:anywhere]">{severityMapText(p.severity_map)}</TableCell>
                    <TableCell className="text-right whitespace-nowrap">
                      <Button size="sm" variant="ghost" onClick={() => openEdit(p)}>
                        <PencilIcon /> 编辑
                      </Button>
                      <Button size="sm" variant="ghost" onClick={() => void remove(p)}>
                        <Trash2Icon /> 删除
                      </Button>
                    </TableCell>
                  </TableRow>
                );
              })}
              {!loading && programs.length === 0 ? (
                <TableRow>
                  <TableCell colSpan={7} className="py-8">
                    <Empty className="py-4">
                      <EmptyHeader>
                        <EmptyTitle>还没有平台项目</EmptyTitle>
                        <EmptyDescription>
                          新建一个，把平台规则（禁用手段/限速/时间窗/等级映射）固化下来，建 SRC 任务时直接绑定。
                        </EmptyDescription>
                      </EmptyHeader>
                    </Empty>
                  </TableCell>
                </TableRow>
              ) : null}
            </TableBody>
          </Table>
        </CardContent>
      </Card>

      <Dialog
        open={open}
        onOpenChange={(next) => {
          setOpen(next);
          if (!next) setEditing(null);
        }}
      >
        <DialogContent className="flex max-h-[88vh] flex-col gap-0 overflow-hidden p-0 sm:max-w-2xl">
          <DialogHeader className="border-b p-6">
            <DialogTitle>{editing ? `编辑项目：${editing.name}` : "新建 SRC 平台项目"}</DialogTitle>
            <DialogDescription>
              规则会编译进绑定任务的提示词与命令拦截：项目规则改动不影响已建任务。
            </DialogDescription>
          </DialogHeader>

          <div className="min-h-0 flex-1 overflow-y-auto p-6">
            <div className="grid gap-4">
              <div className="grid gap-2">
                <Label htmlFor="program-name">项目名 *</Label>
                <Input
                  id="program-name"
                  value={draft.name}
                  placeholder="例如 Acme 众测 2026Q1"
                  onChange={(e) => patch({ name: e.target.value })}
                />
              </div>
              <div className="grid gap-2">
                <Label htmlFor="program-platform">平台</Label>
                <Input
                  id="program-platform"
                  value={draft.platform}
                  placeholder="例如 某众测平台 / 厂商 SRC"
                  onChange={(e) => patch({ platform: e.target.value })}
                />
              </div>
              <div className="grid gap-2">
                <Label htmlFor="program-template">提交块模板</Label>
                <Select value={draft.platformTemplate || "auto"} onValueChange={(v) => patch({ platformTemplate: v === "auto" ? "" : v })}>
                  <SelectTrigger id="program-template">
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    <SelectItem value="auto">自动识别（按平台名）</SelectItem>
                    <SelectItem value="generic">通用</SelectItem>
                    <SelectItem value="butian">补天</SelectItem>
                    <SelectItem value="vulbox">漏洞盒子</SelectItem>
                    <SelectItem value="edu">教育 SRC</SelectItem>
                    <SelectItem value="hackerone">HackerOne</SelectItem>
                  </SelectContent>
                </Select>
                <p className="text-muted-foreground text-xs">
                  只决定【提交信息】块的字段名/顺序，正文仍是同一份 8 块报告；模板内置日期见提交块，提交前按平台最新规则复核。
                </p>
              </div>
              <div className="grid gap-2">
                <Label htmlFor="program-company">关联企业 ID</Label>
                <Input
                  id="program-company"
                  type="number"
                  min={1}
                  value={draft.companyID}
                  placeholder="company_id"
                  onChange={(e) => patch({ companyID: e.target.value })}
                />
                <p className="text-muted-foreground text-xs">可选：绑定企业后便于按公司看资产</p>
              </div>
              <div className="grid gap-2">
                <Label htmlFor="program-scope">范围说明</Label>
                <Textarea
                  id="program-scope"
                  rows={2}
                  value={draft.scopeNote}
                  placeholder="例如 仅 *.acme.com 与 Acme App；明确排除测试环境与第三方托管页"
                  onChange={(e) => patch({ scopeNote: e.target.value })}
                />
                <p className="text-muted-foreground text-xs">
                  写清哪些域名/APP/接口在范围内、哪些明确排除；会进 agent 提示词
                </p>
              </div>
              <div className="grid gap-2 rounded-lg border p-3">
                <div className="flex items-center justify-between gap-2">
                  <div className="grid gap-0.5">
                    <Label className="text-sm">显式 in / out 范围</Label>
                    <p className="text-muted-foreground text-xs">
                      in 编译进 task_scope，out 编译进 scope 排除表（优先于公司/根域包含）；最多 500 条。
                    </p>
                  </div>
                  <Button
                    type="button"
                    size="sm"
                    variant="outline"
                    onClick={() =>
                      patch({
                        scopeRows: [...draft.scopeRows, { direction: "in", kind: "root_domain", value: "", note: "" }],
                      })
                    }
                  >
                    新增一行
                  </Button>
                </div>
                {draft.scopeRows.length === 0 ? (
                  <p className="text-muted-foreground py-2 text-xs">未配置：只使用关联企业/范围说明。</p>
                ) : (
                  <div className="grid gap-2">
                    {draft.scopeRows.map((row, idx) => (
                      <div key={idx} className="grid gap-2 sm:grid-cols-[5rem_8rem_minmax(0,1fr)_minmax(0,1fr)_auto]">
                        <Select
                          value={row.direction}
                          onValueChange={(v) =>
                            patch({
                              scopeRows: draft.scopeRows.map((x, i) =>
                                i === idx ? { ...x, direction: v as ProgramScopeInput["direction"] } : x,
                              ),
                            })
                          }
                        >
                          <SelectTrigger aria-label="方向">
                            <SelectValue />
                          </SelectTrigger>
                          <SelectContent>
                            <SelectItem value="in">in</SelectItem>
                            <SelectItem value="out">out</SelectItem>
                          </SelectContent>
                        </Select>
                        <Select
                          value={row.kind}
                          onValueChange={(v) =>
                            patch({
                              scopeRows: draft.scopeRows.map((x, i) =>
                                i === idx ? { ...x, kind: v as ProgramScopeInput["kind"] } : x,
                              ),
                            })
                          }
                        >
                          <SelectTrigger aria-label="类型">
                            <SelectValue />
                          </SelectTrigger>
                          <SelectContent>
                            <SelectItem value="root_domain">根域名</SelectItem>
                            <SelectItem value="subdomain">子域名</SelectItem>
                            <SelectItem value="ip">IP</SelectItem>
                            <SelectItem value="cidr">CIDR</SelectItem>
                          </SelectContent>
                        </Select>
                        <Input
                          value={row.value}
                          placeholder={
                            row.kind === "ip" || row.kind === "cidr" ? "10.0.0.0/24 或 1.2.3.4" : "example.com"
                          }
                          onChange={(e) =>
                            patch({
                              scopeRows: draft.scopeRows.map((x, i) => (i === idx ? { ...x, value: e.target.value } : x)),
                            })
                          }
                        />
                        <Input
                          value={row.note ?? ""}
                          placeholder="备注（可选）"
                          onChange={(e) =>
                            patch({
                              scopeRows: draft.scopeRows.map((x, i) => (i === idx ? { ...x, note: e.target.value } : x)),
                            })
                          }
                        />
                        <Button
                          type="button"
                          size="sm"
                          variant="ghost"
                          onClick={() => patch({ scopeRows: draft.scopeRows.filter((_, i) => i !== idx) })}
                        >
                          删除
                        </Button>
                      </div>
                    ))}
                  </div>
                )}
              </div>
              <div className="grid gap-4 sm:grid-cols-2">
                <div className="grid gap-2">
                  <Label htmlFor="program-rate">限速</Label>
                  <Input
                    id="program-rate"
                    type="number"
                    min={0}
                    value={draft.rateLimit}
                    placeholder="0 = 不限"
                    onChange={(e) => patch({ rateLimit: e.target.value })}
                  />
                  <p className="text-muted-foreground text-xs">
                    &gt;0 时会编译成&quot;禁洪水/高并发&quot;的命令级硬规则
                  </p>
                </div>
                <div className="grid gap-2">
                  <Label htmlFor="program-retention">任务产物/日志保留</Label>
                  <Input
                    id="program-retention"
                    type="number"
                    min={0}
                    value={draft.retentionDays}
                    placeholder="0 = 跟随全局"
                    onChange={(e) => patch({ retentionDays: e.target.value })}
                  />
                  <p className="text-muted-foreground text-xs">
                    该 program 的任务目录、转写与数据库日志保留天数；0 = 跟随存储清理设置
                  </p>
                </div>
                <div className="grid gap-2">
                  <Label htmlFor="program-window">作业时间窗</Label>
                  <Input
                    id="program-window"
                    value={draft.timeWindow}
                    placeholder="09:00-18:00 或 09:00-12:00,13:00-18:00"
                    onChange={(e) => patch({ timeWindow: e.target.value })}
                  />
                  <p className="text-muted-foreground text-xs">留空 = 不限；格式 HH:MM-HH:MM，多段用逗号分隔</p>
                </div>
              </div>

              <div className="grid gap-2">
                <span className="text-sm font-medium">禁用手段</span>
                <div className="flex flex-wrap gap-1.5">
                  {TECHNIQUE_OPTIONS.map((opt) => (
                    <ToggleChip
                      key={opt.id}
                      on={draft.forbiddenTechniques.includes(opt.id)}
                      label={opt.label}
                      title={opt.id}
                      onClick={() => patch({ forbiddenTechniques: toggleIn(draft.forbiddenTechniques, opt.id) })}
                    />
                  ))}
                </div>
              </div>

              <div className="grid gap-2">
                <span className="text-sm font-medium">敏感写操作</span>
                <div className="flex flex-wrap gap-1.5">
                  {WRITE_OPTIONS.map((opt) => (
                    <ToggleChip
                      key={opt.id}
                      on={draft.forbiddenWrites.includes(opt.id)}
                      label={opt.label}
                      title={opt.id}
                      onClick={() => patch({ forbiddenWrites: toggleIn(draft.forbiddenWrites, opt.id) })}
                    />
                  ))}
                </div>
              </div>

              <div className="grid gap-2">
                <span className="text-sm font-medium">等级映射</span>
                <p className="text-muted-foreground text-xs">
                  内部等级 → 平台等级；不映射的档位不进 severity_map（台账里显示平台等级用）。
                </p>
                <div className="grid gap-2">
                  {SEVERITY_ROWS.map((key) => (
                    <div key={key} className="flex items-center gap-2">
                      <span className="w-32 text-xs">{SEVERITY_ROW_LABELS[key]}</span>
                      <Select
                        value={draft.severityMap[key] ?? NO_MAPPING}
                        onValueChange={(v) => patch({ severityMap: { ...draft.severityMap, [key]: v } })}
                      >
                        <SelectTrigger className="w-40" aria-label={`${key} 等级映射`}>
                          <SelectValue />
                        </SelectTrigger>
                        <SelectContent>
                          <SelectGroup>
                            <SelectItem value={NO_MAPPING}>不映射</SelectItem>
                            {PLATFORM_LEVELS.map((level) => (
                              <SelectItem key={level} value={level}>
                                {level}
                              </SelectItem>
                            ))}
                          </SelectGroup>
                        </SelectContent>
                      </Select>
                    </div>
                  ))}
                </div>
              </div>

              <details className="rounded-md border p-3">
                <summary className="cursor-pointer text-sm font-medium">收尾闸与平台档（F2）</summary>
                <div className="grid gap-4 pt-4">
                  <div className="grid gap-2">
                    <Label htmlFor="program-check-tier">收尾闸档位</Label>
                    <Select
                      value={draft.checkTier || "default"}
                      onValueChange={(v) =>
                        patch({ checkTier: (v === "default" ? "" : v) as "" | "full" | "platform" | "off" })
                      }
                    >
                      <SelectTrigger id="program-check-tier" className="w-full">
                        <SelectValue />
                      </SelectTrigger>
                      <SelectContent>
                        <SelectGroup>
                          <SelectItem value="default">跟随任务默认（full）</SelectItem>
                          <SelectItem value="full">full：路径枚举 ≥200 条等全量门槛</SelectItem>
                          <SelectItem value="platform">platform：手工清单替代批量枚举（平台档案档）</SelectItem>
                          <SelectItem value="off">off：关掉收尾闸（应急）</SelectItem>
                        </SelectGroup>
                      </SelectContent>
                    </Select>
                    <p className="text-muted-foreground text-xs">
                      platform 档只豁免"会产生批量流量的枚举手段"，JS 抽全 / 参数变体 / 防护对照仍然必做。
                    </p>
                  </div>
                  <div className="grid gap-4 sm:grid-cols-2">
                    <div className="grid gap-2">
                      <Label htmlFor="program-paths-min">路径枚举门槛</Label>
                      <Input
                        id="program-paths-min"
                        type="number"
                        min={0}
                        value={draft.pathsMin}
                        placeholder="默认 200"
                        onChange={(e) => patch({ pathsMin: e.target.value })}
                      />
                    </div>
                    <div className="grid gap-2">
                      <Label htmlFor="program-manual-paths">手工路径门槛</Label>
                      <Input
                        id="program-manual-paths"
                        type="number"
                        min={0}
                        value={draft.manualPathsMin}
                        placeholder="platform 档默认 1"
                        onChange={(e) => patch({ manualPathsMin: e.target.value })}
                      />
                    </div>
                  </div>
                  <div className="grid gap-2">
                    <Label htmlFor="program-redlines">平台红线（一行一条，进提示词）</Label>
                    <Textarea
                      id="program-redlines"
                      className="min-h-24"
                      value={draft.redlines}
                      placeholder={"越权读取不超过 5 组\nAK/SK 只提交不深挖\n禁内网渗透/横向"}
                      onChange={(e) => patch({ redlines: e.target.value })}
                    />
                  </div>
                  <div className="flex flex-wrap gap-2">
                    <ToggleChip
                      on={draft.noAutomation}
                      label="禁自动化扫描器"
                      title="编译成命令规则：禁 nuclei/awvs/nmap -p-/ffuf -w 等"
                      onClick={() => patch({ noAutomation: !draft.noAutomation })}
                    />
                    <ToggleChip
                      on={draft.requireScreenshots}
                      label="必须截图"
                      title="收尾项 screenshots 不再允许 na"
                      onClick={() => patch({ requireScreenshots: !draft.requireScreenshots })}
                    />
                    <ToggleChip
                      on={draft.strictLayout}
                      label="严格 8 块版式"
                      title="导出时备注区退出正文"
                      onClick={() => patch({ strictLayout: !draft.strictLayout })}
                    />
                  </div>
                  <div className="grid gap-4 sm:grid-cols-3">
                    <div className="grid gap-2">
                      <Label htmlFor="program-max-records">越权读取上限</Label>
                      <Input
                        id="program-max-records"
                        type="number"
                        min={0}
                        value={draft.maxRecordsRead}
                        placeholder="5"
                        onChange={(e) => patch({ maxRecordsRead: e.target.value })}
                      />
                    </div>
                    <div className="grid gap-2">
                      <Label htmlFor="program-egress">测试源 IP</Label>
                      <Input
                        id="program-egress"
                        value={draft.egressIP}
                        placeholder="1.2.3.4（提交块用）"
                        onChange={(e) => patch({ egressIP: e.target.value })}
                      />
                    </div>
                    <div className="grid gap-2">
                      <Label htmlFor="program-accounts">测试账号</Label>
                      <Input
                        id="program-accounts"
                        value={draft.testAccounts}
                        placeholder="越权类必填"
                        onChange={(e) => patch({ testAccounts: e.target.value })}
                      />
                    </div>
                  </div>
                </div>
              </details>

              <details className="rounded-md border p-3">
                <summary className="cursor-pointer text-sm font-medium">高级规则（额度 / 预算停 / 命令级规则）</summary>
                <div className="grid gap-4 pt-4">
                  <div className="grid gap-4 sm:grid-cols-2">
                    <div className="grid gap-2">
                      <Label htmlFor="program-max-total">意图总数上限</Label>
                      <Input
                        id="program-max-total"
                        type="number"
                        min={0}
                        value={draft.maxIntentsTotal}
                        placeholder="0 = 默认"
                        onChange={(e) => patch({ maxIntentsTotal: e.target.value })}
                      />
                    </div>
                    <div className="grid gap-2">
                      <Label htmlFor="program-max-round">每轮意图上限</Label>
                      <Input
                        id="program-max-round"
                        type="number"
                        min={0}
                        value={draft.maxIntentsPerRound}
                        placeholder="0 = 默认"
                        onChange={(e) => patch({ maxIntentsPerRound: e.target.value })}
                      />
                    </div>
                    <div className="grid gap-2">
                      <Label htmlFor="program-max-cell">每格尝试上限</Label>
                      <Input
                        id="program-max-cell"
                        type="number"
                        min={0}
                        value={draft.maxAttemptsPerCell}
                        placeholder="0 = 默认"
                        onChange={(e) => patch({ maxAttemptsPerCell: e.target.value })}
                      />
                    </div>
                    <div className="grid gap-2">
                      <Label htmlFor="program-max-asset">每资产意图上限</Label>
                      <Input
                        id="program-max-asset"
                        type="number"
                        min={0}
                        value={draft.maxIntentsPerAsset}
                        placeholder="0 = 默认"
                        onChange={(e) => patch({ maxIntentsPerAsset: e.target.value })}
                      />
                    </div>
                  </div>
                  <label htmlFor="program-budget-stop" className="flex items-center gap-2 text-sm">
                    <Checkbox
                      id="program-budget-stop"
                      checked={draft.allowBudgetStop}
                      onCheckedChange={(v) => patch({ allowBudgetStop: !!v })}
                    />
                    允许因额度耗尽收尾（allow_budget_stop）
                  </label>
                  <label htmlFor="program-skip-gate" className="flex items-center gap-2 text-sm">
                    <Checkbox
                      id="program-skip-gate"
                      checked={draft.skipReportGate}
                      onCheckedChange={(v) => patch({ skipReportGate: !!v })}
                    />
                    跳过交付件门（skip_report_gate）
                  </label>
                  <div className="grid gap-2">
                    <Label htmlFor="program-command-rules">命令级规则</Label>
                    <Textarea
                      id="program-command-rules"
                      rows={4}
                      value={draft.commandRules}
                      placeholder={"一行一条，label|pattern 或只写 pattern，例如：\n禁止目录爆破|--force-brute"}
                      onChange={(e) => patch({ commandRules: e.target.value })}
                    />
                    <p className="text-muted-foreground text-xs">
                      按非正则关键字处理（命中即拦）；需要正则请走后端配置。
                    </p>
                  </div>
                </div>
              </details>

              {formError ? <p className="text-destructive text-xs [overflow-wrap:anywhere]">{formError}</p> : null}
            </div>
          </div>

          <DialogFooter className="flex-row justify-end gap-2 border-t p-4">
            <Button variant="outline" onClick={() => setOpen(false)}>
              取消
            </Button>
            <Button disabled={saving || !draft.name.trim()} onClick={() => void submit()}>
              {saving && <Spinner data-icon="inline-start" />}
              {editing ? "保存" : "创建"}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </div>
  );
}

export default function SrcWorkbenchPage() {
  const [tab, setTab] = React.useState("ledger");

  return (
    <Tabs value={tab} onValueChange={setTab} className="gap-3">
      <div className="flex flex-wrap items-end justify-between gap-3">
        <div>
          <h1 className="flex items-center gap-2 font-semibold text-lg">
            <BugIcon className="size-5" />
            SRC 工作台
          </h1>
          <p className="mt-1 text-muted-foreground text-sm">
            按平台项目对账：哪些发现准备好了、交到哪一步、项目规则是什么。
          </p>
        </div>
        <TabsList variant="default">
          <TabsTrigger value="ledger">提交台账</TabsTrigger>
          <TabsTrigger value="programs">平台项目</TabsTrigger>
        </TabsList>
      </div>
      <TabsContent value="ledger" className="mt-0">
        <LedgerTab />
      </TabsContent>
      <TabsContent value="programs" className="mt-0">
        <ProgramsTab />
      </TabsContent>
    </Tabs>
  );
}

/** 收尾闸门槛：只写用户填了的项（空 = 用档位默认）。 */
function buildThresholds(pathsMin: string, manualPathsMin: string): Record<string, number> | undefined {
  const out: Record<string, number> = {};
  const p = Math.floor(Number(pathsMin) || 0);
  const m = Math.floor(Number(manualPathsMin) || 0);
  if (p > 0) out.paths = p;
  if (m > 0) out.manual_paths = m;
  return Object.keys(out).length > 0 ? out : undefined;
}