"use client";

import * as React from "react";

import { PlusIcon, RefreshCwIcon, ScaleIcon } from "lucide-react";
import { toast } from "sonner";

import { Field, SubBlock } from "@/components/page-sections";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { api } from "@/lib/api";
import type { CompanyProfile, RatingRule } from "@/lib/types";

const SEVERITIES = ["critical", "high", "medium", "low", "info"];
const SEV_LABEL: Record<string, string> = {
  critical: "严重",
  high: "高危",
  medium: "中危",
  low: "低危",
  info: "信息",
};
const SEV_VARIANT: Record<string, "destructive" | "default" | "secondary" | "outline"> = {
  critical: "destructive",
  high: "destructive",
  medium: "default",
  low: "secondary",
  info: "outline",
};

/** 定级规则：公司级优先于通用；reviewer/ops agent 与报告定级共用。 */
export default function RulesPage() {
  const [profiles, setProfiles] = React.useState<CompanyProfile[]>([]);
  const [companyId, setCompanyId] = React.useState<string>("all");
  const [rows, setRows] = React.useState<RatingRule[]>([]);
  const [loading, setLoading] = React.useState(false);
  const [saving, setSaving] = React.useState(false);
  const [form, setForm] = React.useState({
    company_id: 0,
    vulnclass: "",
    severity: "high",
    note: "",
  });

  const load = React.useCallback(() => {
    setLoading(true);
    api
      .ratingRules(companyId === "all" ? undefined : Number(companyId))
      .then((r) => setRows(r.rules))
      .catch((e: Error) => toast.error(`加载定级规则失败：${e.message}`))
      .finally(() => setLoading(false));
  }, [companyId]);

  React.useEffect(() => {
    api
      .companyProfiles()
      .then((r) => setProfiles(r.profiles))
      .catch(() => {});
  }, []);
  React.useEffect(() => {
    load();
  }, [load]);

  async function save() {
    if (!form.vulnclass.trim()) {
      toast.error("洞型必填");
      return;
    }
    setSaving(true);
    try {
      await api.saveRatingRule(form);
      toast.success("已保存（同公司 + 洞型会覆盖）");
      setForm({ ...form, vulnclass: "", note: "" });
      load();
    } catch (e) {
      toast.error(`保存失败：${(e as Error).message}`);
    } finally {
      setSaving(false);
    }
  }

  async function remove(r: RatingRule) {
    if (!window.confirm(`删除规则「${r.vulnclass} → ${r.severity}」？`)) return;
    try {
      await api.deleteRatingRule(r.id);
      toast.success("已删除");
      load();
    } catch (e) {
      toast.error(`删除失败：${(e as Error).message}`);
    }
  }

  return (
    <div className="flex flex-col gap-4">
      <div className="flex flex-wrap items-end justify-between gap-3">
        <div>
          <h1 className="flex items-center gap-2 font-semibold text-lg">
            <ScaleIcon className="size-5" />
            规则与定级
          </h1>
          <p className="mt-1 text-muted-foreground text-sm">
            洞型 → 建议等级。公司规则优先于通用规则；任务里的厂商档案会带上这些口径，report_finding 的等级应与之一致。
          </p>
        </div>
        <div className="flex items-center gap-2">
          <Select value={companyId} onValueChange={setCompanyId}>
            <SelectTrigger size="sm" className="w-48">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="all">全部（含通用）</SelectItem>
              {profiles.map((p) => (
                <SelectItem key={p.company_id} value={String(p.company_id)}>
                  {p.company_name}
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

      <Card className="p-4">
        <SubBlock
          title="新建 / 更新规则"
          actions={
            <Button size="sm" onClick={save} disabled={saving}>
              <PlusIcon className="size-4" />
              保存规则
            </Button>
          }
        >
          <div className="grid gap-4 md:grid-cols-2 lg:grid-cols-4">
            <Field label="适用范围">
              <Select
                value={String(form.company_id)}
                onValueChange={(v) => setForm({ ...form, company_id: Number(v) })}
              >
                <SelectTrigger>
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="0">通用规则</SelectItem>
                  {profiles.map((p) => (
                    <SelectItem key={p.company_id} value={String(p.company_id)}>
                      {p.company_name}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </Field>
            <Field label="洞型" hint="slug，如 idor / ssrf / stored-xss。">
              <Input
                placeholder="idor"
                value={form.vulnclass}
                onChange={(e) => setForm({ ...form, vulnclass: e.target.value })}
              />
            </Field>
            <Field label="建议等级">
              <Select value={form.severity} onValueChange={(v) => setForm({ ...form, severity: v })}>
                <SelectTrigger>
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  {SEVERITIES.map((s) => (
                    <SelectItem key={s} value={s}>
                      {SEV_LABEL[s] ?? s}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </Field>
            <Field label="口径说明" hint="可选；会注入厂商上下文。">
              <Input value={form.note} onChange={(e) => setForm({ ...form, note: e.target.value })} />
            </Field>
          </div>
        </SubBlock>
      </Card>

      <Card className="overflow-hidden">
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead className="w-12">#</TableHead>
              <TableHead className="w-44">范围</TableHead>
              <TableHead className="w-56">洞型</TableHead>
              <TableHead className="w-24">等级</TableHead>
              <TableHead>说明</TableHead>
              <TableHead className="w-20">操作</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {rows.length === 0 ? (
              <TableRow>
                <TableCell colSpan={6} className="text-muted-foreground py-12 text-center text-sm">
                  {loading ? "加载中…" : "暂无规则"}
                </TableCell>
              </TableRow>
            ) : (
              rows.map((r) => {
                const company = profiles.find((p) => p.company_id === r.company_id);
                return (
                  <TableRow key={r.id}>
                    <TableCell className="text-muted-foreground text-xs">{r.id}</TableCell>
                    <TableCell>
                      <Badge variant="secondary">{company ? company.company_name : "通用"}</Badge>
                    </TableCell>
                    <TableCell className="font-mono text-xs">{r.vulnclass}</TableCell>
                    <TableCell>
                      <Badge variant={SEV_VARIANT[r.severity] ?? "default"}>
                        {SEV_LABEL[r.severity] ?? r.severity}
                      </Badge>
                    </TableCell>
                    <TableCell className="text-muted-foreground text-xs">{r.note || r.title || "—"}</TableCell>
                    <TableCell>
                      <Button size="sm" variant="ghost" onClick={() => remove(r)}>
                        删除
                      </Button>
                    </TableCell>
                  </TableRow>
                );
              })
            )}
          </TableBody>
        </Table>
      </Card>
    </div>
  );
}
