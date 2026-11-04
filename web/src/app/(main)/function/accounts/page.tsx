"use client";

import * as React from "react";

import { KeyRoundIcon, PlusIcon, RefreshCwIcon } from "lucide-react";
import { toast } from "sonner";

import { Field } from "@/components/page-sections";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
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
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { api } from "@/lib/api";
import type { CompanyProfile, TestAccount } from "@/lib/types";

const ROLES = ["admin", "normal", "readonly", "vip"];
const ROLE_LABELS: Record<string, string> = {
  admin: "管理员",
  normal: "普通用户",
  readonly: "只读",
  vip: "VIP",
};
const CREDS = ["cookie", "token", "password"];

function fmtTime(s?: string) {
  if (!s) return "—";
  return new Date(s).toLocaleString("zh-CN", { month: "2-digit", day: "2-digit", hour: "2-digit", minute: "2-digit" });
}

/** 测试账号池：凭据只写不读（列表掩码），agent 通过 get_account_credential 取明文。 */
export default function AccountsPage() {
  const [profiles, setProfiles] = React.useState<CompanyProfile[]>([]);
  const [companyId, setCompanyId] = React.useState<string>("all");
  const [rows, setRows] = React.useState<TestAccount[]>([]);
  const [loading, setLoading] = React.useState(false);
  const [open, setOpen] = React.useState(false);
  const [saving, setSaving] = React.useState(false);
  const [form, setForm] = React.useState({
    company_id: 0,
    label: "",
    role_tier: "normal",
    cred_type: "cookie",
    credential: "",
    owner_phone: "",
  });

  const load = React.useCallback(() => {
    setLoading(true);
    api
      .accounts(companyId === "all" ? undefined : Number(companyId))
      .then((r) => setRows(r.accounts))
      .catch((e: Error) => toast.error(`加载账号失败：${e.message}`))
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

  async function create() {
    if (!form.company_id || !form.label.trim() || !form.credential.trim()) {
      toast.error("公司、标签与凭据必填");
      return;
    }
    setSaving(true);
    try {
      await api.createAccount(form);
      toast.success("账号已创建（凭据只写不读）");
      setOpen(false);
      setForm({ company_id: 0, label: "", role_tier: "normal", cred_type: "cookie", credential: "", owner_phone: "" });
      load();
    } catch (e) {
      toast.error(`创建失败：${(e as Error).message}`);
    } finally {
      setSaving(false);
    }
  }

  async function check(a: TestAccount) {
    try {
      const r = await api.checkAccount(a.id);
      toast[r.ok ? "success" : "error"](r.note || (r.ok ? "有效" : "失效"));
      load();
    } catch (e) {
      toast.error(`探活失败：${(e as Error).message}`);
    }
  }

  async function rotate(a: TestAccount) {
    const v = window.prompt(`为「${a.label}」输入新凭据（明文只写不读）`);
    if (!v) return;
    try {
      await api.rotateAccountCredential(a.id, v);
      toast.success("凭据已轮换");
      load();
    } catch (e) {
      toast.error(`轮换失败：${(e as Error).message}`);
    }
  }

  async function remove(a: TestAccount) {
    if (!window.confirm(`删除账号「${a.label}」？`)) return;
    try {
      await api.deleteAccount(a.id);
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
            <KeyRoundIcon className="size-5" />
            测试账号
          </h1>
          <p className="mt-1 text-muted-foreground text-sm">
            公司维度的 cookie/token/password 账号池：列表只显示掩码；探活走公司档案里的 check_url，10
            分钟内重复检查直接返回上次结果。
          </p>
        </div>
        <div className="flex items-center gap-2">
          <Select value={companyId} onValueChange={setCompanyId}>
            <SelectTrigger size="sm" className="w-48">
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="all">全部公司</SelectItem>
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
          <Dialog open={open} onOpenChange={setOpen}>
            <DialogTrigger asChild>
              <Button size="sm">
                <PlusIcon className="size-4" />
                新建
              </Button>
            </DialogTrigger>
            <DialogContent className="sm:max-w-2xl">
              <DialogHeader>
                <DialogTitle>新建测试账号</DialogTitle>
                <DialogDescription>凭据写入后不再回显；需要时由 agent 通过受审计的工具读取。</DialogDescription>
              </DialogHeader>
              <div className="grid gap-4">
                <Field label="公司">
                  <Select
                    value={String(form.company_id)}
                    onValueChange={(v) => setForm({ ...form, company_id: Number(v) })}
                  >
                    <SelectTrigger>
                      <SelectValue placeholder="选择公司" />
                    </SelectTrigger>
                    <SelectContent>
                      {profiles.map((p) => (
                        <SelectItem key={p.company_id} value={String(p.company_id)}>
                          {p.company_name}
                        </SelectItem>
                      ))}
                    </SelectContent>
                  </Select>
                </Field>
                <Field label="标签">
                  <Input
                    placeholder="如 测试管理员"
                    autoComplete="off"
                    value={form.label}
                    onChange={(e) => setForm({ ...form, label: e.target.value })}
                  />
                </Field>
                <div className="grid gap-4 sm:grid-cols-2">
                  <Field label="角色档">
                    <Select value={form.role_tier} onValueChange={(v) => setForm({ ...form, role_tier: v })}>
                      <SelectTrigger>
                        <SelectValue />
                      </SelectTrigger>
                      <SelectContent>
                        {ROLES.map((r) => (
                          <SelectItem key={r} value={r}>
                            {ROLE_LABELS[r] ?? r}
                          </SelectItem>
                        ))}
                      </SelectContent>
                    </Select>
                  </Field>
                  <Field label="凭据类型">
                    <Select value={form.cred_type} onValueChange={(v) => setForm({ ...form, cred_type: v })}>
                      <SelectTrigger>
                        <SelectValue />
                      </SelectTrigger>
                      <SelectContent>
                        {CREDS.map((c) => (
                          <SelectItem key={c} value={c}>
                            {c}
                          </SelectItem>
                        ))}
                      </SelectContent>
                    </Select>
                  </Field>
                </div>
                <Field label="凭据" hint="cookie 串 / token / password；明文只写不读。">
                  <Input
                    type="password"
                    autoComplete="new-password"
                    value={form.credential}
                    onChange={(e) => setForm({ ...form, credential: e.target.value })}
                  />
                </Field>
                <Field label="本人手机号（可选）" hint="真实短信/支付类红线核对用。">
                  <Input
                    autoComplete="off"
                    value={form.owner_phone}
                    onChange={(e) => setForm({ ...form, owner_phone: e.target.value })}
                  />
                </Field>
              </div>
              <DialogFooter>
                <Button onClick={create} disabled={saving}>
                  创建
                </Button>
              </DialogFooter>
            </DialogContent>
          </Dialog>
        </div>
      </div>

      <Card className="overflow-hidden">
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead className="w-12">#</TableHead>
              <TableHead>标签</TableHead>
              <TableHead className="w-24">角色</TableHead>
              <TableHead className="w-24">类型</TableHead>
              <TableHead className="w-28">凭据</TableHead>
              <TableHead className="w-24">状态</TableHead>
              <TableHead className="w-44">最近探活</TableHead>
              <TableHead className="w-52">操作</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {rows.length === 0 ? (
              <TableRow>
                <TableCell colSpan={8} className="text-muted-foreground py-12 text-center text-sm">
                  {loading ? "加载中…" : "暂无测试账号"}
                </TableCell>
              </TableRow>
            ) : (
              rows.map((a) => (
                <TableRow key={a.id}>
                  <TableCell className="text-muted-foreground text-xs">{a.id}</TableCell>
                  <TableCell className="font-medium">{a.label}</TableCell>
                  <TableCell>
                    <Badge variant="secondary">{ROLE_LABELS[a.role_tier] ?? a.role_tier}</Badge>
                  </TableCell>
                  <TableCell className="text-muted-foreground text-xs">{a.cred_type}</TableCell>
                  <TableCell className="font-mono text-xs">{a.cred_masked}</TableCell>
                  <TableCell>
                    <Badge variant={a.status === "active" ? "default" : "outline"}>{a.status}</Badge>
                  </TableCell>
                  <TableCell className="text-muted-foreground text-xs">
                    {fmtTime(a.last_checked_at)}
                    {a.check_note ? <div className="line-clamp-1">{a.check_note}</div> : null}
                  </TableCell>
                  <TableCell>
                    <div className="flex gap-1.5">
                      <Button size="sm" variant="outline" onClick={() => check(a)}>
                        探活
                      </Button>
                      <Button size="sm" variant="outline" onClick={() => rotate(a)}>
                        换凭据
                      </Button>
                      <Button size="sm" variant="ghost" onClick={() => remove(a)}>
                        删除
                      </Button>
                    </div>
                  </TableCell>
                </TableRow>
              ))
            )}
          </TableBody>
        </Table>
      </Card>
    </div>
  );
}
